package index

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"ssss/internal/model"
)

// Pack is the pack-level data the index keeps.
type Pack struct {
	ID     string
	Name   string
	Dir    string
	Source string
	Added  int64 // unix ms
	// FileMTime (unix ns or ms, the caller's choice) and FileSize describe
	// the pack's JSON file as last indexed, so a rescan can skip unchanged
	// packs.
	FileMTime int64
	FileSize  int64
}

// Sample is what the index stores per sample. PackName is ignored on the way
// in (the pack row is authoritative) and filled on the way out. Missing is
// only honoured for a sample the index has not seen before; afterwards
// SetMissing owns the flag, because the pack files that drive upserts know
// nothing about what is on this machine's disk.
type Sample struct {
	model.Sample
	Note string
}

const tagSep = "\n"

// ReplacePack makes the index match one pack file: the pack row is upserted,
// its samples are upserted and samples the index had for the pack that are
// not in samples are dropped. Import batches of existing rows are kept.
func (ix *Index) ReplacePack(p Pack, samples []Sample) error {
	return ix.write(func(w *writer) error {
		if err := w.upsertPack(p); err != nil {
			return err
		}
		stale, err := w.packSamples(p.ID)
		if err != nil {
			return err
		}
		for i := range samples {
			s := samples[i]
			s.PackID = p.ID
			if err := w.upsertSample(s, 0); err != nil {
				return err
			}
			delete(stale, s.ID)
		}
		for _, rid := range stale {
			if err := w.deleteSample(rid); err != nil {
				return err
			}
		}
		return nil
	})
}

// UpsertPack inserts or updates a pack row without touching its samples,
// other than keeping their searchable pack name in step.
func (ix *Index) UpsertPack(p Pack) error {
	return ix.write(func(w *writer) error { return w.upsertPack(p) })
}

// DeletePack drops a pack and all its samples.
func (ix *Index) DeletePack(id string) error {
	return ix.write(func(w *writer) error {
		rids, err := w.packSamples(id)
		if err != nil {
			return err
		}
		for _, rid := range rids {
			if err := w.deleteSample(rid); err != nil {
				return err
			}
		}
		return w.exec(`DELETE FROM pack WHERE id = ?`, id)
	})
}

// Packs lists the indexed packs, for diffing against the pack files on disk.
func (ix *Index) Packs() ([]Pack, error) {
	rows, err := ix.db.Query(`SELECT id, name, dir, source, added, file_mtime, file_size FROM pack ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("index: packs: %w", err)
	}
	defer rows.Close()
	packs := []Pack{}
	for rows.Next() {
		var p Pack
		if err := rows.Scan(&p.ID, &p.Name, &p.Dir, &p.Source, &p.Added, &p.FileMTime, &p.FileSize); err != nil {
			return nil, fmt.Errorf("index: packs: %w", err)
		}
		packs = append(packs, p)
	}
	return packs, rows.Err()
}

// UpsertSamples inserts or updates samples by id. A non-zero batch (from
// NewBatch) stamps them as part of that import; zero keeps whatever batch
// existing rows have.
func (ix *Index) UpsertSamples(samples []Sample, batch int64) error {
	return ix.write(func(w *writer) error {
		for i := range samples {
			if err := w.upsertSample(samples[i], batch); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteSamples drops samples by id. Unknown ids are ignored. Collection
// membership is left alone: collections reference ids that may come back.
func (ix *Index) DeleteSamples(ids []string) error {
	return ix.write(func(w *writer) error {
		for _, id := range ids {
			rid, ok, err := w.rid(id)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if err := w.deleteSample(rid); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetMissing records whether the files of the given samples are absent from
// this machine's copy of the library.
func (ix *Index) SetMissing(ids []string, missing bool) error {
	return ix.write(func(w *writer) error {
		for _, id := range ids {
			if err := w.exec(`UPDATE sample SET missing = ? WHERE id = ?`, missing, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// NewBatch allocates the id for one import job. "Recent" is the newest batch
// that still has samples, so an import that only found duplicates does not
// blank the Recent view. Batches live in this machine's cache only and are
// forgotten by a rebuild.
func (ix *Index) NewBatch() (int64, error) {
	var batch int64
	err := ix.write(func(w *writer) error {
		row, err := w.queryRow(`
			INSERT INTO meta(k, v) VALUES('batch', 1)
			ON CONFLICT(k) DO UPDATE SET v = v + 1
			RETURNING v`)
		if err != nil {
			return err
		}
		return row.Scan(&batch)
	})
	return batch, err
}

// recentGap is how far apart two samples' added times may be for a derived
// Recent batch to still count them as one import.
const recentGap = 5 * 60 * 1000 // ms

// DeriveRecent gives a index that knows of no import batch one to show as
// Recent: the newest run of samples added without a pause longer than
// recentGap. Batches are this machine's memory of its own imports, which a
// rebuilt cache has lost and a library synced from another machine never
// had; the added times are in the library itself. It does nothing while any
// sample carries a batch.
func (ix *Index) DeriveRecent() error {
	return ix.write(func(w *writer) error {
		var have int64
		row, err := w.queryRow(`SELECT coalesce(max(batch), 0) FROM sample`)
		if err != nil {
			return err
		}
		if err := row.Scan(&have); err != nil || have > 0 {
			return err
		}
		// The counter moves first so the derived batch is never reused.
		row, err = w.queryRow(`
			INSERT INTO meta(k, v) VALUES('batch', 1)
			ON CONFLICT(k) DO UPDATE SET v = v + 1
			RETURNING v`)
		if err != nil {
			return err
		}
		var batch int64
		if err := row.Scan(&batch); err != nil {
			return err
		}
		return w.exec(`
			WITH times AS (
			  SELECT added, lag(added) OVER (ORDER BY added DESC) AS newer
			  FROM (SELECT DISTINCT added FROM sample WHERE added > 0)
			)
			UPDATE sample SET batch = ?
			WHERE added > coalesce((SELECT max(added) FROM times WHERE newer - added > ?), 0)`,
			batch, recentGap)
	})
}

func (w *writer) upsertPack(p Pack) error {
	if p.ID == "" {
		return errors.New("pack without id")
	}
	oldName, existed, err := w.packName(p.ID)
	if err != nil {
		return err
	}
	err = w.exec(`
		INSERT INTO pack(id, name, name_key, dir, source, added, file_mtime, file_size)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, name_key = excluded.name_key,
		  dir = excluded.dir, source = excluded.source, added = excluded.added,
		  file_mtime = excluded.file_mtime, file_size = excluded.file_size`,
		p.ID, p.Name, strings.ToLower(p.Name), p.Dir, p.Source, p.Added, p.FileMTime, p.FileSize)
	if err != nil {
		return fmt.Errorf("pack %s: %w", p.ID, err)
	}
	w.packNames[p.ID] = p.Name
	// Samples can be indexed before their pack row, so a brand-new row may
	// already have samples whose searchable text lacks the pack name.
	if existed && oldName == p.Name {
		return nil
	}
	if err := w.exec(`UPDATE sample SET pack_key = ? WHERE pack_id = ?`, strings.ToLower(p.Name), p.ID); err != nil {
		return err
	}
	return w.exec(`UPDATE sample_fts SET pack = ? WHERE rowid IN (SELECT rid FROM sample WHERE pack_id = ?)`, p.Name, p.ID)
}

// packName returns the display name of a pack, "" when it is not indexed.
func (w *writer) packName(id string) (name string, ok bool, err error) {
	if name, ok := w.packNames[id]; ok {
		return name, true, nil
	}
	row, err := w.queryRow(`SELECT name FROM pack WHERE id = ?`, id)
	if err != nil {
		return "", false, err
	}
	switch err := row.Scan(&name); {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	w.packNames[id] = name
	return name, true, nil
}

// packSamples maps sample id to rid for every sample of a pack.
func (w *writer) packSamples(packID string) (map[string]int64, error) {
	s, err := w.stmt(`SELECT id, rid FROM sample WHERE pack_id = ?`)
	if err != nil {
		return nil, err
	}
	rows, err := s.Query(packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var (
			id  string
			rid int64
		)
		if err := rows.Scan(&id, &rid); err != nil {
			return nil, err
		}
		out[id] = rid
	}
	return out, rows.Err()
}

func (w *writer) rid(id string) (rid int64, ok bool, err error) {
	row, err := w.queryRow(`SELECT rid FROM sample WHERE id = ?`, id)
	if err != nil {
		return 0, false, err
	}
	switch err := row.Scan(&rid); {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, err
	}
	return rid, true, nil
}

func (w *writer) upsertSample(s Sample, batch int64) error {
	if s.ID == "" {
		return errors.New("sample without id")
	}
	if s.PackID == "" {
		return fmt.Errorf("sample %s: no pack id", s.ID)
	}
	if err := w.upsertSampleRow(s, batch); err != nil {
		return fmt.Errorf("sample %s: %w", s.ID, err)
	}
	return nil
}

func (w *writer) upsertSampleRow(s Sample, batch int64) error {
	tags := normTags(s.Tags)
	joined := strings.Join(tags, tagSep)
	key := s.Key
	if k, ok := NormalizeKey(key); ok {
		key = k
	}
	packName, _, err := w.packName(s.PackID)
	if err != nil {
		return err
	}

	var (
		rid                                 int64
		oldName, oldRel, oldTags, oldPackID string
	)
	row, err := w.queryRow(`SELECT rid, name, relpath, tags, pack_id FROM sample WHERE id = ?`, s.ID)
	if err != nil {
		return err
	}
	switch err := row.Scan(&rid, &oldName, &oldRel, &oldTags, &oldPackID); {
	case errors.Is(err, sql.ErrNoRows):
		res, err := w.execResult(`
			INSERT INTO sample(id, pack_id, pack_key, name, ext, relpath, relpath_key, size, dur, sr, ch, type, bpm, bpm_src,
			  key, key_src, key_ord, key_root, tags, tags_key, fav, added, missing, analyzed, note, batch)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.ID, s.PackID, strings.ToLower(packName), s.Name, s.Ext, s.RelPath, strings.ToLower(s.RelPath), s.Size, s.Dur, s.SR, s.Ch, s.Type, s.BPM, s.BPMSrc,
			key, s.KeySrc, keyOrd(key), s.KeyRoot, joined, strings.ToLower(joined), s.Fav, s.Added, s.Missing, s.Analyzed, s.Note, batch)
		if err != nil {
			return err
		}
		if rid, err = res.LastInsertId(); err != nil {
			return err
		}
		if err := w.insertText(rid, s, packName, joined); err != nil {
			return err
		}
		if err := w.insertTags(rid, tags); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		err := w.exec(`
			UPDATE sample SET pack_id = ?, pack_key = ?, name = ?, ext = ?, relpath = ?, relpath_key = ?, size = ?, dur = ?, sr = ?, ch = ?,
			  type = ?, bpm = ?, bpm_src = ?, key = ?, key_src = ?, key_ord = ?, key_root = ?, tags = ?, tags_key = ?, fav = ?, added = ?,
			  analyzed = ?, note = ?, batch = CASE WHEN ? > 0 THEN ? ELSE batch END
			WHERE rid = ?`,
			s.PackID, strings.ToLower(packName), s.Name, s.Ext, s.RelPath, strings.ToLower(s.RelPath), s.Size, s.Dur, s.SR, s.Ch,
			s.Type, s.BPM, s.BPMSrc, key, s.KeySrc, keyOrd(key), s.KeyRoot, joined, strings.ToLower(joined), s.Fav, s.Added,
			s.Analyzed, s.Note, batch, batch, rid)
		if err != nil {
			return err
		}
		// Rewriting FTS and tag rows is most of the cost of an upsert,
		// and most upserts (analysis results, favourites) change neither.
		if oldName != s.Name || oldRel != s.RelPath || oldTags != joined || oldPackID != s.PackID {
			if err := w.exec(`DELETE FROM sample_fts WHERE rowid = ?`, rid); err != nil {
				return err
			}
			if err := w.insertText(rid, s, packName, joined); err != nil {
				return err
			}
		}
		if oldTags != joined {
			if err := w.exec(`DELETE FROM sample_tag WHERE rid = ?`, rid); err != nil {
				return err
			}
			if err := w.insertTags(rid, tags); err != nil {
				return err
			}
		}
	}

	if s.Mini == "" {
		return w.exec(`DELETE FROM sample_mini WHERE rid = ?`, rid)
	}
	return w.exec(`
		INSERT INTO sample_mini(rid, mini) VALUES(?, ?)
		ON CONFLICT(rid) DO UPDATE SET mini = excluded.mini WHERE mini <> excluded.mini`, rid, s.Mini)
}

func (w *writer) insertText(rid int64, s Sample, packName, tags string) error {
	return w.exec(`INSERT INTO sample_fts(rowid, name, relpath, pack, tags) VALUES(?, ?, ?, ?, ?)`,
		rid, s.Name, s.RelPath, packName, tags)
}

// insertTags writes the closure of tags (each tag and all its ancestors) for
// one sample and makes sure every one of those paths has a display name. A
// tag as applied decides the spelling; a path only implied as an ancestor
// keeps whatever spelling it already has.
func (w *writer) insertTags(rid int64, tags []string) error {
	linked := map[string]bool{}
	for _, tag := range tags {
		for end := 0; end < len(tag); {
			if i := strings.IndexByte(tag[end:], '/'); i >= 0 {
				end += i
			} else {
				end = len(tag)
			}
			name := tag[:end]
			applied := end == len(tag)
			end++ // past the slash
			key := strings.ToLower(name)
			if !linked[key] {
				linked[key] = true
				if err := w.exec(`INSERT INTO sample_tag(key, rid) VALUES(?, ?)`, key, rid); err != nil {
					return err
				}
			}
			if err := w.nameTag(key, name, applied); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *writer) nameTag(key, name string, applied bool) error {
	known, seen := w.tagNames[key]
	if seen && (!applied || known == name) {
		return nil
	}
	leaf := key[strings.LastIndexByte(key, '/')+1:]
	if !applied {
		// "" marks a path this transaction knows exists without knowing
		// how it is spelled.
		w.tagNames[key] = ""
		return w.exec(`INSERT OR IGNORE INTO tag(key, name, leaf) VALUES(?, ?, ?)`, key, name, leaf)
	}
	w.tagNames[key] = name
	return w.exec(`
		INSERT INTO tag(key, name, leaf) VALUES(?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET name = excluded.name WHERE name <> excluded.name`,
		key, name, leaf)
}

func (w *writer) deleteSample(rid int64) error {
	for _, q := range []string{
		`DELETE FROM sample WHERE rid = ?`,
		`DELETE FROM sample_fts WHERE rowid = ?`,
		`DELETE FROM sample_tag WHERE rid = ?`,
		`DELETE FROM sample_mini WHERE rid = ?`,
	} {
		if err := w.exec(q, rid); err != nil {
			return err
		}
	}
	return nil
}

// normTags canonicalises a sample's tags and drops duplicates, comparing
// case-insensitively like everything else about tags.
func normTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, t := range tags {
		t = normTag(strings.ReplaceAll(t, tagSep, " "))
		key := strings.ToLower(t)
		if t == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	return out
}
