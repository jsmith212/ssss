package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"
)

type writeJob struct {
	rel    string // relative to _library, slash-separated
	data   []byte // nil => delete the file
	remark func(d *dirtySet)
	// stamp is the version of the file the in-memory state was last
	// reconciled with (had is false when there was no file). The file is
	// only replaced while that is still what is on disk.
	stamp fileStamp
	had   bool
}

// retrySteps scale Options.RetryDelay for the first, second and every
// further retry of a failed background write.
var retrySteps = [...]time.Duration{1, 3, 8}

// scheduleLocked arranges a debounced write. Callers hold mu.
func (l *Library) scheduleLocked() {
	if l.closed || l.dirty.empty() {
		return
	}
	if l.timer == nil {
		l.firstDirty = time.Now()
		l.timer = time.AfterFunc(l.opts.Debounce, l.backgroundFlush)
		return
	}
	if time.Since(l.firstDirty) < maxWriteDelay {
		l.timer.Reset(l.opts.Debounce)
	}
}

// backgroundFlush is the debounced write. A failed write is retried on its
// own with a growing delay: the change must not live in memory only until
// the user happens to make another one. Only the first failure of a streak
// is reported.
func (l *Library) backgroundFlush() {
	err := l.Flush()
	if err == nil {
		return
	}
	l.mu.Lock()
	l.failures++
	first := l.failures == 1
	if !l.closed && !l.dirty.empty() && l.timer == nil {
		step := retrySteps[min(l.failures, len(retrySteps))-1]
		l.firstDirty = time.Now()
		l.timer = time.AfterFunc(step*l.opts.RetryDelay, l.backgroundFlush)
	}
	l.mu.Unlock()
	if first && l.opts.OnWriteError != nil {
		l.opts.OnWriteError(err)
	}
}

// Flush writes every pending change to disk now. Call it on quit.
func (l *Library) Flush() error {
	l.writeMu.Lock()
	merged, err := l.flushHeld()
	l.writeMu.Unlock()
	l.announce(merged)
	return err
}

// announce tells the listener that files changed by someone else were
// merged into the state outside a Reload.
func (l *Library) announce(merged bool) {
	if !merged {
		return
	}
	l.mu.RLock()
	fn := l.onExternal
	l.mu.RUnlock()
	if fn != nil {
		fn(Change{All: true, Structural: true})
	}
}

// flushHeld does the work of Flush; the caller holds writeMu but not mu.
// Files are serialised under mu and written outside it so that readers and
// mutations never wait for the disk.
//
// A file is never written over a version this library has not seen: a sync
// client may have delivered another machine's edits since the file was last
// read. Such a file is merged into memory first (see absorbHeld) and the
// merged result is what gets written. merged reports that this happened, in
// which case the in-memory state changed beyond what the caller asked for.
func (l *Library) flushHeld() (merged bool, err error) {
	for attempt := 0; ; attempt++ {
		l.mu.Lock()
		if l.timer != nil {
			l.timer.Stop()
			l.timer = nil
		}
		if l.dirty.empty() || l.readOnly {
			l.mu.Unlock()
			return merged, nil
		}
		rels := l.dirtyRelsLocked()
		l.mu.Unlock()

		// library.json is always among the files checked: it is the proof
		// that root still is this library and not an empty mount point or a
		// folder someone moved another library into.
		changed, _, err := l.absorbHeld(append(rels, libraryFileName))
		merged = merged || changed
		if err != nil {
			return merged, err
		}

		l.mu.Lock()
		jobs, jobErr := l.takeJobsLocked()
		l.mu.Unlock()
		stale, writeErr := l.writeJobs(jobs)
		if stale && attempt < 2 {
			continue
		}
		if stale {
			writeErr = errors.Join(writeErr, errors.New("library files keep changing on disk"))
		}
		if err = errors.Join(jobErr, writeErr); err == nil {
			l.mu.Lock()
			l.failures = 0
			l.mu.Unlock()
		}
		return merged, err
	}
}

// statFile returns the stamp of a regular file; exists is false when there
// is none.
func statFile(abs string) (stamp fileStamp, exists bool, err error) {
	info, err := os.Stat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fileStamp{}, false, nil
	case err != nil:
		return fileStamp{}, false, err
	}
	return fileStamp{size: info.Size(), mtime: info.ModTime().UnixNano()}, true, nil
}

// writeJobs writes the serialised files. stale reports that a file changed
// on disk after it was reconciled; such jobs stay dirty for the next round.
// The check sits directly before each write, which leaves a sync client a
// window of microseconds; if it hits that, it sees two diverging versions
// and makes a conflict copy, which the next reload merges.
func (l *Library) writeJobs(jobs []writeJob) (stale bool, err error) {
	meta := metaDir(l.root)
	var errs []error
	var again []writeJob
	stamps := map[string]*fileStamp{}
	for _, job := range jobs {
		abs := filepath.Join(meta, filepath.FromSlash(job.rel))
		cur, exists, err := statFile(abs)
		if err != nil {
			errs = append(errs, err)
			again = append(again, job)
			continue
		}
		if exists != job.had || exists && cur != job.stamp {
			stale = true
			again = append(again, job)
			continue
		}
		if job.data == nil {
			if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
				again = append(again, job)
				continue
			}
			stamps[job.rel] = nil
			continue
		}
		err = writeFileAtomic(abs, job.data)
		if errors.Is(err, fs.ErrNotExist) {
			// The packs or collections folder can vanish under a sync
			// client. Only that one level is recreated: _library itself was
			// seen holding library.json a moment ago, and if it has gone
			// since, the library must not be rebuilt in its place.
			if mkErr := os.Mkdir(filepath.Dir(abs), 0o755); mkErr == nil {
				err = writeFileAtomic(abs, job.data)
			}
		}
		if err != nil {
			errs = append(errs, err)
			again = append(again, job)
			continue
		}
		if stamp, ok, err := statFile(abs); err == nil && ok {
			stamps[job.rel] = &stamp
		}
	}

	l.mu.Lock()
	for rel, stamp := range stamps {
		if stamp == nil {
			delete(l.stamps, rel)
		} else {
			l.stamps[rel] = *stamp
		}
	}
	for _, job := range again {
		job.remark(&l.dirty)
	}
	l.mu.Unlock()
	return stale, errors.Join(errs...)
}

// dirtyRelsLocked lists the files the next write will touch.
func (l *Library) dirtyRelsLocked() []string {
	d := &l.dirty
	var rels []string
	for rel := range d.remove {
		rels = append(rels, rel)
	}
	if d.tags {
		rels = append(rels, tagsFileName)
	}
	if d.views {
		rels = append(rels, viewsFileName)
	}
	for id := range d.packs {
		if p := l.st.packs[id]; p != nil {
			rels = append(rels, path.Join(packsDirName, p.file))
		}
	}
	for id := range d.collections {
		rels = append(rels, path.Join(collectionsDirName, id+".json"))
	}
	return rels
}

// takeJobsLocked serialises everything dirty and clears the dirty set. A
// file whose version on disk could not be read stays dirty and is reported:
// writing it would destroy what a sync client is still delivering.
func (l *Library) takeJobsLocked() ([]writeJob, error) {
	if l.dirty.empty() {
		return nil, nil
	}
	d := l.dirty
	l.dirty = newDirtySet()
	var jobs []writeJob
	var errs []error
	queue := func(rel string, data []byte, remark func(*dirtySet)) {
		if _, bad := l.unreadable[rel]; bad {
			remark(&l.dirty)
			errs = append(errs, fmt.Errorf("%s cannot be read (it may still be syncing); the changes to it are kept in memory and it is left as it is", rel))
			return
		}
		stamp, had := l.stamps[rel]
		jobs = append(jobs, writeJob{rel: rel, data: data, remark: remark, stamp: stamp, had: had})
	}
	add := func(rel string, v any, remark func(*dirtySet)) {
		data, err := json.Marshal(v)
		if err != nil {
			errs = append(errs, err)
			return
		}
		queue(rel, append(data, '\n'), remark)
	}
	for rel := range d.remove {
		queue(rel, nil, func(d *dirtySet) { d.remove[rel] = struct{}{} })
	}
	if d.lib {
		l.st.lib.V = FormatVersion
		add(libraryFileName, l.st.lib, func(d *dirtySet) { d.lib = true })
	}
	if d.tags {
		l.st.tags.V = FormatVersion
		add(tagsFileName, l.st.tags, func(d *dirtySet) { d.tags = true })
	}
	if d.views {
		l.st.views.V = FormatVersion
		add(viewsFileName, l.st.views, func(d *dirtySet) { d.views = true })
	}
	for id := range d.packs {
		p := l.st.packs[id]
		if p == nil {
			continue
		}
		p.V = FormatVersion
		_, head := d.heads[id]
		add(path.Join(packsDirName, p.file), p, func(d *dirtySet) {
			d.packs[id] = struct{}{}
			if head {
				d.heads[id] = struct{}{}
			}
		})
	}
	for id := range d.collections {
		c := l.st.collections[id]
		if c == nil {
			continue
		}
		c.V = FormatVersion
		add(path.Join(collectionsDirName, id+".json"), c, func(d *dirtySet) { d.collections[id] = struct{}{} })
	}
	return jobs, errors.Join(errs...)
}

func (l *Library) dirtyPack(id string) { l.dirty.packs[id] = struct{}{} }

func (l *Library) dirtyCollection(id string) {
	l.dirty.collections[id] = struct{}{}
	delete(l.dirty.remove, path.Join(collectionsDirName, id+".json"))
}

// writableFileLocked refuses a change to tags.json or views.json while the
// copy on disk cannot be read: memory then holds nothing of what the file
// contains, and saving would replace it all.
func (l *Library) writableFileLocked(rel string) error {
	if _, bad := l.unreadable[rel]; bad {
		return fmt.Errorf("%w: %s cannot be read (it may still be syncing); rescan once it has arrived", ErrCorrupt, rel)
	}
	return nil
}
