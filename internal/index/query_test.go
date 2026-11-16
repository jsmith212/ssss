package index

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

var inf = math.Inf(1)

func rng(lo, hi float64) Range { return Range{Lo: lo, Hi: hi} }

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want []Term
	}{
		{"", nil},
		{"   \t\n ", nil},
		{"kick", []Term{{Kind: KindText, Text: "kick"}}},
		{"Kick  DEEP", []Term{{Kind: KindText, Text: "kick"}, {Kind: KindText, Text: "deep"}}},
		{`"deep kick"`, []Term{{Kind: KindText, Text: "deep kick"}}},
		{`"deep kick`, []Term{{Kind: KindText, Text: "deep kick"}}},
		{`de"ep ki"ck`, []Term{{Kind: KindText, Text: "deep kick"}}},
		{`"say \"hi\" \\ there"`, []Term{{Kind: KindText, Text: `say "hi" \ there`}}},
		{`""`, nil},
		{"-kick", []Term{{Kind: KindText, Text: "kick", Neg: true}}},
		{`-"deep kick"`, []Term{{Kind: KindText, Text: "deep kick", Neg: true}}},
		{"-", nil},
		{"- kick", []Term{{Kind: KindText, Text: "kick"}}},
		{"--kick", []Term{{Kind: KindText, Text: "-kick", Neg: true}}},
		{"hi-hat", []Term{{Kind: KindText, Text: "hi-hat"}}},
		{"kick\x00\x01snare", []Term{{Kind: KindText, Text: "kick"}, {Kind: KindText, Text: "snare"}}},
		{"ｋｉｃｋ　ＡＢ", []Term{{Kind: KindText, Text: "ｋｉｃｋ"}, {Kind: KindText, Text: "ａｂ"}}},
		{"é", []Term{{Kind: KindText, Text: "é"}}}, // NFC

		// Literal tokens: quoting the start turns syntax off.
		{`"bpm:120"`, []Term{{Kind: KindText, Text: "bpm:120"}}},
		{`"#kick"`, []Term{{Kind: KindText, Text: "#kick"}}},
		{`"@pack"`, []Term{{Kind: KindText, Text: "@pack"}}},
		{`-"is:fav"`, []Term{{Kind: KindText, Text: "is:fav", Neg: true}}},

		// Unknown keys are free text.
		{"foo:bar", []Term{{Kind: KindText, Text: "foo:bar"}}},
		{":bar", []Term{{Kind: KindText, Text: ":bar"}}},
		{"http://x", []Term{{Kind: KindText, Text: "http://x"}}},

		{"tag:drums/kick", []Term{{Kind: KindTag, Text: "drums/kick"}}},
		{"TAG:Drums/Kick", []Term{{Kind: KindTag, Text: "drums/kick"}}},
		{"tag:/drums//kick/", []Term{{Kind: KindTag, Text: "drums/kick"}}},
		{"tag:kick", []Term{{Kind: KindTag, Text: "kick"}}},
		{`tag:"my tag/sub tag"`, []Term{{Kind: KindTag, Text: "my tag/sub tag"}}},
		{"#drums/kick", []Term{{Kind: KindTag, Text: "drums/kick"}}},
		{"#Kick", []Term{{Kind: KindTagLeaf, Text: "kick"}}},
		{`#"hi hat"`, []Term{{Kind: KindTagLeaf, Text: "hi hat"}}},
		{"-#kick", []Term{{Kind: KindTagLeaf, Text: "kick", Neg: true}}},
		{"-tag:drums", []Term{{Kind: KindTag, Text: "drums", Neg: true}}},
		{"#a:b", []Term{{Kind: KindTagLeaf, Text: "a:b"}}},

		{"pack:pk_01ABC", []Term{{Kind: KindPack, Text: "pk_01ABC"}}},
		{`pack:"KSHMR Vol 4"`, []Term{{Kind: KindPack, Text: "KSHMR Vol 4"}}},
		{`"pack":x`, []Term{{Kind: KindText, Text: "pack:x"}}},
		{"@Unsorted", []Term{{Kind: KindPack, Text: "Unsorted"}}},
		{`@"KSHMR Vol 4"`, []Term{{Kind: KindPack, Text: "KSHMR Vol 4"}}},
		{"-@Unsorted", []Term{{Kind: KindPack, Text: "Unsorted", Neg: true}}},
		{`in:"Techno set"`, []Term{{Kind: KindCollection, Text: "Techno set"}}},
		{"in:col_1", []Term{{Kind: KindCollection, Text: "col_1"}}},

		{"bpm:120", []Term{{Kind: KindBPM, Ranges: []Range{rng(119.5, 120.5)}}}},
		{"bpm:120.5", []Term{{Kind: KindBPM, Ranges: []Range{rng(120, 121)}}}},
		{"bpm:120-128", []Term{{Kind: KindBPM, Ranges: []Range{rng(120, 128)}}}},
		{"bpm:128-120", []Term{{Kind: KindBPM, Ranges: []Range{rng(120, 128)}}}},
		{"bpm:>100", []Term{{Kind: KindBPM, Ranges: []Range{{Lo: 100, Hi: inf, LoExcl: true}}}}},
		{"bpm:>=100", []Term{{Kind: KindBPM, Ranges: []Range{{Lo: 100, Hi: inf}}}}},
		{"bpm:<100", []Term{{Kind: KindBPM, Ranges: []Range{{Lo: -inf, Hi: 100, HiExcl: true}}}}},
		{"bpm:<=100", []Term{{Kind: KindBPM, Ranges: []Range{{Lo: -inf, Hi: 100}}}}},
		{"-bpm:<100", []Term{{Kind: KindBPM, Neg: true, Ranges: []Range{{Lo: -inf, Hi: 100, HiExcl: true}}}}},
		{"bpm:~100", []Term{{Kind: KindBPM, Ranges: []Range{rng(97, 103), rng(48.5, 51.5), rng(194, 206)}}}},

		{"key:Am", []Term{{Kind: KindKey, Text: "Am", Keys: []string{"Am"}}}},
		{"key:bb", []Term{{Kind: KindKey, Text: "A#", Keys: []string{"A#"}}}},
		{`key:"Db minor"`, []Term{{Kind: KindKey, Text: "C#m", Keys: []string{"C#m"}}}},
		{"key:compatible:Am", []Term{{Kind: KindKey, Text: "Am", Keys: []string{"Am", "C", "Em", "Dm"}}}},
		{"KEY:Compatible:c", []Term{{Kind: KindKey, Text: "C", Keys: []string{"C", "Am", "G", "F"}}}},
		{"-key:compatible:F#m", []Term{{Kind: KindKey, Neg: true, Text: "F#m", Keys: []string{"F#m", "A", "C#m", "Bm"}}}},

		{"type:loop", []Term{{Kind: KindType, Text: "loop"}}},
		{"type:OneShot", []Term{{Kind: KindType, Text: "oneshot"}}},
		{"type:one-shot", []Term{{Kind: KindType, Text: "oneshot"}}},

		{"len:<2s", []Term{{Kind: KindLen, Ranges: []Range{{Lo: -inf, Hi: 2, HiExcl: true}}}}},
		{"len:>10s", []Term{{Kind: KindLen, Ranges: []Range{{Lo: 10, Hi: inf, LoExcl: true}}}}},
		{"len:1s-4s", []Term{{Kind: KindLen, Ranges: []Range{rng(1, 4)}}}},
		{"len:1-4s", []Term{{Kind: KindLen, Ranges: []Range{rng(1, 4)}}}},
		{"len:500-900ms", []Term{{Kind: KindLen, Ranges: []Range{rng(0.5, 0.9)}}}},
		{"len:500ms-2s", []Term{{Kind: KindLen, Ranges: []Range{rng(0.5, 2)}}}},
		{"len:<500ms", []Term{{Kind: KindLen, Ranges: []Range{{Lo: -inf, Hi: 0.5, HiExcl: true}}}}},
		{"len:>1.5m", []Term{{Kind: KindLen, Ranges: []Range{{Lo: 90, Hi: inf, LoExcl: true}}}}},
		{"len:>3", []Term{{Kind: KindLen, Ranges: []Range{{Lo: 3, Hi: inf, LoExcl: true}}}}},
		{"len:10", []Term{{Kind: KindLen, Ranges: []Range{rng(9, 11)}}}},

		{"fmt:wav", []Term{{Kind: KindFmt, Text: "wav"}}},
		{"fmt:.FLAC", []Term{{Kind: KindFmt, Text: "flac"}}},

		{"is:fav", []Term{{Kind: KindFlag, Text: FlagFav}}},
		{"is:favourite", []Term{{Kind: KindFlag, Text: FlagFav}}},
		{"is:untagged", []Term{{Kind: KindFlag, Text: FlagUntagged}}},
		{"is:missing", []Term{{Kind: KindFlag, Text: FlagMissing}}},
		{"is:recent", []Term{{Kind: KindFlag, Text: FlagRecent}}},
		{"IS:Unanalysed", []Term{{Kind: KindFlag, Text: FlagUnanalyzed}}},
		{"-is:fav", []Term{{Kind: KindFlag, Text: FlagFav, Neg: true}}},

		{`kick -snare #drums pack:"KSHMR Vol 4" bpm:>100 is:fav`, []Term{
			{Kind: KindText, Text: "kick"},
			{Kind: KindText, Text: "snare", Neg: true},
			{Kind: KindTagLeaf, Text: "drums"},
			{Kind: KindPack, Text: "KSHMR Vol 4"},
			{Kind: KindBPM, Ranges: []Range{{Lo: 100, Hi: inf, LoExcl: true}}},
			{Kind: KindFlag, Text: FlagFav},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			q, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.in, err)
			}
			if !termsEqual(q.Terms, tt.want) {
				t.Errorf("Parse(%q)\n got %+v\nwant %+v", tt.in, q.Terms, tt.want)
			}
		})
	}
}

// termsEqual compares with a tolerance on range bounds, which are computed.
func termsEqual(got, want []Term) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.Kind != w.Kind || g.Neg != w.Neg || g.Text != w.Text || len(g.Ranges) != len(w.Ranges) {
			return false
		}
		if len(g.Keys)+len(w.Keys) > 0 && !reflect.DeepEqual(g.Keys, w.Keys) {
			return false
		}
		for j := range g.Ranges {
			a, b := g.Ranges[j], w.Ranges[j]
			if a.LoExcl != b.LoExcl || a.HiExcl != b.HiExcl || !near(a.Lo, b.Lo) || !near(a.Hi, b.Hi) {
				return false
			}
		}
	}
	return true
}

func near(a, b float64) bool {
	return a == b || math.Abs(a-b) < 1e-9
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		in       string
		wantErr  string // substring of the message
		wantKept []Term // terms that survive
	}{
		{"bpm:fast", "bpm:", nil},
		{"bpm:", "bpm:", nil},
		{"bpm:120-", "bpm:", nil},
		{"bpm:-120", "bpm:", nil},
		{"bpm:~", "bpm:", nil},
		{"bpm:~0", "bpm:", nil},
		{"bpm:1e3", "bpm:", nil},
		{"bpm:inf", "bpm:", nil},
		{"bpm:NaN", "bpm:", nil},
		{"bpm:0x10", "bpm:", nil},
		{"bpm:1.2.3", "bpm:", nil},
		{"bpm:>", "bpm:", nil},
		{"bpm:99999999999999999999", "bpm:", nil},
		{"key:H", "key:", nil},
		{"key:", "key:", nil},
		{"key:Amx", "key:", nil},
		{"key:compatible:", "key:", nil},
		{"key:compatible:zz", "key:", nil},
		{"type:drum", "type:", nil},
		{"len:long", "len:", nil},
		{"len:5h", "len:", nil},
		{"len:<", "len:", nil},
		{"is:nice", "is:", nil},
		{"is:", "is:", nil},
		{"tag:", "tag:", nil},
		{"tag:///", "tag:", nil},
		{"#", "tag:", nil},
		{"@", "pack:", nil},
		{"pack:", "pack:", nil},
		{"in:", "in:", nil},
		{"fmt:", "fmt:", nil},
		{"kick bpm:fast is:fav", "bpm:", []Term{{Kind: KindText, Text: "kick"}, {Kind: KindFlag, Text: FlagFav}}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			q, err := Parse(tt.in)
			var perr *ParseError
			if !errors.As(err, &perr) {
				t.Fatalf("Parse(%q) error = %v, want *ParseError", tt.in, err)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Parse(%q) error = %q, want it to mention %q", tt.in, err, tt.wantErr)
			}
			if !termsEqual(q.Terms, tt.wantKept) {
				t.Errorf("Parse(%q) kept %+v, want %+v", tt.in, q.Terms, tt.wantKept)
			}
		})
	}

	_, err := Parse("bpm:x key:y")
	var perr *ParseError
	if !errors.As(err, &perr) || len(perr.Problems) != 2 {
		t.Errorf("two bad terms: got %v, want two problems", err)
	}
}

func TestHasText(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"kick", true},
		{"-kick", false},
		{"tag:x bpm:120", false},
		{"tag:x kick", true},
		{"foo:bar", true},
	}
	for _, tt := range tests {
		q, _ := Parse(tt.in)
		if got := q.HasText(); got != tt.want {
			t.Errorf("Parse(%q).HasText() = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"C", "C", true},
		{"c", "C", true},
		{"Am", "Am", true},
		{"am", "Am", true},
		{"AM", "Am", true}, // case-insensitive: a trailing m is always minor
		{"A minor", "Am", true},
		{"Amin", "Am", true},
		{"A-min", "Am", true},
		{"Amaj", "A", true},
		{"A major", "A", true},
		{"F#", "F#", true},
		{"f#m", "F#m", true},
		{"F♯m", "F#m", true},
		{"Gb", "F#", true},
		{"Bb", "A#", true},
		{"bb", "A#", true},
		{"B♭ minor", "A#m", true},
		{"bbm", "A#m", true},
		{"b", "B", true},
		{"bm", "Bm", true},
		{"Db", "C#", true},
		{"Eb", "D#", true},
		{"Ab", "G#", true},
		{"Cb", "B", true},
		{"Fb", "E", true},
		{"E#", "F", true},
		{"B#", "C", true},
		{" G ", "G", true},
		{"", "", false},
		{"H", "", false},
		{"Am7", "", false},
		{"A##", "", false},
		{"minor", "", false},
		{"♯", "", false},
	}
	for _, tt := range tests {
		got, ok := NormalizeKey(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("NormalizeKey(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCompatibleKeys(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"Am", []string{"Am", "C", "Em", "Dm"}},
		{"C", []string{"C", "Am", "G", "F"}},
		{"F#", []string{"F#", "D#m", "C#", "B"}},
		{"Bbm", []string{"A#m", "C#", "Fm", "D#m"}},
		{"B", []string{"B", "G#m", "F#", "E"}},
		{"nope", nil},
	}
	for _, tt := range tests {
		if got := CompatibleKeys(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("CompatibleKeys(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestKeyOrd(t *testing.T) {
	order := []string{"C", "Cm", "C#", "C#m", "D", "A#m", "B", "Bm"}
	prev := -1
	for _, k := range order {
		o := keyOrd(k)
		if o <= prev {
			t.Errorf("keyOrd(%q) = %d, want more than %d", k, o, prev)
		}
		prev = o
	}
	if got := keyOrd(""); got != -1 {
		t.Errorf(`keyOrd("") = %d, want -1`, got)
	}
}
