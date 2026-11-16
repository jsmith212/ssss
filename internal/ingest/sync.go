package ingest

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"ssss/internal/index"
	"ssss/internal/library"
	"ssss/internal/model"
)

func indexPack(pf library.PackFile) index.Pack {
	p := index.Pack{
		ID: pf.ID, Name: pf.Name, Dir: pf.Dir, Source: pf.Source,
		FileMTime: pf.MTime, FileSize: pf.Size,
	}
	if !pf.Added.IsZero() {
		p.Added = pf.Added.UnixMilli()
	}
	return p
}

func indexSamples(rows []library.Row) []index.Sample {
	out := make([]index.Sample, len(rows))
	for i, r := range rows {
		out[i] = index.Sample{Sample: r.Sample, Note: r.Note}
	}
	return out
}

func packHeader(pf library.PackFile) model.PackInfo {
	info := pf.PackInfo
	info.Count = 0
	return info
}

// SyncIndex makes the index match the library. Unless force is set, packs
// whose JSON file has the stamp the index recorded for it are skipped, which
// makes the usual startup nearly free. A session that did not close cleanly
// forces a full pass.
func (e *Engine) SyncIndex(force bool) error {
	e.syncMu.Lock()
	defer e.syncMu.Unlock()
	if _, err := os.Stat(dirtyMarker(e.cacheDir)); err == nil && !e.marked {
		force = true
	}
	err := e.syncLocked(force)
	if err != nil {
		e.markLocked()
		e.stale = true
	}
	return err
}

func (e *Engine) syncLocked(force bool) error {
	indexed, err := e.idx.Packs()
	if err != nil {
		return err
	}
	have := make(map[string]index.Pack, len(indexed))
	for _, p := range indexed {
		have[p.ID] = p
	}
	e.packs = map[string]model.PackInfo{}
	var errs []error
	for _, pf := range e.lib.PackFiles() {
		e.packs[pf.ID] = packHeader(pf)
		want := indexPack(pf)
		got, ok := have[pf.ID]
		delete(have, pf.ID)
		if !force && ok && pf.MTime != 0 && got == want {
			continue
		}
		if err := e.idx.ReplacePack(want, indexSamples(e.lib.PackRows(pf.ID))); err != nil {
			errs = append(errs, err)
		}
	}
	for id := range have {
		if err := e.idx.DeletePack(id); err != nil {
			errs = append(errs, err)
		}
	}
	lists := e.lib.CollectionLists()
	cols := make([]index.Collection, len(lists))
	e.colls = make(map[string]library.CollectionList, len(lists))
	for i, c := range lists {
		cols[i] = index.Collection{ID: c.ID, Name: c.Name, Items: c.Items}
		e.colls[c.ID] = c
	}
	if err := e.idx.ReplaceCollections(cols); err != nil {
		errs = append(errs, err)
	}
	// A cache that was rebuilt, or filled from a library another machine
	// imported into, has no import batch to call Recent.
	if err := e.idx.DeriveRecent(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Apply updates the index for a change the library reported. Callers do
// their own notifying.
func (e *Engine) Apply(ch library.Change) error {
	return e.apply(ch, 0)
}

// apply is Apply with an import batch for newly added samples.
func (e *Engine) apply(ch library.Change, batch int64) error {
	if ch.Empty() {
		return nil
	}
	e.syncMu.Lock()
	defer e.syncMu.Unlock()
	e.markLocked()
	if e.repairLocked() {
		return nil // the full sync that repaired the index covered ch
	}
	err := e.applyLocked(ch, batch)
	if err != nil {
		e.stale = true
	}
	return err
}

// repairRetry spaces out attempts to repair a stale index: while the cause
// lasts (a full cache disk, another process holding the database) each one
// costs a pass over the whole library and fails again.
const repairRetry = 30 * time.Second

// repairLocked brings a stale index back in step with a full sync. It
// reports whether it did.
func (e *Engine) repairLocked() bool {
	if !e.stale || time.Since(e.lastRepair) < repairRetry {
		return false
	}
	e.lastRepair = time.Now()
	if e.syncLocked(true) != nil {
		return false
	}
	e.stale = false
	return true
}

func (e *Engine) applyLocked(ch library.Change, batch int64) error {
	if ch.All {
		return e.syncLocked(false)
	}
	var errs []error
	if ch.Structural {
		errs = append(errs, e.syncPacksLocked())
	}
	if len(ch.IDs) > 0 {
		errs = append(errs, e.idx.UpsertSamples(indexSamples(e.lib.Rows(ch.IDs)), batch))
	}
	if len(ch.Removed) > 0 {
		errs = append(errs, e.idx.DeleteSamples(ch.Removed))
	}
	if ch.Structural {
		errs = append(errs, e.syncCollectionsLocked())
	}
	return errors.Join(errs...)
}

// markLocked leaves the crash marker before the first index write of a
// session. Failing to write it only costs the shortcut at next startup if
// this session then crashes, so the error is not fatal.
func (e *Engine) markLocked() {
	if e.marked {
		return
	}
	e.marked = true
	if f, err := os.Create(dirtyMarker(e.cacheDir)); err == nil {
		f.Close()
	}
}

// syncPacksLocked mirrors created, renamed and dropped packs. The stamp is
// zeroed: the index no longer matches any file on disk until Close.
func (e *Engine) syncPacksLocked() error {
	var errs []error
	seen := map[string]struct{}{}
	for _, pf := range e.lib.PackFiles() {
		seen[pf.ID] = struct{}{}
		head := packHeader(pf)
		if old, ok := e.packs[pf.ID]; ok && old == head {
			continue
		}
		p := indexPack(pf)
		p.FileMTime, p.FileSize = 0, 0
		if err := e.idx.UpsertPack(p); err != nil {
			errs = append(errs, err)
			continue
		}
		e.packs[pf.ID] = head
	}
	for id := range e.packs {
		if _, ok := seen[id]; ok {
			continue
		}
		if err := e.idx.DeletePack(id); err != nil {
			errs = append(errs, err)
			continue
		}
		delete(e.packs, id)
	}
	return errors.Join(errs...)
}

func (e *Engine) syncCollectionsLocked() error {
	var errs []error
	seen := map[string]struct{}{}
	for _, c := range e.lib.CollectionLists() {
		seen[c.ID] = struct{}{}
		if old, ok := e.colls[c.ID]; ok && old.Name == c.Name && slices.Equal(old.Items, c.Items) {
			continue
		}
		if err := e.idx.UpsertCollection(index.Collection{ID: c.ID, Name: c.Name, Items: c.Items}); err != nil {
			errs = append(errs, err)
			continue
		}
		e.colls[c.ID] = c
	}
	for id := range e.colls {
		if _, ok := seen[id]; ok {
			continue
		}
		if err := e.idx.DeleteCollection(id); err != nil {
			errs = append(errs, err)
			continue
		}
		delete(e.colls, id)
	}
	return errors.Join(errs...)
}

// stampPacks records, after a flush, which version of each pack file the
// index reflects, and clears the crash marker. An index that missed a write
// is repaired first. If that fails nothing is stamped and the marker stays:
// a stamp would tell the next session the index is in step, and the missing
// change would stay out of search for good.
func (e *Engine) stampPacks() error {
	e.syncMu.Lock()
	defer e.syncMu.Unlock()
	if !e.marked {
		return nil
	}
	if e.stale {
		if err := e.syncLocked(true); err != nil {
			return fmt.Errorf("the local index is out of date and will be rebuilt at the next start: %w", err)
		}
		e.stale = false
	}
	var errs []error
	for _, pf := range e.lib.PackFiles() {
		// A pack that is still unwritten has a zero stamp, which never
		// matches a file, so the next sync reads it again.
		errs = append(errs, e.idx.UpsertPack(indexPack(pf)))
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	e.marked = false
	return removeIfExists(dirtyMarker(e.cacheDir))
}
