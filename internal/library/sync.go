package library

import (
	"fmt"
	"path"
	"slices"
	"time"

	"golang.org/x/text/unicode/norm"

	"ssss/internal/model"
)

// Row is a sample with everything the local index stores about it.
type Row struct {
	model.Sample
	Note string
}

// PackFile describes a pack and the JSON file it was last read from or
// written to, so an index can tell whether it already holds that version.
type PackFile struct {
	model.PackInfo
	Added time.Time
	// MTime (unix nanoseconds) and Size are zero while the pack has changes
	// that are not on disk yet.
	MTime int64
	Size  int64
}

// CollectionList is a collection with its members in saved order.
type CollectionList struct {
	ID    string
	Name  string
	Items []string
}

func (l *Library) toRow(p *pack, id string) Row {
	rec := p.Samples[id]
	return Row{Sample: l.toSample(p, id, rec), Note: rec.Note}
}

// Rows returns the index rows for the IDs that exist, in the order asked.
func (l *Library) Rows(ids []string) []Row {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Row, 0, len(ids))
	for _, id := range ids {
		if p := l.st.byID[id]; p != nil {
			out = append(out, l.toRow(p, id))
		}
	}
	return out
}

// PackRows returns the index rows of every sample the pack owns.
func (l *Library) PackRows(packID string) []Row {
	l.mu.RLock()
	defer l.mu.RUnlock()
	p := l.st.packs[packID]
	if p == nil {
		return nil
	}
	out := make([]Row, 0, len(p.Samples))
	for _, id := range sortedKeys(p.Samples) {
		if l.st.byID[id] == p {
			out = append(out, l.toRow(p, id))
		}
	}
	return out
}

// PackFiles lists every pack with the stamp of its file, ordered by ID.
func (l *Library) PackFiles() []PackFile {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]PackFile, 0, len(l.st.packs))
	for _, id := range sortedKeys(l.st.packs) {
		p := l.st.packs[id]
		n := 0
		for sid := range p.Samples {
			if l.st.byID[sid] == p {
				n++
			}
		}
		pf := PackFile{PackInfo: packInfo(p, n), Added: p.Added}
		if _, dirty := l.dirty.packs[id]; !dirty {
			stamp := l.stamps[path.Join(packsDirName, p.file)]
			pf.MTime, pf.Size = stamp.mtime, stamp.size
		}
		out = append(out, pf)
	}
	return out
}

// CollectionLists returns every collection with its members, ordered by ID.
// Members that have been removed from the library are included.
func (l *Library) CollectionLists() []CollectionList {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]CollectionList, 0, len(l.st.collections))
	for _, id := range sortedKeys(l.st.collections) {
		c := l.st.collections[id]
		out = append(out, CollectionList{ID: c.ID, Name: c.Name, Items: slices.Clone(c.Items)})
	}
	return out
}

// MoveSample points a record at a file that was moved outside the app,
// possibly into another pack's folder. rel is relative to the folder of
// packID. Nothing is moved on disk and the change is not undoable.
func (l *Library) MoveSample(id, packID, rel string) (Change, error) {
	ch, err := l.mutate(func(t *tx) error {
		src := l.st.byID[id]
		if src == nil {
			return fmt.Errorf("sample %s: %w", id, ErrNotFound)
		}
		dst := l.st.packs[packID]
		if dst == nil {
			return fmt.Errorf("pack %s: %w", packID, ErrNotFound)
		}
		if owner, taken := l.st.pathKeys[sampleKey(dst, rel)]; taken && owner != id {
			return fmt.Errorf("%w: %s", ErrPathTaken, rel)
		}
		if src != dst {
			rec := src.Samples[id]
			delete(l.st.pathKeys, sampleKey(src, rec.Path))
			delete(src.Samples, id)
			rec.Path = norm.NFC.String(rel)
			rec.Rev = now()
			dst.Samples[id] = rec
			delete(dst.Removed, id)
			l.st.byID[id] = dst
			l.st.pathKeys[sampleKey(dst, rec.Path)] = id
			l.dirtyPack(src.ID)
			l.dirtyPack(dst.ID)
			t.touch(id)
			t.structural = true
		} else {
			t.setPath(dst, id, rel)
		}
		delete(l.missing, id)
		return nil
	})
	ch.Undo = nil
	return ch, err
}
