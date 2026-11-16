package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// mergePack folds a conflict copy into dst: per sample the newest rev wins,
// tombstones are unioned, and a tombstone beats a sample unless the sample
// was touched (e.g. restored) after the removal.
func mergePack(dst, src *pack) {
	for id, s := range src.Samples {
		d, ok := dst.Samples[id]
		removed, tombstoned := dst.Removed[id]
		switch {
		case ok:
			// Analysis does not count as a revision, so of two records
			// with the same one the analysed is the more complete.
			if s.Rev.After(d.Rev) || s.Rev.Equal(d.Rev) && s.Analyzed && !d.Analyzed {
				dst.Samples[id] = s
			}
		case tombstoned && !s.Rev.After(removed):
			// removed here after the copy last touched it
		default:
			dst.Samples[id] = s
			delete(dst.Removed, id)
		}
	}
	for id, at := range src.Removed {
		if d, ok := dst.Samples[id]; ok {
			if d.Rev.After(at) {
				continue
			}
			delete(dst.Samples, id)
		}
		if at.After(dst.Removed[id]) {
			dst.Removed[id] = at
		}
	}
}

// mergeCollection keeps the newer name and the union of the items: losing an
// addition is worse than resurrecting a removal.
func mergeCollection(dst, src *collection) {
	items := dst.Items
	if src.Rev.After(dst.Rev) {
		dst.Name, dst.Rev = src.Name, src.Rev
	}
	seen := make(map[string]struct{}, len(items))
	for _, id := range items {
		seen[id] = struct{}{}
	}
	for _, id := range src.Items {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			items = append(items, id)
		}
	}
	dst.Items = items
}

func mergeTags(dst, src *tagsFile) {
	for tag, decor := range src.Tags {
		if _, ok := dst.Tags[tag]; !ok {
			dst.Tags[tag] = decor
		}
	}
}

func mergeViews(dst, src *viewsFile) {
	for _, v := range src.Views {
		if !slices.ContainsFunc(dst.Views, func(d view) bool { return d.ID == v.ID }) {
			dst.Views = append(dst.Views, v)
		}
	}
}

// mergeOneLocked folds one conflict copy into the in-memory state.
func (l *Library) mergeOneLocked(rel string) error {
	data, err := os.ReadFile(filepath.Join(metaDir(l.root), filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	dir, name := path.Split(rel)
	stem := conflictStem(name)
	st := l.st
	switch strings.TrimSuffix(dir, "/") {
	case packsDirName:
		src := &pack{}
		if err := json.Unmarshal(data, src); err != nil {
			return err
		}
		if src.ID == "" {
			src.ID = stem
		}
		if src.V > FormatVersion || !isSafeID(src.ID) {
			return errors.New("unsupported version or id")
		}
		if dst := st.packs[src.ID]; dst != nil {
			mergePack(dst, src)
		} else {
			src.file = src.ID + ".json"
			st.packs[src.ID] = src
		}
		l.dirtyPack(src.ID)
	case collectionsDirName:
		src := &collection{}
		if err := json.Unmarshal(data, src); err != nil {
			return err
		}
		if src.ID == "" {
			src.ID = stem
		}
		if src.V > FormatVersion || !isSafeID(src.ID) {
			return errors.New("unsupported version or id")
		}
		if dst := st.collections[src.ID]; dst != nil {
			mergeCollection(dst, src)
		} else {
			st.collections[src.ID] = src
		}
		l.dirtyCollection(src.ID)
	default:
		switch {
		case strings.HasPrefix(stem, "tags"):
			var src tagsFile
			if err := json.Unmarshal(data, &src); err != nil {
				return err
			}
			mergeTags(&st.tags, &src)
			l.dirty.tags = true
		case strings.HasPrefix(stem, "views"):
			var src viewsFile
			if err := json.Unmarshal(data, &src); err != nil {
				return err
			}
			mergeViews(&st.views, &src)
			l.dirty.views = true
		case strings.HasPrefix(stem, "library"):
			// Identity never changes after creation; the copy is redundant.
		default:
			return errors.New("unrecognised conflict copy")
		}
	}
	return nil
}

// mergeConflictsHeld merges the given conflict copies, writes the result
// and only then moves the copies to the trash, so a crash in between loses
// nothing. The caller holds writeMu. merged reports whether the state
// changed; the error lists the copies that could not be merged.
func (l *Library) mergeConflictsHeld(conflicts []string) (merged bool, err error) {
	l.mu.Lock()
	if len(conflicts) == 0 || l.readOnly {
		l.mu.Unlock()
		return false, nil
	}
	var done []string
	var errs []error
	for _, rel := range conflicts {
		if err := l.mergeOneLocked(rel); err != nil {
			errs = append(errs, fmt.Errorf("conflict copy %s: %w", rel, err))
			continue
		}
		done = append(done, rel)
	}
	l.foldLocked()
	l.st.reindex()
	l.mu.Unlock()

	absorbed, err := l.flushHeld()
	merged = absorbed || len(done) > 0
	if err != nil {
		return merged, errors.Join(append(errs, err)...)
	}
	for _, rel := range done {
		src := filepath.Join(metaDir(l.root), filepath.FromSlash(rel))
		if err := moveToTrash(src, l.TrashDir(), path.Base(rel)); err != nil {
			errs = append(errs, err)
			continue
		}
		l.mu.Lock()
		delete(l.stamps, rel)
		l.mu.Unlock()
	}
	return merged, errors.Join(errs...)
}

// moveToTrash moves src into the trash folder under name, never replacing a
// file that is already there.
func moveToTrash(src, trashDir, name string) error {
	// One level only: the trash is created inside an existing _library or
	// not at all.
	if err := os.Mkdir(trashDir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	dst := filepath.Join(trashDir, name)
	if _, err := os.Lstat(dst); err == nil {
		dst = filepath.Join(trashDir, strconv.FormatInt(time.Now().UnixNano(), 36)+" "+name)
	}
	return os.Rename(src, dst)
}

// Reload brings the state up to date with metadata files that changed on
// disk behind our back (another machine synced in, a conflict copy
// appeared), then writes whatever is pending. Changed files are merged into
// the live state, never swapped in for it, so mutations made while Reload
// runs are kept. The returned Change has All set when anything was taken
// over; it is empty when the files are as we left them. warnings name files
// that had to be skipped. A root that has become unreachable returns
// ErrUnavailable and keeps the in-memory state.
func (l *Library) Reload() (ch Change, warnings []string, err error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	l.mu.RLock()
	closed := l.closed
	l.mu.RUnlock()
	if closed {
		return Change{}, nil, ErrClosed
	}
	onDisk, err := scanMeta(l.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			err = fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return Change{}, nil, err
	}
	// library.json is always looked at; without it this is not our folder.
	differ := []string{libraryFileName}
	var conflicts []string
	l.mu.RLock()
	for rel, stamp := range onDisk {
		if was, ok := l.stamps[rel]; ok && was == stamp {
			continue
		}
		if isConflictCopy(path.Base(rel)) {
			conflicts = append(conflicts, rel)
		} else {
			differ = append(differ, rel)
		}
	}
	for rel := range l.stamps {
		if _, ok := onDisk[rel]; !ok {
			differ = append(differ, rel)
		}
	}
	l.mu.RUnlock()

	changed, warnings, err := l.absorbHeld(differ)
	if err != nil {
		return Change{}, warnings, err
	}
	slices.Sort(conflicts)
	merged, err := l.mergeConflictsHeld(conflicts)
	if err != nil {
		warnings = append(warnings, err.Error())
	}
	// A copy that could not be merged is not tried again until it changes.
	l.mu.Lock()
	for _, rel := range conflicts {
		if _, err := os.Lstat(filepath.Join(metaDir(l.root), filepath.FromSlash(rel))); err == nil {
			l.stamps[rel] = onDisk[rel]
		}
	}
	l.mu.Unlock()
	absorbed, err := l.flushHeld()
	switch {
	case errors.Is(err, ErrUnavailable):
		return Change{}, warnings, err
	case err != nil:
		warnings = append(warnings, err.Error())
	}
	if changed || merged || absorbed {
		ch = Change{All: true, Structural: true}
	}
	return ch, warnings, nil
}
