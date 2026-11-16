package splice

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"ssss/internal/analysis"
	"ssss/internal/model"
)

// Meta is what Splice's database says about one downloaded file.
type Meta struct {
	PackName string
	Genre    string
	Tags     []string
	BPM      float64 // 0 = not stated
	Key      string  // library notation, "" = not stated
	Type     string  // model.TypeLoop, model.TypeOneShot or ""
}

// DB is the metadata read from a sounds.db. The schema is undocumented and
// has changed before, so everything here is optional: a missing table or
// column only means less metadata.
type DB struct {
	byPath map[string]Meta
	// byName holds files whose name is unique in the database, for when the
	// sample folder has moved since Splice recorded the paths.
	byName map[string]Meta
}

// pathKey makes paths comparable whatever OS wrote them.
func pathKey(p string) string {
	return strings.ToLower(strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/"))
}

// Lookup returns the metadata for a file in the Splice folder.
func (d *DB) Lookup(absPath string) (Meta, bool) {
	if d == nil {
		return Meta{}, false
	}
	if m, ok := d.byPath[pathKey(absPath)]; ok {
		return m, true
	}
	m, ok := d.byName[strings.ToLower(filepath.Base(absPath))]
	return m, ok
}

// Len is the number of files the database describes.
func (d *DB) Len() int {
	if d == nil {
		return 0
	}
	return len(d.byPath)
}

// ReadDB reads a sounds.db without touching it: Splice may have it open, so
// the file (and its write-ahead log, which holds the newest rows) is copied
// to a temp folder and the copy is queried.
func ReadDB(path string) (*DB, error) {
	tmp, err := os.MkdirTemp("", "ssss-splice-*")
	if err != nil {
		return nil, fmt.Errorf("splice: %w", err)
	}
	defer os.RemoveAll(tmp)
	work := filepath.Join(tmp, "sounds.db")
	if err := copyFile(path, work); err != nil {
		return nil, fmt.Errorf("splice: copy database: %w", err)
	}
	if err := copyFile(path+"-wal", work+"-wal"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("splice: copy database log: %w", err)
	}

	db, err := sql.Open("sqlite", work+"?_pragma=query_only(1)")
	if err != nil {
		return nil, fmt.Errorf("splice: open database: %w", err)
	}
	defer db.Close()

	packs, err := readPacks(db)
	if err != nil {
		return nil, fmt.Errorf("splice: read packs: %w", err)
	}
	out := &DB{byPath: map[string]Meta{}, byName: map[string]Meta{}}
	if err := out.readSamples(db, packs); err != nil {
		return nil, fmt.Errorf("splice: read samples: %w", err)
	}
	return out, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// columns returns the lower-cased column names of a table; empty when the
// table does not exist.
func columns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols[strings.ToLower(name)] = true
	}
	return cols, rows.Err()
}

// textColumn selects a column as text, or an empty string when the table
// lacks it, so one query shape serves every schema version.
func textColumn(cols map[string]bool, name string) string {
	if cols[name] {
		return `coalesce(CAST("` + name + `" AS TEXT), '')`
	}
	return `''`
}

type packRow struct{ name, genre string }

func readPacks(db *sql.DB) (map[string]packRow, error) {
	cols, err := columns(db, "packs")
	if err != nil {
		return nil, err
	}
	packs := map[string]packRow{}
	if !cols["uuid"] {
		return packs, nil
	}
	rows, err := db.Query(`SELECT ` + textColumn(cols, "uuid") + `, ` + textColumn(cols, "name") + `, ` +
		textColumn(cols, "genre") + ` FROM packs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var uuid string
		var p packRow
		if err := rows.Scan(&uuid, &p.name, &p.genre); err != nil {
			return nil, err
		}
		packs[uuid] = p
	}
	return packs, rows.Err()
}

func (d *DB) readSamples(db *sql.DB, packs map[string]packRow) error {
	cols, err := columns(db, "samples")
	if err != nil {
		return err
	}
	if !cols["local_path"] {
		return nil // nothing to match files with
	}
	names := []string{"local_path", "audio_key", "chord_type", "bpm", "sample_type", "tags", "pack_uuid"}
	exprs := make([]string, len(names))
	for i, name := range names {
		exprs[i] = textColumn(cols, name)
	}
	rows, err := db.Query(`SELECT ` + strings.Join(exprs, ", ") +
		` FROM samples WHERE local_path IS NOT NULL AND local_path <> ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	ambiguous := map[string]bool{}
	for rows.Next() {
		var localPath, key, chord, bpm, typ, tags, packUUID string
		if err := rows.Scan(&localPath, &key, &chord, &bpm, &typ, &tags, &packUUID); err != nil {
			return err
		}
		pack := packs[packUUID]
		m := Meta{
			PackName: pack.name,
			Genre:    pack.genre,
			Tags:     splitTags(tags),
			BPM:      parseBPM(bpm),
			Key:      parseKey(key, chord),
			Type:     parseType(typ),
		}
		d.byPath[pathKey(localPath)] = m
		name := pathKey(localPath)
		name = name[strings.LastIndexByte(name, '/')+1:]
		if _, dup := d.byName[name]; dup || ambiguous[name] {
			ambiguous[name] = true
			delete(d.byName, name)
			continue
		}
		d.byName[name] = m
	}
	return rows.Err()
}

// splitTags splits Splice's delimited tag string. The delimiter is not
// documented; commas, semicolons and pipes have all been reported.
func splitTags(s string) []string {
	var tags []string
	for _, t := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '|' }) {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

func parseBPM(s string) float64 {
	bpm, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || bpm < 20 || bpm > 400 {
		return 0
	}
	return bpm
}

// parseKey joins Splice's root ("c#") and chord type ("minor") into library
// notation.
func parseKey(root, chord string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	key, ok := analysis.NormalizeKey(strings.TrimSpace(root + " " + chord))
	if !ok {
		key, ok = analysis.NormalizeKey(root)
	}
	if !ok {
		return ""
	}
	return key
}

func parseType(s string) string {
	letters := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
	switch letters {
	case "loop":
		return model.TypeLoop
	case "oneshot":
		return model.TypeOneShot
	}
	return ""
}
