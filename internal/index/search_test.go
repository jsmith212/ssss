package index

import (
	"reflect"
	"slices"
	"testing"

	"ssss/internal/model"
)

func TestSearchTokens(t *testing.T) {
	ix := openFixture(t)
	batch, err := ix.NewBatch()
	if err != nil {
		t.Fatal(err)
	}
	// Stamp two samples as the latest import.
	recent, err := ix.Samples([]string{"vox", "dm"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.UpsertSamples(recent, batch); err != nil {
		t.Fatal(err)
	}

	packAIDs := []string{"kick", "snare", "bass", "dm", "fsharp"}
	packBIDs := []string{"pad", "lead", "hat", "vox"}
	tests := []struct {
		query string
		want  []string
	}{
		{"", allIDs},

		// Free text: name, path, pack name, tags; trigram and short LIKE.
		{"kick", []string{"kick", "snare", "lead"}},
		{"KICK", []string{"kick", "snare", "lead"}},
		{"deep", []string{"kick"}},
		{"kicks/kick", []string{"kick"}},
		{"kshmr", packAIDs},
		{"kshmr piano", []string{"dm"}},
		{`"hat 808"`, []string{"hat"}},
		{`"808 hat"`, nil},
		{"ba", []string{"bass"}},
		{"un", packBIDs}, // only the pack name "Unsorted" contains it
		{"f#", []string{"fsharp"}},
		{"-kick", allBut("kick", "snare", "lead")},
		{"-ba", allBut("bass")},
		{"loop -bass", nil},
		{"foo:bar", nil},
		{"%", nil},
		{"_", allBut("hat")},
		{`"`, allIDs},
		{`'; DROP TABLE sample; --`, nil},
		{`" OR 1=1 --`, nil},
		{`kick" OR "snare`, nil},
		{"kick OR snare", nil},
		{"name:kick*", nil},

		// Tags.
		{"tag:drums", []string{"kick", "snare", "hat"}},
		{"tag:DRUMS/Kick", []string{"kick", "snare"}},
		{"#drums/kick", []string{"kick", "snare"}},
		{"tag:drums/hat", []string{"hat"}},
		{"tag:kick", []string{"lead"}},               // full path only
		{"#kick", []string{"kick", "snare", "lead"}}, // leaf anywhere
		{"#808", []string{"hat"}},                    // deep leaf
		{"#hat", []string{"hat"}},                    // leaf of an ancestor includes descendants
		{"#drums", []string{"kick", "snare", "hat"}}, // leaf form of a root
		{"#kic", nil},     // whole segments only
		{"tag:drum", nil}, // not a prefix match
		{"tag:keys", []string{"dm", "fsharp"}},
		{"-tag:drums", allBut("kick", "snare", "hat")},
		{"-#kick", allBut("kick", "snare", "lead")},
		{"#kick #snare", []string{"snare"}},
		{"is:untagged", []string{"pad"}},
		{"-is:untagged", allBut("pad")},

		// Packs and collections.
		{"pack:pk_A", packAIDs},
		{`pack:"kshmr vol 4"`, packAIDs},
		{"pack:kshmr", nil}, // exact name, not a substring
		{"@unsorted", packBIDs},
		{"-@unsorted", packAIDs},
		{"in:col_1", []string{"bass", "kick"}},
		{`in:"techno set"`, []string{"bass", "kick"}},
		{"-in:col_1", allBut("bass", "kick")},
		{"in:Empty", nil},
		{"in:nope", nil},

		// Tempo. Unknown BPM never matches a positive tempo filter.
		{"bpm:124", []string{"bass"}},
		{"bpm:120", []string{"dm"}}, // 120.4 is within ±0.5
		{"bpm:121", nil},
		{"bpm:120-128", []string{"bass", "lead", "dm"}},
		{"bpm:>124", []string{"lead", "vox"}},
		{"bpm:>=124", []string{"bass", "lead", "vox"}},
		{"bpm:<100", []string{"pad"}},
		{"bpm:<=100", []string{"pad", "fsharp"}},
		{"-bpm:<100", allBut("pad")},
		{"bpm:~124", []string{"bass", "dm", "pad", "vox"}}, // ±3 %, plus 62 and 248
		{"bpm:~62", []string{"pad", "bass", "dm"}},         // double time reaches 124
		{"bpm:~128", []string{"lead"}},                     // 124.16..131.84 excludes 124
		{"bpm:~200", []string{"fsharp"}},                   // half time reaches 100
		{"bpm:~180", nil},

		// Key.
		{"key:Am", []string{"bass"}},
		{"key:am", []string{"bass"}},
		{`key:"a minor"`, []string{"bass"}},
		{"key:Em", []string{"lead"}}, // stored from "E minor"
		{"key:Gb", []string{"fsharp"}},
		{"key:F#", []string{"fsharp"}},
		{"key:compatible:Am", []string{"bass", "pad", "lead", "dm"}}, // Am, C, Em, Dm
		{"key:compatible:C", []string{"pad", "bass"}},                // C, Am, G, F
		{"key:compatible:Bb", []string{"vox"}},                       // A#, Gm, F, D#
		{"key:compatible:B", []string{"fsharp"}},                     // B, G#m, F#, E
		{"-key:compatible:Am", allBut("bass", "pad", "lead", "dm")},
		{"-key:Am", allBut("bass")},

		// Type, length, format.
		{"type:loop", []string{"bass", "pad", "lead", "vox", "dm", "fsharp"}},
		{"type:oneshot", []string{"kick", "snare", "hat"}},
		{"-type:loop", []string{"kick", "snare", "hat"}},
		{"len:<1s", []string{"kick", "snare", "hat"}},
		{"len:<350ms", []string{"snare", "hat"}},
		{"len:>10s", []string{"pad"}},
		{"len:1s-4s", []string{"lead", "vox", "fsharp"}},
		{"len:1-4s", []string{"lead", "vox", "fsharp"}},
		{"len:>0.1m", []string{"bass", "pad", "dm"}},
		{"len:8", []string{"bass", "dm"}}, // ±10 %
		{"-len:<1s", allBut("kick", "snare", "hat")},
		{"fmt:wav", packAIDs},
		{"fmt:WAV", packAIDs},
		{"fmt:flac", []string{"pad"}},
		{"fmt:aiff", []string{"lead"}}, // stored as .aif
		{"fmt:mp3", []string{"hat"}},
		{"fmt:ogg", []string{"vox"}},
		{"-fmt:wav", packBIDs},

		// Flags.
		{"is:fav", []string{"kick"}},
		{"-is:fav", allBut("kick")},
		{"is:missing", []string{"hat"}},
		{"is:unanalyzed", []string{"hat"}},
		{"is:recent", []string{"vox", "dm"}},
		{"-is:recent", allBut("vox", "dm")},

		// Combinations.
		{"type:loop bpm:>100 -key:Am", []string{"lead", "vox", "dm"}},
		{"kick is:fav", []string{"kick"}},
		{"#drums -#kick @unsorted", []string{"hat"}},
		{"kshmr un", nil},
		{"key:compatible:Am bpm:~124 -@unsorted", []string{"bass", "dm"}},

		// Bad terms are dropped, the rest still filters.
		{"is:fav bpm:fast", []string{"kick"}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			if got, want := matches(t, ix, tt.query), sorted(tt.want...); !reflect.DeepEqual(got, want) {
				t.Errorf("query %q\n got %v\nwant %v", tt.query, got, want)
			}
			// Search must agree with SearchIDs.
			res, err := ix.Search(model.SearchRequest{Query: tt.query})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if res.Total != len(tt.want) || len(res.Rows) != len(tt.want) {
				t.Errorf("Search(%q): total %d, %d rows; want %d", tt.query, res.Total, len(res.Rows), len(tt.want))
			}
		})
	}
}

func TestSearchSort(t *testing.T) {
	ix := openFixture(t)
	tests := []struct {
		name, query, sort string
		desc              bool
		want              []string
	}{
		{"default is newest first", "", "", false, []string{"fsharp", "dm", "vox", "hat", "lead", "pad", "bass", "snare", "kick"}},
		{"added asc", "", "added", false, []string{"kick", "snare", "bass", "pad", "lead", "hat", "vox", "dm", "fsharp"}},
		{"added desc", "", "added", true, []string{"fsharp", "dm", "vox", "hat", "lead", "pad", "bass", "snare", "kick"}},
		{"name asc ignores case", "", "name", false, []string{"bass", "hat", "kick", "lead", "pad", "dm", "fsharp", "snare", "vox"}},
		{"name desc", "", "name", true, []string{"vox", "snare", "fsharp", "dm", "pad", "lead", "kick", "hat", "bass"}},
		{"bpm asc, unknown last", "", "bpm", false, []string{"pad", "fsharp", "dm", "bass", "lead", "vox", "kick", "snare", "hat"}},
		{"bpm desc, unknown last", "", "bpm", true, []string{"vox", "lead", "bass", "dm", "fsharp", "pad", "kick", "snare", "hat"}},
		{"key asc, unknown last", "", "key", false, []string{"pad", "dm", "lead", "fsharp", "vox", "bass", "kick", "snare", "hat"}},
		{"key desc, unknown last", "", "key", true, []string{"bass", "vox", "fsharp", "lead", "dm", "pad", "kick", "snare", "hat"}},
		{"dur asc", "", "dur", false, []string{"hat", "snare", "kick", "vox", "lead", "fsharp", "bass", "dm", "pad"}},
		{"dur desc", "", "dur", true, []string{"pad", "dm", "bass", "fsharp", "lead", "vox", "kick", "snare", "hat"}},
		{"pack asc then path", "", "pack", false, []string{"bass", "kick", "snare", "dm", "fsharp", "hat", "lead", "pad", "vox"}},
		{"pack desc", "", "pack", true, []string{"vox", "pad", "lead", "hat", "fsharp", "dm", "snare", "kick", "bass"}},
		{"unknown sort falls back", "", "bogus; DROP TABLE sample", false, []string{"fsharp", "dm", "vox", "hat", "lead", "pad", "bass", "snare", "kick"}},
		{"filter keeps sort", "type:loop", "bpm", true, []string{"vox", "lead", "bass", "dm", "fsharp", "pad"}},
		{"text with explicit sort", "kick", "added", false, []string{"kick", "snare", "lead"}},
		{"relevance without text is newest first", "type:oneshot", "relevance", false, []string{"hat", "snare", "kick"}},
		{"short text cannot rank, newest first", "ba", "", false, []string{"bass"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ix.SearchIDs(tt.query, tt.sort, tt.desc)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("\n got %v\nwant %v", got, tt.want)
			}
			res, err := ix.Search(model.SearchRequest{Query: tt.query, Sort: tt.sort, Desc: tt.desc})
			if err != nil {
				t.Fatal(err)
			}
			var rows []string
			for _, r := range res.Rows {
				rows = append(rows, r.ID)
			}
			if !reflect.DeepEqual(rows, tt.want) {
				t.Errorf("Search rows\n got %v\nwant %v", rows, tt.want)
			}
		})
	}
}

func TestSearchRelevance(t *testing.T) {
	ix := openFixture(t)
	// "kick" is in kick's name, path and tags, in snare's tags only and in
	// lead's tags only: the name match must rank first by default.
	for _, sort := range []string{"", "relevance"} {
		got, err := ix.SearchIDs("kick", sort, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 3 || got[0] != "kick" {
			t.Errorf("sort %q: got %v, want kick first of three", sort, got)
		}
	}
	// Relevance still honours the other filters.
	got, err := ix.SearchIDs("kick -is:fav", "relevance", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"lead", "snare"}; !reflect.DeepEqual(sorted(got...), want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSearchPaging(t *testing.T) {
	ix := openFixture(t)
	all, err := ix.SearchIDs("", "name", false)
	if err != nil {
		t.Fatal(err)
	}
	var paged []string
	for offset := 0; ; offset += 4 {
		res, err := ix.Search(model.SearchRequest{Sort: "name", Offset: offset, Limit: 4})
		if err != nil {
			t.Fatal(err)
		}
		if res.Total != len(allIDs) {
			t.Fatalf("offset %d: total %d, want %d", offset, res.Total, len(allIDs))
		}
		if len(res.Rows) == 0 {
			break
		}
		for _, r := range res.Rows {
			paged = append(paged, r.ID)
		}
	}
	if !reflect.DeepEqual(paged, all) {
		t.Errorf("pages %v, want %v", paged, all)
	}

	res, err := ix.Search(model.SearchRequest{Offset: -5, Limit: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != len(allIDs) {
		t.Errorf("negative offset/limit: %d rows, want all %d", len(res.Rows), len(allIDs))
	}
	res, err = ix.Search(model.SearchRequest{Offset: 100})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows == nil || len(res.Rows) != 0 || res.Total != len(allIDs) {
		t.Errorf("offset past the end: rows %v total %d", res.Rows, res.Total)
	}
}

func TestSearchRowsAndErrors(t *testing.T) {
	ix := openFixture(t)
	res, err := ix.Search(model.SearchRequest{Query: "key:Em"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error != "" || len(res.Rows) != 1 {
		t.Fatalf("got error %q and %d rows", res.Error, len(res.Rows))
	}
	want := model.Sample{
		ID: "lead", PackID: "pk_B", PackName: "Unsorted", Name: "Lead_Em_128", Ext: "aif",
		RelPath: "Lead_Em_128.aif", Size: 1000, Dur: 3.75, SR: 44100, Ch: 2, Type: "loop",
		BPM: 128, Key: "Em", Tags: []string{"synth/lead", "Kick"}, Added: 5000,
		Analyzed: true, Mini: "bWluaQ==",
	}
	if !reflect.DeepEqual(res.Rows[0], want) {
		t.Errorf("row\n got %+v\nwant %+v", res.Rows[0], want)
	}

	res, err = ix.Search(model.SearchRequest{Query: "is:missing"})
	if err != nil {
		t.Fatal(err)
	}
	if hat := res.Rows[0]; !hat.Missing || hat.Analyzed || hat.Mini != "" {
		t.Errorf("hat row = %+v", hat)
	}

	res, err = ix.Search(model.SearchRequest{Query: "is:untagged"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows[0].Tags == nil {
		t.Error("untagged row has nil Tags; the frontend needs []")
	}

	// A bad term is reported and the rest of the query still applies.
	res, err = ix.Search(model.SearchRequest{Query: "is:fav bpm:fast"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" || res.Total != 1 || res.Rows[0].ID != "kick" {
		t.Errorf("got error %q, total %d", res.Error, res.Total)
	}
}

func TestSamples(t *testing.T) {
	ix := openFixture(t)
	got, err := ix.Samples([]string{"vox", "nope", "kick", "vox"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if want := []string{"vox", "kick"}; !slices.Equal(ids, want) {
		t.Errorf("got %v, want %v", ids, want)
	}
	if got[1].PackName != "KSHMR Vol 4" || !got[1].Fav {
		t.Errorf("kick = %+v", got[1])
	}

	n, err := ix.Count()
	if err != nil || n != len(allIDs) {
		t.Errorf("Count = %d, %v", n, err)
	}
}

func TestSidebar(t *testing.T) {
	ix := openFixture(t)
	views := []model.SavedView{{ID: "v1", Name: "Loops", Query: "type:loop"}}
	sb, err := ix.Sidebar(views, map[string]string{"Drums/Kick": "#e66", "keys": "#66e"})
	if err != nil {
		t.Fatal(err)
	}
	want := model.Sidebar{
		All: 9, Favorites: 1, Untagged: 1, Recent: 0,
		Tags: []model.TagCount{
			{Tag: "bass", Count: 1},
			{Tag: "drums", Count: 3}, // snare has two drums/* tags and counts once
			{Tag: "drums/hat", Count: 1},
			{Tag: "drums/hat/808", Count: 1},
			{Tag: "drums/kick", Count: 2, Color: "#e66"},
			{Tag: "drums/snare", Count: 1},
			{Tag: "keys", Count: 2, Color: "#66e"},
			{Tag: "keys/piano", Count: 1},
			{Tag: "Kick", Count: 1},
			{Tag: "synth", Count: 1},
			{Tag: "synth/lead", Count: 1},
			{Tag: "vocal", Count: 1},
		},
		Packs: []model.PackInfo{
			{ID: "pk_A", Name: "KSHMR Vol 4", Dir: "Splice/KSHMR Vol 4", Source: "splice", Count: 5},
			{ID: "pk_B", Name: "Unsorted", Dir: "Unsorted", Source: "import", Count: 4},
		},
		Collections: []model.CollectionInfo{
			{ID: "col_2", Name: "Empty", Count: 0},
			{ID: "col_1", Name: "Techno Set", Count: 2}, // "gone" is not indexed, "bass" is listed twice
		},
		Views: views,
	}
	if !reflect.DeepEqual(sb, want) {
		t.Errorf("sidebar\n got %+v\nwant %+v", sb, want)
	}

	// Recent follows the newest batch that has samples.
	b1, _ := ix.NewBatch()
	if err := ix.UpsertSamples(fixtureB()[:2], b1); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.NewBatch(); err != nil { // an import that added nothing
		t.Fatal(err)
	}
	sb, err = ix.Sidebar(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sb.Recent != 2 {
		t.Errorf("Recent = %d, want 2", sb.Recent)
	}
	if sb.Views == nil {
		t.Error("Views is nil; the frontend needs []")
	}
}

func TestSidebarEmpty(t *testing.T) {
	sb, err := openTemp(t).Sidebar(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sb.All != 0 || sb.Recent != 0 || sb.Tags == nil || sb.Packs == nil || sb.Collections == nil {
		t.Errorf("empty sidebar = %+v", sb)
	}
}

func TestSidebarTagOrder(t *testing.T) {
	ix := openTemp(t)
	err := ix.UpsertSamples(inPack(packA, []Sample{
		sample("1", "a.wav", "", 1, 0, "", 1, "a/b", "a-b", "A"),
	}), 0)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := ix.Sidebar(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tc := range sb.Tags {
		got = append(got, tc.Tag)
	}
	// "a-b" sorts before "a/b" bytewise but must not split a from its child.
	if want := []string{"A", "a/b", "a-b"}; !slices.Equal(got, want) {
		t.Errorf("tags %v, want %v", got, want)
	}
}
