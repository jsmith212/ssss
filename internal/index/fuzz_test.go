package index

import (
	"math"
	"testing"

	"ssss/internal/model"
)

// FuzzQuery checks that no input panics the parser, that what it produces is
// well formed, and that the compiled SQL always executes: user text reaching
// the SQL or FTS5 syntax layer would surface here as a database error.
func FuzzQuery(f *testing.F) {
	for _, seed := range []string{
		"", "kick", `pack:"KSHMR Vol 4" -#drums/kick bpm:~124`, "bpm:120-128 key:compatible:Am",
		"len:1s-4s type:loop fmt:wav is:fav -is:untagged", `"unterminated`, `a"b"c:d"e`, "-", "--", "-\"",
		"bpm:", "bpm:>", "bpm:~", "key:compatible:", "len:-", "#", "@", ":", "::", "tag:/", `\`, `"\`, `"\"`,
		"'; DROP TABLE sample; --", `" OR "`, "kick* NEAR(a b)", "name : x", "{name}:x", "a AND b OR NOT c",
		"%_\\", "\x00", "\xff\xfe", "ｋｉｃｋ", "é", "bpm:1e309", "len:9999999999999999m", "in:\"\"",
		"ab", "a", "^abc", "abc^2", "(abc)", "abc + def", `""""`, "tag:a/b/c/d/e/f", "#é", "is:FAV",
	} {
		f.Add(seed, "", false)
	}
	f.Add("kick", "relevance", true)
	f.Add("kick", "pack", false)

	ix := openFixture(f)
	f.Fuzz(func(t *testing.T, query, sort string, desc bool) {
		q, err := Parse(query)
		for _, term := range q.Terms {
			if term.Kind < KindText || term.Kind > KindFlag {
				t.Errorf("term %+v has an unknown kind", term)
			}
			for _, r := range term.Ranges {
				if math.IsNaN(r.Lo) || math.IsNaN(r.Hi) || r.Lo > r.Hi {
					t.Errorf("term %+v has a malformed range", term)
				}
			}
			if (term.Kind == KindBPM || term.Kind == KindLen) && len(term.Ranges) == 0 {
				t.Errorf("term %+v has no range", term)
			}
			if term.Kind == KindKey && len(term.Keys) == 0 {
				t.Errorf("term %+v has no key", term)
			}
			if term.Kind != KindBPM && term.Kind != KindLen && term.Text == "" {
				t.Errorf("term %+v has no text", term)
			}
		}

		ids, dbErr := ix.SearchIDs(query, sort, desc)
		if dbErr != nil {
			t.Fatalf("SearchIDs(%q, %q): %v", query, sort, dbErr)
		}
		res, dbErr := ix.Search(model.SearchRequest{Query: query, Sort: sort, Desc: desc})
		if dbErr != nil {
			t.Fatalf("Search(%q, %q): %v", query, sort, dbErr)
		}
		if res.Total != len(ids) || len(res.Rows) != len(ids) {
			t.Errorf("Search(%q) total %d rows %d, SearchIDs %d", query, res.Total, len(res.Rows), len(ids))
		}
		if (err != nil) != (res.Error != "") {
			t.Errorf("Parse error %v but SearchResult.Error %q", err, res.Error)
		}
		if n, cErr := ix.Count(); cErr != nil || n != len(allIDs) {
			t.Fatalf("index damaged by %q: count %d, %v", query, n, cErr)
		}
	})
}
