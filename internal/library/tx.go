package library

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"ssss/internal/model"
)

// Op re-applies a recorded operation. Running the Undo of a Change reverts
// it and returns a Change whose own Undo redoes it, so an undo stack and a
// redo stack of Ops are all the caller needs. Ops address samples by ID and
// skip anything that has since disappeared.
type Op func() (Change, error)

// Change tells the caller what a mutation touched so it can update the
// index, notify the frontend and record undo.
type Change struct {
	// IDs are samples that were added or modified (re-read and upsert).
	IDs []string
	// Removed are samples that no longer exist.
	Removed []string
	// Structural is set when sidebar data may have changed: packs,
	// collections, tags, views or any of their counts.
	Structural bool
	// All means everything may have changed (reload, conflict merge).
	All bool
	// Undo reverts the mutation; nil when there is nothing to undo.
	Undo Op
}

// Empty reports whether the mutation changed nothing.
func (c Change) Empty() bool {
	return len(c.IDs) == 0 && len(c.Removed) == 0 && !c.Structural && !c.All
}

// step is one primitive mutation, replayable inside a later transaction.
type step func(t *tx) error

// tx collects what a mutation touched and how to revert it. Primitives
// (methods on tx) each record their own inverse, which is itself a
// primitive, so undo and redo come from the same code as the mutation.
type tx struct {
	l          *Library
	touched    map[string]struct{}
	structural bool
	undo       []step
}

func (t *tx) touch(id string) { t.touched[id] = struct{}{} }

func (t *tx) onUndo(s step) { t.undo = append(t.undo, s) }

// mutate runs fn as one all-or-nothing mutation under the write lock.
func (l *Library) mutate(fn func(t *tx) error) (Change, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return Change{}, ErrClosed
	}
	if l.readOnly {
		return Change{}, ErrReadOnly
	}
	t := &tx{l: l, touched: map[string]struct{}{}}
	err := fn(t)
	defer l.scheduleLocked()
	if err != nil {
		rollback := &tx{l: l, touched: map[string]struct{}{}}
		if rerr := replay(rollback, t.undo); rerr != nil {
			err = errors.Join(err, fmt.Errorf("rollback: %w", rerr))
		}
		return Change{}, err
	}
	ch := Change{Structural: t.structural}
	for id := range t.touched {
		if l.st.byID[id] != nil {
			ch.IDs = append(ch.IDs, id)
		} else {
			ch.Removed = append(ch.Removed, id)
		}
	}
	slices.Sort(ch.IDs)
	slices.Sort(ch.Removed)
	if steps := t.undo; len(steps) > 0 {
		ch.Undo = func() (Change, error) {
			return l.mutate(func(t *tx) error { return replay(t, steps) })
		}
	}
	return ch, nil
}

// replay runs recorded inverse steps, newest first.
func replay(t *tx, steps []step) error {
	for i := len(steps) - 1; i >= 0; i-- {
		if err := steps[i](t); err != nil {
			return err
		}
	}
	return nil
}

// field selects one group of SampleRecord fields for setField.
type field uint8

const (
	fFav field = 1 << iota
	fBPM
	fKey
	fType
	fNote
)

var allFields = [...]field{fFav, fBPM, fKey, fType, fNote}

func copyField(dst, src *SampleRecord, f field) {
	switch f {
	case fFav:
		dst.Fav = src.Fav
	case fBPM:
		dst.BPM, dst.BPMSrc = src.BPM, src.BPMSrc
	case fKey:
		dst.Key, dst.KeySrc = src.Key, src.KeySrc
	case fType:
		dst.Type, dst.TypeSrc = src.Type, src.TypeSrc
	case fNote:
		dst.Note = src.Note
	}
}

func equalField(a, b *SampleRecord, f field) bool {
	switch f {
	case fFav:
		return a.Fav == b.Fav
	case fBPM:
		return a.BPM == b.BPM && a.BPMSrc == b.BPMSrc
	case fKey:
		return a.Key == b.Key && a.KeySrc == b.KeySrc
	case fType:
		return a.Type == b.Type && a.TypeSrc == b.TypeSrc
	}
	return a.Note == b.Note
}

// fallBack fills an unset tempo, key or type from what analysis detected.
// An undo that takes a user's value away would otherwise leave the field
// blank for good: analysis ran while the user's value was in place and was
// not allowed to replace it.
func fallBack(vals *SampleRecord, f field, d *Detected) {
	if d == nil {
		return
	}
	switch {
	case f == fBPM && vals.BPM <= 0 && vals.BPMSrc != model.SrcUser && d.BPM > 0:
		vals.BPM, vals.BPMSrc = d.BPM, model.SrcDetected
	case f == fKey && vals.Key == "" && vals.KeySrc != model.SrcUser && d.Key != "":
		vals.Key, vals.KeySrc = d.Key, model.SrcDetected
	case f == fType && vals.Type == "" && vals.TypeSrc != model.SrcUser && d.Type != "":
		vals.Type, vals.TypeSrc = d.Type, model.SrcDetected
	}
}

// setFields copies the selected field groups of vals into sample id. An
// unknown id is skipped: selections and undo entries can outlive a sample.
func (t *tx) setFields(id string, m field, vals SampleRecord) {
	for _, f := range allFields {
		if m&f != 0 {
			t.setField(id, f, vals, nil)
		}
	}
}

// setField sets one field group. Its inverse restores the previous value,
// but only while the value written here is still in place (expect): a field
// that analysis, an import or another machine has changed since is theirs.
func (t *tx) setField(id string, f field, vals SampleRecord, expect *SampleRecord) {
	p := t.l.st.byID[id]
	if p == nil {
		return
	}
	rec := p.Samples[id]
	if expect != nil {
		if !equalField(rec, expect, f) {
			return
		}
		fallBack(&vals, f, rec.Detected)
	}
	if equalField(rec, &vals, f) {
		return
	}
	old := *rec
	copyField(rec, &vals, f)
	wrote := *rec
	rec.Rev = now()
	t.l.dirtyPack(p.ID)
	t.touch(id)
	if f == fFav {
		t.structural = true
	}
	t.onUndo(func(t *tx) error { t.setField(id, f, old, &wrote); return nil })
}

// placedTag is a tag and the position it goes to; a position past the end
// appends.
type placedTag struct {
	tag string
	at  int
}

func appendTags(tags []string) []placedTag {
	out := make([]placedTag, len(tags))
	for i, tag := range tags {
		out[i] = placedTag{tag, math.MaxInt}
	}
	return out
}

// editTags removes drop from the sample's tags and then inserts add. The
// inverse takes away exactly the tags this call added and puts back exactly
// the ones it removed, where they were, so tags that arrived in between (an
// import's drop target, another machine) survive an undo.
func (t *tx) editTags(id string, drop []string, add []placedTag) {
	p := t.l.st.byID[id]
	if p == nil {
		return
	}
	rec := p.Samples[id]
	var dropped []placedTag
	var added []string
	next := make([]string, 0, len(rec.Tags)+len(add))
	for i, tag := range rec.Tags {
		if slices.Contains(drop, tag) {
			dropped = append(dropped, placedTag{tag, i})
			continue
		}
		next = append(next, tag)
	}
	for _, a := range add {
		if slices.Contains(next, a.tag) {
			continue
		}
		next = slices.Insert(next, min(a.at, len(next)), a.tag)
		added = append(added, a.tag)
	}
	if len(dropped) == 0 && len(added) == 0 {
		return
	}
	rec.Tags = next
	rec.Rev = now()
	t.l.dirtyPack(p.ID)
	t.touch(id)
	t.structural = true
	t.onUndo(func(t *tx) error { t.editTags(id, added, dropped); return nil })
}

// setTagDecor sets or (with nil) deletes a tag's decoration entry.
func (t *tx) setTagDecor(tag string, d *tagDecor) {
	tags := t.l.st.tags.Tags
	old, had := tags[tag]
	if d == nil {
		if !had {
			return
		}
		delete(tags, tag)
	} else {
		tags[tag] = *d
	}
	t.l.dirty.tags = true
	t.structural = true
	t.onUndo(func(t *tx) error {
		if had {
			t.setTagDecor(tag, &old)
		} else {
			t.setTagDecor(tag, nil)
		}
		return nil
	})
}

// putCollection adds collection c, or deletes collection id when c is nil.
// Undoing a delete puts the collection back as it was; if one with that ID
// has reappeared in the meantime (another machine still had it) the two are
// merged instead of the newcomer being replaced.
func (t *tx) putCollection(id string, c *collection) {
	st := t.l.st
	old := st.collections[id]
	switch {
	case c == nil && old == nil:
		return
	case c == nil:
		delete(st.collections, id)
		delete(t.l.dirty.collections, id)
		t.l.dirty.remove[collectionsDirName+"/"+id+".json"] = struct{}{}
		for _, sid := range old.Items {
			t.touch(sid)
		}
		t.structural = true
		t.onUndo(func(t *tx) error { t.putCollection(id, old); return nil })
	case old == nil:
		st.collections[id] = c
		t.l.dirtyCollection(id)
		for _, sid := range c.Items {
			t.touch(sid)
		}
		t.structural = true
		t.onUndo(func(t *tx) error { t.putCollection(id, nil); return nil })
	default:
		t.editCollection(id, nil, appendItems(c.Items))
	}
}

// placedItem is a collection member and the position it goes to.
type placedItem struct {
	id string
	at int
}

func appendItems(ids []string) []placedItem {
	out := make([]placedItem, len(ids))
	for i, id := range ids {
		out[i] = placedItem{id, math.MaxInt}
	}
	return out
}

// editCollection removes drop from the collection and then inserts add. As
// with editTags, the inverse reverts exactly what this call did.
func (t *tx) editCollection(id string, drop []string, add []placedItem) {
	c := t.l.st.collections[id]
	if c == nil {
		return
	}
	var dropped []placedItem
	var added []string
	next := make([]string, 0, len(c.Items)+len(add))
	for i, sid := range c.Items {
		if slices.Contains(drop, sid) {
			dropped = append(dropped, placedItem{sid, i})
			continue
		}
		next = append(next, sid)
	}
	for _, a := range add {
		if slices.Contains(next, a.id) {
			continue
		}
		next = slices.Insert(next, min(a.at, len(next)), a.id)
		added = append(added, a.id)
	}
	if len(dropped) == 0 && len(added) == 0 {
		return
	}
	// A new slice: readers and conflict merges may hold the old one.
	c.Items = next
	c.Rev = now()
	t.l.dirtyCollection(id)
	for _, d := range dropped {
		t.touch(d.id)
	}
	for _, sid := range added {
		t.touch(sid)
	}
	t.structural = true
	t.onUndo(func(t *tx) error { t.editCollection(id, added, dropped); return nil })
}

// setCollectionName renames a collection. The inverse only applies while
// the name written here still stands.
func (t *tx) setCollectionName(id, name string, expect *string) {
	c := t.l.st.collections[id]
	if c == nil || c.Name == name || expect != nil && c.Name != *expect {
		return
	}
	old := c.Name
	c.Name = name
	c.Rev = now()
	t.l.dirtyCollection(id)
	t.structural = true
	t.onUndo(func(t *tx) error { t.setCollectionName(id, old, &name); return nil })
}

func (t *tx) setPackName(id, name string) {
	p := t.l.st.packs[id]
	if p == nil || p.Name == name {
		return
	}
	old := p.Name
	p.Name = name
	t.l.dirtyPack(id)
	t.l.dirty.heads[id] = struct{}{}
	for sid := range p.Samples {
		if t.l.st.byID[sid] == p {
			t.touch(sid) // rows carry the pack name
		}
	}
	t.structural = true
	t.onUndo(func(t *tx) error { t.setPackName(id, old); return nil })
}

// setView stores v under its ID, in place or at index at for a new one, or
// deletes view id when v is nil. Views are edited one at a time so that an
// undo leaves alone the ones another machine added since.
func (t *tx) setView(id string, v *view, at int) {
	views := t.l.st.views.Views
	i := slices.IndexFunc(views, func(have view) bool { return have.ID == id })
	var inverse step
	switch {
	case v == nil && i < 0:
		return
	case v == nil:
		old := views[i]
		views = slices.Delete(slices.Clone(views), i, i+1)
		inverse = func(t *tx) error { t.setView(id, &old, i); return nil }
	case i < 0:
		views = slices.Insert(slices.Clone(views), min(max(at, 0), len(views)), *v)
		inverse = func(t *tx) error { t.setView(id, nil, 0); return nil }
	default:
		old := views[i]
		if old.Name == v.Name && old.Query == v.Query {
			return
		}
		views = slices.Clone(views)
		views[i] = *v
		inverse = func(t *tx) error { t.setView(id, &old, i); return nil }
	}
	t.l.st.views.Views = views
	t.l.dirty.views = true
	t.structural = true
	t.onUndo(inverse)
}

// putPack adds a new, empty pack.
func (t *tx) putPack(p *pack) {
	t.l.st.packs[p.ID] = p
	t.l.dirtyPack(p.ID)
	delete(t.l.dirty.remove, packsDirName+"/"+p.file)
	t.structural = true
	t.onUndo(func(t *tx) error { return t.dropPack(p.ID) })
}

// dropPack deletes an empty pack and its file.
func (t *tx) dropPack(id string) error {
	p := t.l.st.packs[id]
	if p == nil {
		return nil
	}
	if len(p.Samples) > 0 {
		return fmt.Errorf("%w: %s", ErrNotEmpty, p.Name)
	}
	delete(t.l.st.packs, id)
	delete(t.l.dirty.packs, id)
	t.l.dirty.remove[packsDirName+"/"+p.file] = struct{}{}
	t.structural = true
	t.onUndo(func(t *tx) error { t.putPack(p); return nil })
	return nil
}
