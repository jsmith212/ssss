package index

import (
	"encoding/base64"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"ssss/internal/model"
)

const (
	bigSamples = 50_000
	bigPacks   = 250

	// The targets are 30 ms for a page and 80 ms for the sidebar; the
	// asserted bounds are several times that so a loaded CI machine does
	// not flake, while a query plan gone quadratic still fails.
	pageBound    = 150 * time.Millisecond
	sidebarBound = 400 * time.Millisecond
)

var big struct {
	once sync.Once
	dir  string
	ix   *Index
	err  error
	fill time.Duration
}

func TestMain(m *testing.M) {
	code := m.Run()
	if big.ix != nil {
		big.ix.Close()
	}
	if big.dir != "" {
		os.RemoveAll(big.dir)
	}
	os.Exit(code)
}

// bigIndex builds a 50k-sample index once per test binary.
func bigIndex(tb testing.TB) *Index {
	tb.Helper()
	big.once.Do(func() {
		big.dir, big.err = os.MkdirTemp("", "ssss-index-perf")
		if big.err != nil {
			return
		}
		big.ix, big.err = Open(filepath.Join(big.dir, "index.db"))
		if big.err != nil {
			return
		}
		start := time.Now()
		big.err = fillBig(big.ix)
		big.fill = time.Since(start)
	})
	if big.err != nil {
		tb.Fatalf("build 50k index: %v", big.err)
	}
	return big.ix
}

func fillBig(ix *Index) error {
	rng := rand.New(rand.NewPCG(1, 2))
	instruments := []string{"Kick", "Snare", "Hat", "Clap", "Bass", "Pad", "Lead", "Vox", "Perc", "FX", "Piano", "Guitar", "Tom", "Ride"}
	adjectives := []string{"Deep", "Tight", "Dirty", "Warm", "Bright", "Lofi", "Punchy", "Soft", "Wide", "Dark", "Analog", "Crisp"}
	tagsFor := map[string][]string{
		"Kick": {"drums/kick", "drums/kick/808"}, "Snare": {"drums/snare"}, "Hat": {"drums/hat/open", "drums/hat/closed"},
		"Clap": {"drums/clap"}, "Bass": {"bass", "bass/sub", "bass/reese"}, "Pad": {"synth/pad"}, "Lead": {"synth/lead"},
		"Vox": {"vocal", "vocal/chop"}, "Perc": {"drums/perc", "perc/shaker"}, "FX": {"fx/riser", "fx/impact"},
		"Piano": {"keys/piano"}, "Guitar": {"guitar"}, "Tom": {"drums/tom"}, "Ride": {"drums/cymbal/ride"},
	}
	genres := []string{"techno", "house", "dnb", "trap", "ambient", "garage", "disco", "lofi"}
	keys := []string{"", "", "C", "Am", "G", "Em", "D", "Bm", "F", "Dm", "A#", "Gm", "F#m", "C#m", "D#", "Cm"}
	exts := []string{"wav", "wav", "wav", "wav", "aif", "flac", "mp3", "ogg"}

	mini := make([]byte, model.MiniPeaks)
	n := 0
	for p := range bigPacks {
		pack := Pack{
			ID:     fmt.Sprintf("pk_%04d", p),
			Name:   fmt.Sprintf("%s %s Vol %d", adjectives[p%len(adjectives)], genres[p%len(genres)], p),
			Dir:    fmt.Sprintf("Splice/Pack %d", p),
			Source: "splice",
			Added:  int64(p),
		}
		samples := make([]Sample, 0, bigSamples/bigPacks)
		for range bigSamples / bigPacks {
			inst := instruments[rng.IntN(len(instruments))]
			loop := rng.IntN(3) == 0
			s := Sample{Sample: model.Sample{
				ID:       fmt.Sprintf("%032x", n),
				Name:     fmt.Sprintf("%s_%s_%05d", inst, adjectives[rng.IntN(len(adjectives))], n),
				Ext:      exts[rng.IntN(len(exts))],
				Size:     int64(50_000 + rng.IntN(5_000_000)),
				Dur:      0.05 + rng.Float64()*2,
				SR:       44100,
				Ch:       2,
				Type:     model.TypeOneShot,
				Key:      keys[rng.IntN(len(keys))],
				KeySrc:   model.SrcDetected,
				Fav:      rng.IntN(50) == 0,
				Added:    1_700_000_000_000 + int64(n)*1000,
				Analyzed: rng.IntN(100) != 0,
			}}
			if loop {
				s.Type = model.TypeLoop
				s.Dur = 1 + rng.Float64()*30
				s.BPM = float64(70 + rng.IntN(110))
				s.BPMSrc = model.SrcFilename
			}
			s.RelPath = fmt.Sprintf("%ss/%s.%s", inst, s.Name, s.Ext)
			if rng.IntN(10) != 0 {
				choices := tagsFor[inst]
				s.Tags = append(s.Tags, choices[rng.IntN(len(choices))])
				if rng.IntN(2) == 0 {
					s.Tags = append(s.Tags, "genre/"+genres[rng.IntN(len(genres))])
				}
			}
			if s.Analyzed {
				for i := range mini {
					mini[i] = byte(rng.IntN(256))
				}
				s.Mini = base64.StdEncoding.EncodeToString(mini)
			}
			samples = append(samples, s)
			n++
		}
		if err := ix.ReplacePack(pack, samples); err != nil {
			return err
		}
	}

	cols := make([]Collection, 20)
	for c := range cols {
		cols[c] = Collection{ID: fmt.Sprintf("col_%02d", c), Name: fmt.Sprintf("Set %d", c)}
		for range 200 {
			cols[c].Items = append(cols[c].Items, fmt.Sprintf("%032x", rng.IntN(bigSamples)))
		}
	}
	if err := ix.ReplaceCollections(cols); err != nil {
		return err
	}

	// The last import: one pack's worth of samples.
	batch, err := ix.NewBatch()
	if err != nil {
		return err
	}
	ids := make([]string, 200)
	for i := range ids {
		ids[i] = fmt.Sprintf("%032x", bigSamples-1-i)
	}
	recent, err := ix.Samples(ids)
	if err != nil {
		return err
	}
	return ix.UpsertSamples(recent, batch)
}

var bigSearches = []struct {
	name string
	req  model.SearchRequest
}{
	{"all, newest first", model.SearchRequest{}},
	{"all, last page", model.SearchRequest{Offset: bigSamples - 200}},
	{"all, by name", model.SearchRequest{Sort: "name"}},
	{"all, by name, last page", model.SearchRequest{Sort: "name", Offset: bigSamples - 200}},
	{"all, by bpm", model.SearchRequest{Sort: "bpm"}},
	{"all, by bpm desc, middle page", model.SearchRequest{Sort: "bpm", Desc: true, Offset: 25_000}},
	{"all, by key", model.SearchRequest{Sort: "key"}},
	{"all, by dur", model.SearchRequest{Sort: "dur", Desc: true}},
	{"all, by pack", model.SearchRequest{Sort: "pack"}},
	{"all, by pack, last page", model.SearchRequest{Sort: "pack", Offset: bigSamples - 200}},
	{"text, relevance", model.SearchRequest{Query: "kick"}},
	{"text, relevance, deep page", model.SearchRequest{Query: "kick", Offset: 3000}},
	{"text, newest first", model.SearchRequest{Query: "kick", Sort: "added", Desc: true}},
	{"two text terms", model.SearchRequest{Query: "kick deep"}},
	{"text hitting most rows", model.SearchRequest{Query: "vol"}},
	{"text at the ranking cap", model.SearchRequest{Query: "drums"}},
	{"selective text", model.SearchRequest{Query: "kick_deep_000"}},
	{"short text (LIKE)", model.SearchRequest{Query: "ki"}},
	{"negated text", model.SearchRequest{Query: "-kick"}},
	{"tag with descendants", model.SearchRequest{Query: "tag:drums"}},
	{"leaf tag", model.SearchRequest{Query: "#kick"}},
	{"negated tag", model.SearchRequest{Query: "-tag:drums"}},
	{"pack by name", model.SearchRequest{Query: `pack:"deep techno vol 0"`}},
	{"collection", model.SearchRequest{Query: `in:"set 3"`}},
	{"bpm range", model.SearchRequest{Query: "bpm:120-128"}},
	{"bpm approx by name", model.SearchRequest{Query: "bpm:~124", Sort: "name"}},
	{"compatible key loops", model.SearchRequest{Query: "key:compatible:Am type:loop"}},
	{"length and format", model.SearchRequest{Query: "len:<1s fmt:wav"}},
	{"favourites", model.SearchRequest{Query: "is:fav"}},
	{"untagged", model.SearchRequest{Query: "is:untagged"}},
	{"recent", model.SearchRequest{Query: "is:recent"}},
	{"everything at once", model.SearchRequest{Query: "kick #drums type:oneshot -is:fav len:<1s -snare", Sort: "name"}},
}

func median(runs int, fn func()) time.Duration {
	times := make([]time.Duration, runs)
	for i := range times {
		start := time.Now()
		fn()
		times[i] = time.Since(start)
	}
	slices.Sort(times)
	return times[runs/2]
}

// TestPerformance50k reports real timings (go test -v) and fails when they
// are far off the targets.
func TestPerformance50k(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a 50k-sample index")
	}
	if raceEnabled {
		// The pure-Go SQLite runs some fifty times slower when instrumented.
		t.Skip("timings are meaningless under the race detector")
	}
	ix := bigIndex(t)
	t.Logf("build: %d samples in %d packs: %v", bigSamples, bigPacks, big.fill.Round(time.Millisecond))
	if n, err := ix.Count(); err != nil || n != bigSamples {
		t.Fatalf("Count = %d, %v; want %d", n, err, bigSamples)
	}

	var worst time.Duration
	for _, s := range bigSearches {
		req := s.req
		req.Limit = 200
		var res model.SearchResult
		d := median(9, func() {
			var err error
			if res, err = ix.Search(req); err != nil {
				t.Fatalf("%s: %v", s.name, err)
			}
		})
		worst = max(worst, d)
		t.Logf("search %-32s %8.2f ms  total %5d rows %3d", s.name, ms(d), res.Total, len(res.Rows))
		if res.Error != "" {
			t.Errorf("%s: query error %q", s.name, res.Error)
		}
		if res.Total == 0 || len(res.Rows) == 0 {
			t.Errorf("%s: matched nothing, so it measures nothing", s.name)
		}
		if d > pageBound {
			t.Errorf("%s: median %v exceeds %v", s.name, d, pageBound)
		}
	}
	t.Logf("search worst median: %.2f ms (target 30 ms)", ms(worst))

	// More matches than relevanceMax are not ranked but listed newest first,
	// identically by Search and SearchIDs.
	res, err := ix.Search(model.SearchRequest{Query: "vol", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := ix.SearchIDs("vol", "relevance", false)
	if err != nil {
		t.Fatal(err)
	}
	newest := fmt.Sprintf("%032x", bigSamples-1)
	if res.Total <= relevanceMax || res.Rows[0].ID != newest || ids[0] != newest {
		t.Errorf("unranked search: total %d, first %s / %s, want %s", res.Total, res.Rows[0].ID, ids[0], newest)
	}

	var sb model.Sidebar
	d := median(9, func() {
		var err error
		if sb, err = ix.Sidebar(nil, nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("sidebar %8.2f ms (target 80 ms): all %d fav %d untagged %d recent %d, %d tags %d packs %d collections",
		ms(d), sb.All, sb.Favorites, sb.Untagged, sb.Recent, len(sb.Tags), len(sb.Packs), len(sb.Collections))
	if d > sidebarBound {
		t.Errorf("sidebar: median %v exceeds %v", d, sidebarBound)
	}
	if sb.All != bigSamples || sb.Recent != 200 || len(sb.Packs) != bigPacks || len(sb.Collections) != 20 {
		t.Errorf("sidebar totals are off: %+v", sb)
	}

	d = median(5, func() {
		ids, err := ix.SearchIDs("", "name", false)
		if err != nil || len(ids) != bigSamples {
			t.Fatalf("SearchIDs: %d ids, %v", len(ids), err)
		}
	})
	t.Logf("SearchIDs of all %d by name: %.2f ms", bigSamples, ms(d))

	// A single-sample edit (favourite, analysis result) must stay cheap.
	one, err := ix.Samples([]string{fmt.Sprintf("%032x", 123)})
	if err != nil || len(one) != 1 {
		t.Fatalf("Samples: %v, %v", one, err)
	}
	d = median(9, func() {
		one[0].Fav = !one[0].Fav
		if err := ix.UpsertSamples(one, 0); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("upsert of one sample: %.2f ms", ms(d))
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func BenchmarkSearch(b *testing.B) {
	ix := bigIndex(b)
	for _, s := range bigSearches {
		req := s.req
		req.Limit = 200
		b.Run(s.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := ix.Search(req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSidebar(b *testing.B) {
	ix := bigIndex(b)
	for b.Loop() {
		if _, err := ix.Sidebar(nil, nil); err != nil {
			b.Fatal(err)
		}
	}
}
