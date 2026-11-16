package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func (l *Library) absPath(p *pack, rel string) string {
	return filepath.Join(l.root, samplesDirName, filepath.FromSlash(p.Dir), filepath.FromSlash(rel))
}

func (l *Library) trashPath(id, rel string) string {
	return filepath.Join(l.TrashDir(), id+strings.ToLower(path.Ext(rel)))
}

// relLimit is the length budget for paths inside p, so that
// Samples/<dir>/<rel> stays within what Windows accepts.
func relLimit(p *pack) int {
	return max(40, maxRelPathChars-utf8.RuneCountInString(p.Dir)-1)
}

// freePath returns rel, or rel with a " (n)" suffix, such that no sample in
// the library and no file on disk already occupies it.
func (l *Library) freePath(p *pack, rel, id string) string {
	limit := relLimit(p)
	for n := 1; n < 1000; n++ {
		cand := withSuffix(rel, n, limit)
		if _, taken := l.st.pathKeys[sampleKey(p, cand)]; taken {
			continue
		}
		if _, err := os.Lstat(l.absPath(p, cand)); err == nil {
			continue
		}
		return cand
	}
	// Only reachable when truncation swallows the counter; the ID is unique.
	return path.Join(path.Dir(rel), id+path.Ext(rel))
}

// pruneEmptyDirs removes dir and its parents while they are empty, stopping
// at the Samples folder.
func (l *Library) pruneEmptyDirs(dir string) {
	samples := l.SamplesDir()
	for dir != samples && strings.HasPrefix(dir, samples+string(filepath.Separator)) {
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// insertSample adds a record at the already-final path rec.Path, with the
// revision the caller gave it. place, if set, puts the file there; it runs
// under the library lock, so keep it to a rename.
func (t *tx) insertSample(packID, id string, rec SampleRecord, place func(abs string) error) error {
	l, st := t.l, t.l.st
	p := st.packs[packID]
	if p == nil {
		return fmt.Errorf("pack %s: %w", packID, ErrNotFound)
	}
	if st.byID[id] != nil {
		return ErrDuplicate
	}
	key := sampleKey(p, rec.Path)
	if _, taken := st.pathKeys[key]; taken {
		return fmt.Errorf("%w: %s", ErrPathTaken, rec.Path)
	}
	if place != nil {
		// Folders are only made inside a library that is still there; see
		// Available.
		if err := l.Available(); err != nil {
			return err
		}
		abs := l.absPath(p, rec.Path)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := place(abs); err != nil {
			l.pruneEmptyDirs(filepath.Dir(abs))
			return err
		}
	}
	p.Samples[id] = &rec
	st.byID[id] = p
	st.pathKeys[key] = id
	delete(p.Removed, id)
	l.dirtyPack(p.ID)
	t.touch(id)
	t.structural = true
	t.onUndo(func(t *tx) error { return t.removeSample(id) })
	return nil
}

// removeSample moves the file to the trash, drops the record and leaves a
// tombstone so other machines (and a rescan) know the removal was intended.
func (t *tx) removeSample(id string) error {
	l, st := t.l, t.l.st
	p := st.byID[id]
	if p == nil {
		return nil
	}
	rec := *p.Samples[id]
	abs := l.absPath(p, rec.Path)
	if _, err := os.Lstat(abs); err == nil {
		if err := l.Available(); err != nil {
			return err
		}
		if err := os.Mkdir(l.TrashDir(), 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := os.Rename(abs, l.trashPath(id, rec.Path)); err != nil {
			return err
		}
		l.pruneEmptyDirs(filepath.Dir(abs))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	delete(p.Samples, id)
	delete(st.byID, id)
	delete(st.pathKeys, sampleKey(p, rec.Path))
	delete(l.missing, id)
	removed := now()
	p.Removed[id] = removed
	l.dirtyPack(p.ID)
	// Another pack file may list the same content. The sample is removed
	// from the library, not from one pack: left in the other it would come
	// back pointing at the file that has just gone to the trash.
	for _, other := range st.packs {
		if other.Samples[id] != nil {
			delete(other.Samples, id)
			other.Removed[id] = removed
			l.dirtyPack(other.ID)
		}
	}
	t.touch(id)
	t.structural = true
	packID := p.ID
	t.onUndo(func(t *tx) error { return t.restoreSample(packID, id, rec) })
	return nil
}

// restoreSample undoes removeSample: the file comes back from the trash, to
// a fresh name if its old one has been taken in the meantime.
func (t *tx) restoreSample(packID, id string, rec SampleRecord) error {
	l := t.l
	p := l.st.packs[packID]
	if p == nil {
		return fmt.Errorf("pack %s: %w", packID, ErrNotFound)
	}
	if l.st.byID[id] != nil {
		return nil
	}
	trashed := l.trashPath(id, rec.Path)
	rec.Path = l.freePath(p, rec.Path, id)
	rec.Rev = now()
	return t.insertSample(packID, id, rec, func(abs string) error {
		err := os.Rename(trashed, abs)
		if errors.Is(err, fs.ErrNotExist) {
			return nil // the file was already missing when it was removed
		}
		return err
	})
}

// renamePath moves a sample's file within its pack.
func (t *tx) renamePath(id, newRel string) error {
	l, st := t.l, t.l.st
	p := st.byID[id]
	if p == nil {
		return nil
	}
	rec := p.Samples[id]
	oldRel := rec.Path
	if oldRel == newRel {
		return nil
	}
	newKey := sampleKey(p, newRel)
	if owner, taken := st.pathKeys[newKey]; taken && owner != id {
		return fmt.Errorf("%w: %s", ErrPathTaken, path.Base(newRel))
	}
	if err := l.Available(); err != nil {
		return err
	}
	oldAbs, newAbs := l.absPath(p, oldRel), l.absPath(p, newRel)
	if target, err := os.Lstat(newAbs); err == nil {
		// On a case-insensitive file system a case-only rename finds itself.
		if current, err := os.Lstat(oldAbs); err != nil || !os.SameFile(target, current) {
			return fmt.Errorf("%w: %s", ErrPathTaken, path.Base(newRel))
		}
	}
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return err
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		return err
	}
	l.pruneEmptyDirs(filepath.Dir(oldAbs))
	t.setPath(p, id, newRel)
	t.onUndo(func(t *tx) error { return t.renamePath(id, oldRel) })
	return nil
}

// setPath updates the record only; the file is already at rel.
func (t *tx) setPath(p *pack, id, rel string) {
	rec := p.Samples[id]
	delete(t.l.st.pathKeys, sampleKey(p, rec.Path))
	rec.Path = norm.NFC.String(rel)
	rec.Rev = now()
	t.l.st.pathKeys[sampleKey(p, rec.Path)] = id
	t.l.dirtyPack(p.ID)
	t.touch(id)
}
