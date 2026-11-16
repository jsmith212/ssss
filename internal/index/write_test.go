package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"ssss/internal/model"
)

func TestOpenCreatesAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "index.db")
	ix, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ix.Fresh() {
		t.Error("new index is not Fresh")
	}
	var mode string
	if err := ix.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q, %v; want wal", mode, err)
	}
	if err := ix.ReplacePack(packA, fixtureA()); err != nil {
		t.Fatal(err)
	}
	if err := ix.Close(); err != nil {
		t.Fatal(err)
	}

	ix, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if ix.Fresh() {
		t.Error("reopened index is Fresh")
	}
	if n, _ := ix.Count(); n != len(fixtureA()) {
		t.Errorf("Count after reopen = %d, want %d", n, len(fixtureA()))
	}
}

func TestOpenRecreatesOnVersionMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	ix, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.ReplacePack(packA, fixtureA()); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.db.Exec(`PRAGMA user_version = 9999`); err != nil {
		t.Fatal(err)
	}
	if err := ix.Close(); err != nil {
		t.Fatal(err)
	}

	ix, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if !ix.Fresh() {
		t.Error("index with another schema version was kept")
	}
	if n, _ := ix.Count(); n != 0 {
		t.Errorf("Count = %d, want 0 after recreate", n)
	}
	var v int
	if err := ix.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Errorf("user_version = %d, %v; want %d", v, err, schemaVersion)
	}
}

func TestOpenRecreatesForeignAndCorruptFiles(t *testing.T) {
	t.Run("unversioned database", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "index.db")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE sample(x)`); err != nil {
			t.Fatal(err)
		}
		db.Close()
		ix, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer ix.Close()
		if !ix.Fresh() {
			t.Error("foreign database was kept")
		}
		if err := ix.ReplacePack(packA, fixtureA()); err != nil {
			t.Errorf("recreated index is not usable: %v", err)
		}
	})
	t.Run("not a database", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "index.db")
		if err := os.WriteFile(path, []byte("this is definitely not an sqlite file, just some bytes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ix, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer ix.Close()
		if !ix.Fresh() {
			t.Error("garbage file was kept")
		}
	})
}

func TestReplacePackDropsStaleAndMovesSamples(t *testing.T) {
	ix := openFixture(t)
	// Pack A loses everything but bass and gains "pad", which was in B.
	pad := fixtureB()[0]
	if err := ix.ReplacePack(packA, []Sample{fixtureA()[2], pad}); err != nil {
		t.Fatal(err)
	}
	if got, want := matches(t, ix, "pack:pk_A"), sorted("bass", "pad"); !reflect.DeepEqual(got, want) {
		t.Errorf("pack A = %v, want %v", got, want)
	}
	if got, want := matches(t, ix, ""), sorted("bass", "pad", "lead", "hat", "vox"); !reflect.DeepEqual(got, want) {
		t.Errorf("all = %v, want %v", got, want)
	}
	// Derived rows of dropped samples are gone, the moved one is findable
	// under its new pack name.
	for query, want := range map[string][]string{
		"kick":       {"lead"},
		"tag:drums":  {"hat"},
		"kshmr":      {"bass", "pad"},
		"pad":        {"pad"},
		"@unsorted":  {"lead", "hat", "vox"},
		"in:col_1":   {"bass"},
		"is:missing": {"hat"},
	} {
		if got := matches(t, ix, query); !reflect.DeepEqual(got, sorted(want...)) {
			t.Errorf("%q = %v, want %v", query, got, want)
		}
	}
	for _, table := range []string{"sample_fts", "sample_mini"} {
		var n int
		if err := ix.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		// hat has no mini peaks.
		if want := map[string]int{"sample_fts": 5, "sample_mini": 4}[table]; n != want {
			t.Errorf("%s has %d rows, want %d", table, n, want)
		}
	}
}

func TestUpsertUpdatesDerivedRows(t *testing.T) {
	ix := openFixture(t)
	kick := fixtureA()[0]
	kick.Name, kick.RelPath = "Thump", "Drums/Thump.wav"
	kick.Tags = []string{"Perc/Low", "perc/low", " perc / low ", ""}
	kick.BPM, kick.Key, kick.Fav, kick.Mini, kick.Note = 90, "Bb minor", false, "", "nice"
	if err := ix.UpsertSamples([]Sample{kick}, 0); err != nil {
		t.Fatal(err)
	}
	for query, want := range map[string][]string{
		"deep":       nil,
		"thump":      {"kick"},
		"tag:drums":  {"snare", "hat"},
		"#kick":      {"snare", "lead"},
		"#low":       {"kick"},
		"tag:perc":   {"kick"},
		"bpm:90":     {"kick"},
		"key:A#m":    {"kick"},
		"is:fav":     nil,
		"pack:pk_A":  {"kick", "snare", "bass", "dm", "fsharp"},
		"kshmr thum": {"kick"},
	} {
		if got := matches(t, ix, query); !reflect.DeepEqual(got, sorted(want...)) {
			t.Errorf("%q = %v, want %v", query, got, want)
		}
	}
	got, err := ix.Samples([]string{"kick"})
	if err != nil || len(got) != 1 {
		t.Fatalf("Samples: %v, %v", got, err)
	}
	if s := got[0]; !reflect.DeepEqual(s.Tags, []string{"Perc/Low"}) || s.Mini != "" || s.Note != "nice" || s.Key != "A#m" {
		t.Errorf("kick = %+v", s)
	}
}

func TestUpsertKeepsBatchAndMissing(t *testing.T) {
	ix := openTemp(t)
	if err := ix.UpsertPack(packB); err != nil {
		t.Fatal(err)
	}
	b1, err := ix.NewBatch()
	if err != nil {
		t.Fatal(err)
	}
	b2, err := ix.NewBatch()
	if err != nil {
		t.Fatal(err)
	}
	if b1 <= 0 || b2 <= b1 {
		t.Fatalf("batches %d, %d are not increasing", b1, b2)
	}
	samples := fixtureB() // hat arrives flagged missing
	if err := ix.UpsertSamples(samples, b1); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, ix, "is:recent"); len(got) != 4 {
		t.Errorf("recent = %v, want all four", got)
	}

	// A resync from the pack file carries no batch and no missing state.
	for i := range samples {
		samples[i].Missing = false
	}
	if err := ix.ReplacePack(packB, samples); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, ix, "is:recent"); len(got) != 4 {
		t.Errorf("recent after resync = %v, want all four", got)
	}
	if got := matches(t, ix, "is:missing"); !reflect.DeepEqual(got, []string{"hat"}) {
		t.Errorf("missing after resync = %v, want [hat]", got)
	}

	if err := ix.SetMissing([]string{"hat", "nope"}, false); err != nil {
		t.Fatal(err)
	}
	if err := ix.SetMissing([]string{"pad", "vox"}, true); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, ix, "is:missing"); !reflect.DeepEqual(got, sorted("pad", "vox")) {
		t.Errorf("missing = %v, want [pad vox]", got)
	}

	// A later import takes over "recent"; deleting it hands it back.
	if err := ix.UpsertSamples(fixtureA()[:1], b2); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, ix, "is:recent"); !reflect.DeepEqual(got, []string{"kick"}) {
		t.Errorf("recent = %v, want [kick]", got)
	}
	if err := ix.DeleteSamples([]string{"kick", "nope"}); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, ix, "is:recent"); len(got) != 4 {
		t.Errorf("recent after delete = %v, want the earlier four", got)
	}
}

func TestSamplesBeforePackAndPackRename(t *testing.T) {
	ix := openTemp(t)
	s := fixtureA()[2]
	if err := ix.UpsertSamples([]Sample{s}, 0); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, ix, "kshmr"); len(got) != 0 {
		t.Errorf("pack name matched before the pack exists: %v", got)
	}
	if got := matches(t, ix, "bass"); len(got) != 1 {
		t.Errorf("sample without pack row is not searchable: %v", got)
	}
	if err := ix.UpsertPack(packA); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, ix, "kshmr"); len(got) != 1 {
		t.Errorf("pack name not searchable after the pack arrived: %v", got)
	}

	renamed := packA
	renamed.Name = "Renamed Pack"
	renamed.FileMTime, renamed.FileSize = 111, 222
	if err := ix.UpsertPack(renamed); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, ix, "kshmr"); len(got) != 0 {
		t.Errorf("old pack name still matches: %v", got)
	}
	if got := matches(t, ix, `renamed @"renamed pack"`); len(got) != 1 {
		t.Errorf("new pack name does not match: %v", got)
	}
	packs, err := ix.Packs()
	if err != nil {
		t.Fatal(err)
	}
	if want := []Pack{renamed}; !reflect.DeepEqual(packs, want) {
		t.Errorf("Packs = %+v, want %+v", packs, want)
	}

	// Sorting by pack follows the rename: "Renamed Pack" < "Unsorted" < "Zebra".
	if err := ix.ReplacePack(packB, fixtureB()[:1]); err != nil {
		t.Fatal(err)
	}
	order := func() []string {
		ids, err := ix.SearchIDs("", "pack", false)
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	if got, want := order(), []string{"bass", "pad"}; !reflect.DeepEqual(got, want) {
		t.Errorf("by pack = %v, want %v", got, want)
	}
	renamed.Name = "Zebra"
	if err := ix.UpsertPack(renamed); err != nil {
		t.Fatal(err)
	}
	if got, want := order(), []string{"pad", "bass"}; !reflect.DeepEqual(got, want) {
		t.Errorf("by pack after rename = %v, want %v", got, want)
	}
}

func TestDeletePackAndReset(t *testing.T) {
	ix := openFixture(t)
	if err := ix.DeletePack("pk_A"); err != nil {
		t.Fatal(err)
	}
	if got, want := matches(t, ix, ""), sorted("pad", "lead", "hat", "vox"); !reflect.DeepEqual(got, want) {
		t.Errorf("after DeletePack = %v, want %v", got, want)
	}
	if got := matches(t, ix, "kshmr"); len(got) != 0 {
		t.Errorf("deleted pack still searchable: %v", got)
	}
	sb, err := ix.Sidebar(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sb.Packs) != 1 || sb.Packs[0].ID != "pk_B" {
		t.Errorf("packs = %+v", sb.Packs)
	}

	if err := ix.Reset(); err != nil {
		t.Fatal(err)
	}
	sb, err = ix.Sidebar(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sb.All != 0 || len(sb.Tags) != 0 || len(sb.Packs) != 0 || len(sb.Collections) != 0 {
		t.Errorf("after Reset = %+v", sb)
	}
	if got := matches(t, ix, "kick"); len(got) != 0 {
		t.Errorf("text index survived Reset: %v", got)
	}
}

func TestCollections(t *testing.T) {
	ix := openFixture(t)
	if err := ix.UpsertCollection(Collection{ID: "col_2", Name: "Loops", Items: []string{"vox", "bass"}}); err != nil {
		t.Fatal(err)
	}
	if got, want := matches(t, ix, "in:loops"), sorted("vox", "bass"); !reflect.DeepEqual(got, want) {
		t.Errorf("in:loops = %v, want %v", got, want)
	}
	if got := matches(t, ix, "in:empty"); len(got) != 0 {
		t.Errorf("old collection name still matches: %v", got)
	}
	got, err := ix.SampleCollections([]string{"bass", "kick", "pad", "nope"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"bass": {"col_1", "col_2"}, "kick": {"col_1"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SampleCollections = %v, want %v", got, want)
	}

	// Shrinking a collection drops the old members.
	if err := ix.UpsertCollection(Collection{ID: "col_1", Name: "Techno Set", Items: []string{"kick"}}); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, ix, "in:col_1"); !reflect.DeepEqual(got, []string{"kick"}) {
		t.Errorf("in:col_1 = %v, want [kick]", got)
	}
	if err := ix.DeleteCollection("col_1"); err != nil {
		t.Fatal(err)
	}
	if got := matches(t, ix, "in:col_1"); len(got) != 0 {
		t.Errorf("deleted collection still matches: %v", got)
	}
	if err := ix.ReplaceCollections([]Collection{{ID: "col_9", Name: "Only", Items: []string{"pad"}}}); err != nil {
		t.Fatal(err)
	}
	sb, err := ix.Sidebar(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sb.Collections) != 1 || sb.Collections[0].ID != "col_9" || sb.Collections[0].Count != 1 {
		t.Errorf("collections = %+v", sb.Collections)
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	ix := openFixture(t)
	done := make(chan error, 5)
	for range 4 {
		go func() {
			for range 50 {
				if _, err := ix.Search(model.SearchRequest{Query: "kick -is:fav"}); err != nil {
					done <- err
					return
				}
				if _, err := ix.Sidebar(nil, nil); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
	}
	go func() {
		for i := range 50 {
			samples := fixtureA()
			samples[0].Fav = i%2 == 0
			samples[1].Tags = []string{"drums/snare", fmt.Sprintf("round/%d", i)}
			if err := ix.ReplacePack(packA, samples); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for range 5 {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	if n, err := ix.Count(); err != nil || n != len(allIDs) {
		t.Errorf("Count = %d, %v", n, err)
	}
}

func TestWriteRejectsMissingIDs(t *testing.T) {
	ix := openFixture(t)
	if err := ix.UpsertSamples([]Sample{fixtureB()[0], {}}, 0); err == nil {
		t.Error("sample without id was accepted")
	}
	if err := ix.UpsertSamples(bareFixtureB(), 0); err == nil {
		t.Error("sample without pack id was accepted")
	}
	if err := ix.UpsertPack(Pack{}); err == nil {
		t.Error("pack without id was accepted")
	}
	if err := ix.UpsertCollection(Collection{}); err == nil {
		t.Error("collection without id was accepted")
	}
	// A failed write leaves nothing behind.
	bad := fixtureA()
	bad[0].Name = "Changed"
	bad = append(bad, Sample{})
	if err := ix.ReplacePack(packA, bad); err == nil {
		t.Fatal("ReplacePack with a bad sample succeeded")
	}
	if got := matches(t, ix, "changed"); len(got) != 0 {
		t.Errorf("failed ReplacePack was not rolled back: %v", got)
	}
}
