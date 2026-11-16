package index

import (
	"path/filepath"
	"slices"
	"testing"

	"ssss/internal/model"
)

func openTemp(t testing.TB) *Index {
	t.Helper()
	ix, err := Open(filepath.Join(t.TempDir(), "cache", "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix
}

var (
	packA = Pack{ID: "pk_A", Name: "KSHMR Vol 4", Dir: "Splice/KSHMR Vol 4", Source: "splice", Added: 1}
	packB = Pack{ID: "pk_B", Name: "Unsorted", Dir: "Unsorted", Source: "import", Added: 2}
)

func sample(id, relpath, typ string, dur, bpm float64, key string, added int64, tags ...string) Sample {
	base := filepath.Base(relpath)
	ext := filepath.Ext(base)
	return Sample{Sample: model.Sample{
		ID: id, Name: base[:len(base)-len(ext)], Ext: ext[1:], RelPath: relpath,
		Size: 1000, Dur: dur, SR: 44100, Ch: 2, Type: typ, BPM: bpm, Key: key,
		Tags: tags, Added: added, Analyzed: true, Mini: "bWluaQ==",
	}}
}

// The fixture uses readable ids; the index does not care what an id looks
// like.
func fixtureA() []Sample { return inPack(packA, bareFixtureA()) }
func fixtureB() []Sample { return inPack(packB, bareFixtureB()) }

func inPack(p Pack, samples []Sample) []Sample {
	for i := range samples {
		samples[i].PackID = p.ID
	}
	return samples
}

func bareFixtureA() []Sample {
	kick := sample("kick", "Drums/Kicks/Kick_Deep_01.wav", "oneshot", 0.4, 0, "", 1000, "drums/kick")
	kick.Fav = true
	return []Sample{
		kick,
		sample("snare", "Drums/Snares/Snare_Tight.wav", "oneshot", 0.3, 0, "", 2000, "drums/snare", "drums/kick"),
		sample("bass", "Bass/Bass_Loop_Am_124.wav", "loop", 7.7, 124, "Am", 3000, "bass"),
		sample("dm", "Keys/Piano_Dm_120.wav", "loop", 8, 120.4, "Dm", 8000, "keys/piano"),
		sample("fsharp", "Keys/Rhodes_F#_100.wav", "loop", 4, 100, "Gb", 9000, "keys"),
	}
}

func bareFixtureB() []Sample {
	hat := sample("hat", "hat 808.mp3", "oneshot", 0.1, 0, "", 6000, "drums/hat/808")
	hat.Analyzed, hat.Mini, hat.Missing = false, "", true
	return []Sample{
		sample("pad", "Pad_C_62.flac", "loop", 15.5, 62, "C", 4000),
		sample("lead", "Lead_Em_128.aif", "loop", 3.75, 128, "E minor", 5000, "synth/lead", "Kick"),
		hat,
		sample("vox", "Vox_Gm_248.ogg", "loop", 2, 248, "Gm", 7000, "vocal"),
	}
}

func openFixture(t testing.TB) *Index {
	t.Helper()
	ix := openTemp(t)
	if err := ix.ReplacePack(packA, fixtureA()); err != nil {
		t.Fatalf("ReplacePack A: %v", err)
	}
	if err := ix.ReplacePack(packB, fixtureB()); err != nil {
		t.Fatalf("ReplacePack B: %v", err)
	}
	err := ix.ReplaceCollections([]Collection{
		{ID: "col_1", Name: "Techno Set", Items: []string{"bass", "kick", "gone", "bass"}},
		{ID: "col_2", Name: "Empty"},
	})
	if err != nil {
		t.Fatalf("ReplaceCollections: %v", err)
	}
	return ix
}

// matches returns the ids matching query, sorted, so tests can ignore order.
func matches(t testing.TB, ix *Index, query string) []string {
	t.Helper()
	ids, err := ix.SearchIDs(query, "", false)
	if err != nil {
		t.Fatalf("SearchIDs(%q): %v", query, err)
	}
	slices.Sort(ids)
	return ids
}

func sorted(ids ...string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	if out == nil {
		out = []string{}
	}
	return out
}

var allIDs = []string{"kick", "snare", "bass", "dm", "fsharp", "pad", "lead", "hat", "vox"}

func allBut(drop ...string) []string {
	var out []string
	for _, id := range allIDs {
		if !slices.Contains(drop, id) {
			out = append(out, id)
		}
	}
	return sorted(out...)
}
