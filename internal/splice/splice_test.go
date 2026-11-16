package splice

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"ssss/internal/model"
)

// writeTree lays out a Splice folder the way the app does and returns its
// root. Contents do not matter here; only the tree is read.
func writeTree(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Splice")
	files := []string{
		"sounds/packs/KSHMR Vol 4/Drums/Kicks/KSHMR_Kick_01.wav",
		"sounds/packs/KSHMR Vol 4/Loops/KSHMR_Bass_Loop_128_Am.wav",
		"sounds/packs/KSHMR Vol 4/Loops/unknown_to_db.WAV",
		"sounds/packs/KSHMR Vol 4/.DS_Store",
		"sounds/packs/KSHMR Vol 4/artwork.jpg",
		"sounds/packs/Lo-Fi Keys/Keys/LK_Rhodes_Cmaj.flac",
		"sounds/packs/Presets Only/serum/lead.fxp",
		"sounds/packs/.hidden/secret.wav",
		"presets/serum/pad.fxp",
		"midi/chords.mid",
	}
	for _, rel := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// writeDB creates a sounds.db with the given schema and rows.
func writeDB(t *testing.T, statements ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sounds.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return path
}

const fullSchema = `
CREATE TABLE packs(uuid TEXT PRIMARY KEY, name TEXT, genre TEXT);
CREATE TABLE samples(id INTEGER PRIMARY KEY, local_path TEXT, filename TEXT, file_hash TEXT,
  audio_key TEXT, chord_type TEXT, bpm INTEGER, duration INTEGER, sample_type TEXT, tags TEXT,
  provider_name TEXT, pack_uuid TEXT, popularity INTEGER);`

func TestScanTreeOnly(t *testing.T) {
	root := writeTree(t)
	tree, err := Scan(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tree.HasDB || tree.Folder != root {
		t.Errorf("tree = %+v", tree)
	}
	if len(tree.Packs) != 2 {
		t.Fatalf("packs = %+v, want the two with audio", tree.Packs)
	}
	kshmr, keys := tree.Packs[0], tree.Packs[1]
	if kshmr.Name != "KSHMR Vol 4" || len(kshmr.Files) != 3 || keys.Name != "Lo-Fi Keys" || len(keys.Files) != 1 {
		t.Errorf("packs = %+v", tree.Packs)
	}
	if got := kshmr.Files[0]; got.Rel != "Drums/Kicks/KSHMR_Kick_01.wav" || !filepath.IsAbs(got.Path) || !reflect.DeepEqual(got.Meta, Meta{}) {
		t.Errorf("first file = %+v", got)
	}

	// Pointing at the packs folder itself, or at any folder of folders,
	// works the same.
	for _, folder := range []string{filepath.Join(root, "sounds"), filepath.Join(root, "sounds", "packs")} {
		tree, err := Scan(folder, nil)
		if err != nil || len(tree.Packs) != 2 {
			t.Errorf("Scan(%s) = %+v, %v", folder, tree, err)
		}
	}
	if _, err := Scan(filepath.Join(root, "nope"), nil); err == nil {
		t.Error("Scan of a missing folder succeeded")
	}
}

func TestReadDBAndScan(t *testing.T) {
	root := writeTree(t)
	packs := filepath.Join(root, "sounds", "packs")
	kick := filepath.Join(packs, "KSHMR Vol 4", "Drums", "Kicks", "KSHMR_Kick_01.wav")
	// Recorded under a folder that has since moved: matched by file name.
	movedLoop := `C:\Users\someone\Documents\Splice\sounds\packs\KSHMR Vol 4\Loops\KSHMR_Bass_Loop_128_Am.wav`
	rhodes := filepath.Join(packs, "Lo-Fi Keys", "Keys", "LK_Rhodes_Cmaj.flac")

	dbPath := writeDB(t, fullSchema,
		`INSERT INTO packs VALUES ('p1', 'KSHMR Vol. 4', 'EDM'), ('p2', 'Lo-Fi Keys', 'Lo-Fi Hip Hop')`,
		`INSERT INTO samples(local_path, filename, audio_key, chord_type, bpm, duration, sample_type, tags, pack_uuid) VALUES
		 ('`+kick+`', 'KSHMR_Kick_01.wav', NULL, NULL, NULL, 412, 'oneshot', 'drums,kicks', 'p1'),
		 ('`+movedLoop+`', 'KSHMR_Bass_Loop_128_Am.wav', 'a', 'minor', 128, 7500, 'loop', 'bass, synth ,', 'p1'),
		 ('`+rhodes+`', 'LK_Rhodes_Cmaj.flac', 'c#', 'major', 0, 3000, 'one_shot', 'keys|rhodes', 'p2'),
		 (NULL, 'not_downloaded.wav', 'e', 'minor', 90, 1000, 'loop', 'x', 'p1')`,
	)
	db, err := ReadDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if db.Len() != 3 {
		t.Errorf("database describes %d files, want 3 downloaded ones", db.Len())
	}

	tree, err := Scan(root, db)
	if err != nil {
		t.Fatal(err)
	}
	if !tree.HasDB || len(tree.Packs) != 2 {
		t.Fatalf("tree = %+v", tree)
	}
	kshmr, keys := tree.Packs[0], tree.Packs[1]
	if kshmr.Name != "KSHMR Vol 4" || kshmr.Genre != "EDM" || keys.Genre != "Lo-Fi Hip Hop" {
		t.Errorf("pack names and genres: %q %q, %q %q", kshmr.Name, kshmr.Genre, keys.Name, keys.Genre)
	}
	want := map[string]Meta{
		"Drums/Kicks/KSHMR_Kick_01.wav": {
			PackName: "KSHMR Vol. 4", Genre: "EDM", Tags: []string{"drums", "kicks"}, Type: model.TypeOneShot,
		},
		"Loops/KSHMR_Bass_Loop_128_Am.wav": {
			PackName: "KSHMR Vol. 4", Genre: "EDM", Tags: []string{"bass", "synth"}, BPM: 128, Key: "Am", Type: model.TypeLoop,
		},
		"Loops/unknown_to_db.WAV": {},
	}
	for _, f := range kshmr.Files {
		if !reflect.DeepEqual(f.Meta, want[f.Rel]) {
			t.Errorf("%s: meta = %+v, want %+v", f.Rel, f.Meta, want[f.Rel])
		}
	}
	if got := keys.Files[0].Meta; got.Key != "C#" || got.BPM != 0 || got.Type != model.TypeOneShot || !reflect.DeepEqual(got.Tags, []string{"keys", "rhodes"}) {
		t.Errorf("rhodes meta = %+v", got)
	}

	// The original is untouched and no copy is left behind.
	if _, err := os.Stat(dbPath + "-wal"); err == nil {
		t.Error("reading created a write-ahead log next to the database")
	}
}

func TestReadDBDefensive(t *testing.T) {
	file := "/music/Splice/sounds/packs/P/a.wav"
	tests := []struct {
		name       string
		statements []string
		wantLen    int
		want       Meta
	}{
		{"no tables", nil, 0, Meta{}},
		{"samples without paths", []string{
			`CREATE TABLE samples(id INTEGER, filename TEXT, bpm INTEGER)`,
			`INSERT INTO samples VALUES (1, 'a.wav', 120)`,
		}, 0, Meta{}},
		{"no packs table, few columns", []string{
			`CREATE TABLE samples(local_path TEXT, bpm REAL, pack_uuid TEXT)`,
			`INSERT INTO samples VALUES ('` + file + `', 127.5, 'p1')`,
		}, 1, Meta{BPM: 127.5}},
		{"packs without genre, odd values", []string{
			`CREATE TABLE packs(uuid TEXT, name TEXT)`,
			`CREATE TABLE samples(local_path TEXT, audio_key TEXT, chord_type TEXT, bpm TEXT, sample_type TEXT, tags TEXT, pack_uuid TEXT, extra BLOB)`,
			`INSERT INTO packs VALUES ('p1', 'P')`,
			`INSERT INTO samples VALUES ('` + file + `', 'H', 'weird', 'fast', 'texture', NULL, 'p1', x'00ff')`,
		}, 1, Meta{PackName: "P"}},
		{"flat key without chord type", []string{
			`CREATE TABLE samples(local_path TEXT, audio_key TEXT)`,
			`INSERT INTO samples VALUES ('` + file + `', 'Bb')`,
		}, 1, Meta{Key: "A#"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := ReadDB(writeDB(t, append([]string{`CREATE TABLE unrelated(x)`}, tt.statements...)...))
			if err != nil {
				t.Fatal(err)
			}
			if db.Len() != tt.wantLen {
				t.Fatalf("Len = %d, want %d", db.Len(), tt.wantLen)
			}
			got, ok := db.Lookup(file)
			if ok != (tt.wantLen > 0) || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Lookup = %+v, %v; want %+v", got, ok, tt.want)
			}
		})
	}

	garbage := filepath.Join(t.TempDir(), "sounds.db")
	os.WriteFile(garbage, []byte("this is not a database, not even slightly, but long enough to be read"), 0o644)
	if _, err := ReadDB(garbage); err == nil {
		t.Error("ReadDB accepted a file that is not a database")
	}
	if _, err := ReadDB(filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Error("ReadDB accepted a missing file")
	}
}

func TestLookupAmbiguousName(t *testing.T) {
	dbPath := writeDB(t, fullSchema,
		`INSERT INTO samples(local_path, bpm) VALUES ('/old/packs/A/loop.wav', 100), ('/old/packs/B/loop.wav', 140), ('/old/packs/B/only.wav', 90)`)
	db, err := ReadDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := db.Lookup("/new/packs/A/loop.wav"); ok {
		t.Error("a file name shared by two rows was matched by name")
	}
	if m, ok := db.Lookup("/new/packs/B/ONLY.wav"); !ok || m.BPM != 90 {
		t.Errorf("unique name lookup = %+v, %v", m, ok)
	}
	if m, ok := db.Lookup(`\old\packs\B\loop.wav`); !ok || m.BPM != 140 {
		t.Errorf("exact path lookup = %+v, %v", m, ok)
	}
}

func TestOpenFindsDBInFolder(t *testing.T) {
	root := writeTree(t)
	src := writeDB(t, fullSchema,
		`INSERT INTO samples(local_path, bpm, sample_type) VALUES ('/elsewhere/KSHMR_Kick_01.wav', 0, 'oneshot')`)
	data, _ := os.ReadFile(src)
	if err := os.WriteFile(filepath.Join(root, "sounds.db"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if !tree.HasDB || tree.Packs[0].Files[0].Meta.Type != model.TypeOneShot {
		t.Errorf("tree = %+v", tree)
	}
}

func TestLocate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if dir, ok := Locate(""); ok {
		t.Errorf("Locate found %q in an empty home", dir)
	}
	want := filepath.Join(home, "Splice")
	os.MkdirAll(want, 0o755)
	if dir, ok := Locate(""); !ok || dir != want {
		t.Errorf("Locate = %q, %v; want %q", dir, ok, want)
	}
	override := t.TempDir()
	if dir, ok := Locate(override); !ok || dir != override {
		t.Errorf("Locate with override = %q, %v", dir, ok)
	}
	// A configured folder that is gone falls back to the default places.
	if dir, ok := Locate(filepath.Join(override, "gone")); !ok || dir != want {
		t.Errorf("Locate with a stale override = %q, %v", dir, ok)
	}
}
