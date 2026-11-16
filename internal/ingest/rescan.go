package ingest

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"ssss/internal/library"
	"ssss/internal/model"
)

const (
	// missingStopShare is the share of samples a scan may newly flag as
	// missing before it assumes the folder is only partly there (a sync in
	// progress, a half-mounted drive) and stops instead.
	missingStopShare = 0.2
	// missingStopFloor keeps the safety stop from firing in tiny libraries,
	// where deleting two files of five is an ordinary thing to do.
	missingStopFloor = 10
	// maxPackDir mirrors the length library.CreatePack allows a pack folder.
	maxPackDir = 80
)

// RescanOptions tune a rescan.
type RescanOptions struct {
	// Quiet suppresses progress events unless files need hashing; for the
	// automatic rescans on startup and window focus.
	Quiet bool
	// AllowMassMissing lets the scan flag any number of samples as
	// missing, for when the user has confirmed that is what they want.
	AllowMassMissing bool
}

// RescanResult says what a rescan found.
type RescanResult struct {
	Reloaded bool // metadata changed on disk and was re-read
	Added    int  // files found in Samples that were not in the library
	Healed   int  // samples whose file had been moved or renamed outside the app
	Changed  int  // samples whose file now holds other audio
	Missing  int  // samples newly flagged as missing
	Found    int  // samples whose file is back
	// Ignored counts files holding audio the user removed from the library
	// earlier; they are left where they are and not taken back in.
	Ignored int
	// Waiting counts new files that were seen but not adopted yet, because
	// the pack file describing them may still be on its way.
	Waiting int
	Failed  int // files that could not be taken in
	// MissingStopped is non-zero when the safety stop fired: that many
	// samples have no file, too many to flag without asking.
	MissingStopped int
	Cancelled      bool
}

// diskFile is a file found under Samples that no record points at.
type diskFile struct {
	abs  string
	rel  string // slash-separated, relative to Samples
	stat fileStat
}

// knownFile is a sample's file whose size or time is not what was last
// verified.
type knownFile struct {
	id   string
	abs  string
	stat fileStat
}

// hashedFile caches the content hash of a file version.
type hashedFile struct {
	stat fileStat
	id   string
}

// Rescan reconciles the library with what is on disk: metadata written by
// another machine (including conflict copies) is merged in, files are
// matched to records by path, files moved outside the app are recognised by
// content and healed, files overwritten in place are noticed, new files are
// adopted into packs and records without a file are flagged missing. It
// blocks until done; ErrBusy means another rescan is running.
func (e *Engine) Rescan(opts RescanOptions) (res RescanResult, err error) {
	if !e.rescanMu.TryLock() {
		return res, ErrBusy
	}
	defer e.rescanMu.Unlock()
	j, ctx := e.startJob(model.JobRescan)
	loud := !opts.Quiet
	defer func() {
		res.Cancelled = ctx.Err() != nil
		j.finish(res.Cancelled, !loud)
	}()
	if loud {
		j.update(func(p *model.Progress) { p.Current = "Reading library" })
	}
	ch, warnings, err := e.lib.Reload()
	if err != nil {
		return res, err
	}
	// A file that stays unreadable is reported once, not at every rescan.
	warned := make(map[string]struct{}, len(warnings))
	for _, w := range warnings {
		warned[w] = struct{}{}
		if _, old := e.warned[w]; !old {
			e.fail(fmt.Errorf("skipped %s", w))
		}
	}
	e.warned = warned
	if !ch.Empty() {
		res.Reloaded = true
		e.record(ch, 0)
		// Another machine may have imported without finishing analysis.
		e.AnalyzePending()
	}
	readOnly := e.lib.ReadOnly()
	if !readOnly {
		e.sweepTemps()
	}

	present, unknown, changed, err := e.walkSamples(ctx)
	if err != nil {
		return res, err
	}
	var absent []string
	samples := e.lib.Samples()
	for _, s := range samples {
		if _, ok := present[s.ID]; ok {
			continue
		}
		// An import running alongside may have added the sample after the
		// walk passed its folder; only a failed stat makes it absent.
		if abs, ok := e.lib.AbsPath(s.ID); ok {
			if _, err := os.Lstat(abs); err != nil {
				absent = append(absent, s.ID)
			}
		}
	}
	stale, err := e.idx.SearchIDs("is:missing", model.SortAdded, false)
	if err != nil {
		return res, err
	}
	if len(unknown)+len(changed) > 0 && !readOnly {
		// Files already hashed and merely waiting are no work worth showing.
		loud = loud || len(changed) > 0 || slices.ContainsFunc(unknown, func(f diskFile) bool {
			return e.waiting[f.rel].stat != f.stat
		})
		j.update(func(p *model.Progress) { p.Total = len(unknown) + len(changed) })
		e.recheck(ctx, j, changed, &res)
		healed := e.identify(ctx, j, unknown, absent, opts.Quiet, &res)
		absent = filterOut(absent, healed)
	}
	e.verified.save(present)
	if ctx.Err() != nil {
		return res, nil
	}

	wasMissing := make(map[string]struct{}, len(stale))
	for _, id := range stale {
		wasMissing[id] = struct{}{}
	}
	var newly []string
	for _, id := range absent {
		if _, ok := wasMissing[id]; !ok {
			newly = append(newly, id)
		}
	}
	if !opts.AllowMassMissing && len(newly) >= missingStopFloor &&
		float64(len(newly)) > missingStopShare*float64(len(samples)) {
		res.MissingStopped = len(newly)
		absent = filterOut(absent, toSet(newly))
	} else {
		res.Missing = len(newly)
	}
	// Samples the index already had as missing are flagged again too: the
	// library keeps the flag in memory only and starts each session blank.
	if len(absent) > 0 {
		ch := e.lib.SetMissing(absent, true)
		e.fail(e.idx.SetMissing(absent, true))
		e.changes.add(ch)
	}
	absentSet := toSet(absent)
	var back []string
	for _, id := range stale {
		if _, gone := absentSet[id]; !gone {
			back = append(back, id)
		}
	}
	if len(back) > 0 {
		e.lib.SetMissing(back, false)
		e.fail(e.idx.SetMissing(back, false))
		e.changes.add(library.Change{IDs: back})
		res.Found = len(back)
	}
	return res, nil
}

// sameFile reports whether abs names the file info describes.
func sameFile(abs string, info fs.FileInfo) bool {
	other, err := os.Lstat(abs)
	return err == nil && os.SameFile(other, info)
}

// walkSamples stats everything under Samples. It returns the samples whose
// recorded path exists, those among them whose file does not look like the
// version last verified, and the audio-looking files no record points at.
func (e *Engine) walkSamples(ctx context.Context) (present map[string]struct{}, unknown []diskFile, changed []knownFile, err error) {
	root := e.lib.SamplesDir()
	present = map[string]struct{}{}
	if e.verified == nil {
		e.verified = loadStatCache(e.cacheDir)
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if werr != nil {
			if p == root {
				return fmt.Errorf("%w: %v", library.ErrUnavailable, werr)
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == root {
			return nil
		}
		if skipName(d.Name()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil // gone while walking
		}
		stat := fileStat{size: info.Size(), mtime: info.ModTime()}
		// Records are matched to paths ignoring case and Unicode form, the
		// file system may not be that lenient: a match only counts if the
		// record's own path leads to this very file. Otherwise the file is
		// treated as unknown, which lets identify heal the record.
		if sf, ok := e.lib.SampleFileForPath(p); ok && (sf.Abs == p || sameFile(sf.Abs, info)) {
			present[sf.ID] = struct{}{}
			if e.verified.differs(sf, stat) {
				changed = append(changed, knownFile{id: sf.ID, abs: p, stat: stat})
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		f := diskFile{abs: p, rel: filepath.ToSlash(rel), stat: stat}
		if e.ignored[f.rel] != f.stat {
			unknown = append(unknown, f)
		}
		return nil
	})
	return present, unknown, changed, err
}

// recheck hashes the samples whose file changed size or time. Same content
// means the file was merely touched. Other content means it was edited or
// replaced in place: the sample keeps its identity and everything the user
// gave it, and is analysed again.
func (e *Engine) recheck(ctx context.Context, j *job, changed []knownFile, res *RescanResult) {
	var again []string
	for _, f := range changed {
		if ctx.Err() != nil {
			break
		}
		name := filepath.Base(f.abs)
		j.update(func(p *model.Progress) { p.Current = name })
		hash, err := HashFile(ctx, f.abs)
		switch {
		case err != nil && ctx.Err() != nil:
			return
		case err != nil:
			j.addError(name, unwrapPath(err))
			res.Failed++
		case hash == f.id:
			e.verified.set(f.id, f.stat)
		default:
			ch, err := e.lib.FileChanged(f.id, f.stat.size, f.stat.mtime.UTC())
			if err != nil {
				j.addError(name, err)
				res.Failed++
				break
			}
			e.record(ch, 0)
			e.verified.set(f.id, f.stat)
			again = append(again, f.id)
			res.Changed++
		}
		j.update(func(p *model.Progress) { p.Done++ })
	}
	e.analysis.enqueue(again)
}

// identify hashes the files no record points at. Content the library knows
// whose own file is absent has been moved: the record is pointed at the new
// place. Unknown content is adopted as a new sample. It returns the samples
// it healed.
//
// Adoption waits whenever the library may not know everything yet. While a
// pack file cannot be read, nothing is adopted: its samples would all look
// new. And an automatic (quiet) rescan does not start a pack for a folder
// the first time it sees a file there, because on a machine that is still
// syncing the audio regularly arrives before the pack file describing it;
// the file is adopted by the next rescan that finds it unchanged, or by one
// the user asks for.
func (e *Engine) identify(ctx context.Context, j *job, unknown []diskFile, absent []string, quiet bool, res *RescanResult) map[string]struct{} {
	gone := toSet(absent)
	healed := map[string]struct{}{}
	packs := newPackDirs(e.lib.Packs())
	incomplete := slices.ContainsFunc(e.lib.Unreadable(), func(rel string) bool {
		return strings.HasPrefix(rel, "packs/")
	})
	waiting := map[string]hashedFile{}
	var adopted []string
	for _, f := range unknown {
		if ctx.Err() != nil {
			break
		}
		name := path.Base(f.rel)
		j.update(func(p *model.Progress) { p.Current = name })
		format, ok := IsAudio(f.abs)
		if !ok {
			e.ignored[f.rel] = f.stat
			j.update(func(p *model.Progress) { p.Done++ })
			continue
		}
		seen, wasSeen := e.waiting[f.rel]
		wasSeen = wasSeen && seen.stat == f.stat
		id := seen.id
		if !wasSeen {
			var err error
			if id, err = HashFile(ctx, f.abs); err != nil {
				if ctx.Err() == nil {
					j.addError(name, unwrapPath(err))
					res.Failed++
					j.update(func(p *model.Progress) { p.Done++ })
				}
				continue
			}
		}
		_, isGone := gone[id]
		_, isHealed := healed[id]
		_, _, hasPack := packs.find(f.rel)
		switch {
		case isGone && !isHealed:
			if err := e.heal(packs, id, f); err != nil {
				e.ignored[f.rel] = f.stat
				j.addError(name, err)
				res.Failed++
			} else {
				healed[id] = struct{}{}
				res.Healed++
			}
		case e.lib.Has(id):
			// A second copy of something in the library: leave it be.
			e.ignored[f.rel] = f.stat
			j.update(func(p *model.Progress) { p.Duplicates++ })
		case e.lib.Tombstoned(id):
			// Removed by the user and back on disk. Usually a sync client
			// restored it, so it stays out; but it is counted, so that
			// someone who put it back by hand learns why it does not show.
			e.ignored[f.rel] = f.stat
			res.Ignored++
		case incomplete, quiet && !hasPack && !wasSeen && strings.Contains(f.rel, "/"):
			waiting[f.rel] = hashedFile{stat: f.stat, id: id}
			res.Waiting++
		default:
			if err := e.adopt(packs, id, f, string(format)); err != nil {
				e.ignored[f.rel] = f.stat
				j.addError(name, err)
				res.Failed++
			} else {
				adopted = append(adopted, id)
				res.Added++
				j.update(func(p *model.Progress) { p.Added++ })
			}
		}
		j.update(func(p *model.Progress) { p.Done++ })
	}
	if ctx.Err() == nil {
		e.waiting = waiting
	}
	e.analysis.enqueue(adopted)
	return healed
}

// settle makes sure the file found at f.abs is reachable under the path a
// record in packID with the in-pack path rel resolves to, and returns that
// path. Packs and records are matched to folders and files ignoring case
// and Unicode form, but on most Linux file systems "Kit" and "kit", or a
// composed and a decomposed "é", are different names. A file that does not
// answer to the record's spelling is moved to it (the Samples tree is the
// library's own); if something else is in the way, the file is refused.
func (e *Engine) settle(packID, rel string, f diskFile) error {
	want, ok := e.lib.PathIn(packID, rel)
	if !ok {
		return fmt.Errorf("pack %s: %w", packID, library.ErrNotFound)
	}
	if want == f.abs {
		return nil
	}
	if there, err := os.Lstat(want); err == nil {
		if here, err := os.Lstat(f.abs); err == nil && os.SameFile(there, here) {
			return nil // one file under two spellings
		}
		return fmt.Errorf("%w: %s", library.ErrPathTaken, rel)
	}
	if err := e.lib.Available(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		return err
	}
	if err := os.Rename(f.abs, want); err != nil {
		return err
	}
	// Leave no empty folder behind under the old spelling.
	samples := e.lib.SamplesDir()
	for dir := filepath.Dir(f.abs); dir != samples && strings.HasPrefix(dir, samples+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break
		}
	}
	return nil
}

func (e *Engine) heal(packs *packDirs, id string, f diskFile) error {
	packID, rel, err := packs.locate(e, f.rel)
	if err != nil {
		return err
	}
	if err := e.settle(packID, rel, f); err != nil {
		return err
	}
	ch, err := e.lib.MoveSample(id, packID, rel)
	if err != nil {
		return err
	}
	e.record(ch, 0)
	e.fail(e.idx.SetMissing([]string{id}, false))
	return nil
}

func (e *Engine) adopt(packs *packDirs, id string, f diskFile, format string) error {
	packID, rel, err := packs.locate(e, f.rel)
	if err != nil {
		return err
	}
	if err := e.settle(packID, rel, f); err != nil {
		return err
	}
	rec := library.SampleRecord{Path: rel, Fmt: format, Size: f.stat.size, MTime: f.stat.mtime.UTC()}
	ch, err := e.lib.AdoptSample(packID, id, rec)
	if err != nil {
		return err
	}
	e.record(ch, 0)
	e.verified.set(id, f.stat)
	return nil
}

// packDirs maps folders under Samples to the packs stored in them.
type packDirs struct {
	byDir map[string]string // PathKey(dir) -> pack ID
}

func newPackDirs(packs []model.PackInfo) *packDirs {
	d := &packDirs{byDir: make(map[string]string, len(packs))}
	for _, p := range packs {
		d.byDir[library.PathKey(p.Dir)] = p.ID
	}
	return d
}

// find returns the existing pack whose folder holds rel (relative to
// Samples) and rel's path inside it.
func (d *packDirs) find(rel string) (packID, inPack string, ok bool) {
	parts := strings.Split(rel, "/")
	for n := len(parts) - 1; n > 0; n-- {
		if id, ok := d.byDir[library.PathKey(strings.Join(parts[:n], "/"))]; ok {
			return id, strings.Join(parts[n:], "/"), true
		}
	}
	return "", "", false
}

// locate finds the pack whose folder holds rel (relative to Samples) and
// rel's path inside it. A file under no pack's folder gets a new pack: the
// shallowest folder on its path that does not contain other packs, so
// "Splice/New Pack/x.wav" becomes pack "New Pack", not a pack "Splice"
// swallowing its neighbours. A file directly in Samples belongs to the
// Unsorted pack, as loose files do when they are imported; the caller moves
// it into that pack's folder under the free name returned here.
func (d *packDirs) locate(e *Engine, rel string) (packID, inPack string, err error) {
	if id, in, ok := d.find(rel); ok {
		return id, in, nil
	}
	parts := strings.Split(rel, "/")
	dirs := parts[:len(parts)-1]
	if len(dirs) == 0 {
		id, ok := d.byDir[library.PathKey(UnsortedPack)]
		if !ok {
			if id, err = d.create(e, UnsortedPack, UnsortedPack); err != nil {
				return "", "", err
			}
		}
		free, ok := e.lib.FreeRel(id, rel)
		if !ok {
			return "", "", fmt.Errorf("pack %s: %w", id, library.ErrNotFound)
		}
		return id, free, nil
	}
	depth := 1
	for depth < len(dirs) && d.containsPacks(strings.Join(dirs[:depth], "/")) {
		depth++
	}
	dir := strings.Join(dirs[:depth], "/")
	// A pack's folder is stored sanitised and at most maxPackDir long, so
	// a folder whose name is neither cannot be addressed through a pack.
	if d.containsPacks(dir) || utf8.RuneCountInString(dir) > maxPackDir ||
		library.PathKey(library.SanitizeRelPath(dir)) != library.PathKey(dir) {
		return "", "", fmt.Errorf("folder %q cannot be used as a pack", dir)
	}
	id, err := d.create(e, dirs[depth-1], dir)
	if err != nil {
		return "", "", err
	}
	return id, strings.Join(parts[depth:], "/"), nil
}

func (d *packDirs) create(e *Engine, name, dir string) (string, error) {
	info, ch, err := e.lib.CreatePack(name, dir, library.SourceScan)
	if err != nil {
		return "", err
	}
	e.record(ch, 0)
	d.byDir[library.PathKey(info.Dir)] = info.ID
	return info.ID, nil
}

func (d *packDirs) containsPacks(dir string) bool {
	prefix := library.PathKey(dir) + "/"
	for key := range d.byDir {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func toSet(ids []string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func filterOut(ids []string, drop map[string]struct{}) []string {
	if len(drop) == 0 {
		return ids
	}
	out := ids[:0:0]
	for _, id := range ids {
		if _, ok := drop[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}

// fileStat identifies a version of a file without reading it.
type fileStat struct {
	size  int64
	mtime time.Time
}
