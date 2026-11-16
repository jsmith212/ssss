package index

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Kind says what a Term filters on.
type Kind int

const (
	KindText       Kind = iota // Text: lower-cased substring of name, path, pack name or tags
	KindTag                    // Text: lower-cased tag path; matches it and its descendants
	KindTagLeaf                // Text: lower-cased last segment of a tag path (the "#kick" form)
	KindPack                   // Text: pack name or id as typed
	KindCollection             // Text: collection name or id as typed
	KindBPM                    // Ranges: any of them
	KindKey                    // Keys: any of them
	KindType                   // Text: "loop" or "oneshot"
	KindLen                    // Ranges: seconds
	KindFmt                    // Text: lower-cased extension without the dot
	KindFlag                   // Text: one of the Flag constants
)

// Values of Term.Text for KindFlag.
const (
	FlagFav        = "fav"
	FlagUntagged   = "untagged"
	FlagMissing    = "missing"
	FlagRecent     = "recent"
	FlagUnanalyzed = "unanalyzed"
)

// Range is a numeric interval. An infinite bound is open-ended.
type Range struct {
	Lo, Hi         float64
	LoExcl, HiExcl bool
}

// Term is one filter of a query.
type Term struct {
	Kind   Kind
	Neg    bool
	Text   string
	Ranges []Range
	Keys   []string
}

// Query is a parsed omnibar string: every term must hold.
type Query struct {
	Terms []Term
}

// HasText reports whether the query has a positive free-text term, which is
// when relevance is the natural sort order.
func (q Query) HasText() bool {
	for _, t := range q.Terms {
		if t.Kind == KindText && !t.Neg {
			return true
		}
	}
	return false
}

// ParseError lists the terms of a query that could not be understood.
type ParseError struct {
	Problems []string
}

func (e *ParseError) Error() string { return strings.Join(e.Problems, "; ") }

// Limits on what Parse reads. Nobody types a query this long; one that is
// pasted or generated would otherwise turn into SQL with more terms and
// parameters than SQLite accepts.
const (
	maxQueryBytes = 4096
	maxQueryTerms = 64
)

const (
	bpmExactTolerance  = 0.5  // bpm:120 matches 119.5..120.5
	bpmApproxTolerance = 0.03 // bpm:~124 matches within 3 %
	lenExactTolerance  = 0.1  // len:2s matches within 10 %
)

// Parse turns an omnibar string into a Query. It never panics. Terms it
// cannot understand are left out and described in the returned *ParseError;
// the Query is usable either way, so a list can stay populated while the user
// is half-way through typing a token.
func Parse(s string) (Query, error) {
	var (
		q        Query
		problems []string
	)
	if len(s) > maxQueryBytes {
		s = strings.ToValidUTF8(s[:maxQueryBytes], "")
		problems = append(problems, fmt.Sprintf("query too long: only its first %d bytes are used", maxQueryBytes))
	}
	toks := lex(norm.NFC.String(s))
	if len(toks) > maxQueryTerms {
		toks = toks[:maxQueryTerms]
		problems = append(problems, fmt.Sprintf("too many terms: only the first %d are used", maxQueryTerms))
	}
	for _, tok := range toks {
		term, err := parseToken(tok)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		term.Neg = tok.neg
		q.Terms = append(q.Terms, term)
	}
	if problems != nil {
		return q, &ParseError{Problems: problems}
	}
	return q, nil
}

func parseToken(tok token) (Term, error) {
	text := Term{Kind: KindText, Text: strings.ToLower(tok.text)}
	if tok.literal {
		return text, nil
	}
	switch tok.text[0] {
	case '#':
		return parseTag(tok.text[1:], true)
	case '@':
		return parsePack(tok.text[1:])
	}
	if tok.colon <= 0 {
		return text, nil
	}
	val := strings.TrimSpace(tok.text[tok.colon+1:])
	switch strings.ToLower(tok.text[:tok.colon]) {
	case "tag":
		return parseTag(val, false)
	case "pack":
		return parsePack(val)
	case "in":
		if val == "" {
			return Term{}, fmt.Errorf("in: needs a collection name")
		}
		return Term{Kind: KindCollection, Text: val}, nil
	case "bpm":
		return parseBPM(val)
	case "key":
		return parseKeyTerm(val)
	case "type":
		return parseType(val)
	case "len":
		return parseLen(val)
	case "fmt":
		ext := strings.ToLower(strings.TrimPrefix(val, "."))
		if ext == "" {
			return Term{}, fmt.Errorf("fmt: needs a format such as wav")
		}
		return Term{Kind: KindFmt, Text: ext}, nil
	case "is":
		return parseFlag(val)
	}
	return text, nil
}

// normTag canonicalises a tag path: segments trimmed, empty ones dropped.
func normTag(tag string) string {
	parts := strings.Split(tag, "/")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "/")
}

func parseTag(val string, sigil bool) (Term, error) {
	tag := strings.ToLower(normTag(val))
	if tag == "" {
		return Term{}, fmt.Errorf("tag: needs a tag name")
	}
	if sigil && !strings.Contains(tag, "/") {
		return Term{Kind: KindTagLeaf, Text: tag}, nil
	}
	return Term{Kind: KindTag, Text: tag}, nil
}

func parsePack(val string) (Term, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return Term{}, fmt.Errorf("pack: needs a pack name")
	}
	return Term{Kind: KindPack, Text: val}, nil
}

func parseBPM(val string) (Term, error) {
	bad := fmt.Errorf("bpm: %q is not a tempo (try 120, 120-128, ~124, >100)", val)
	if rest, ok := strings.CutPrefix(val, "~"); ok {
		bpm, ok := parseNumber(rest)
		if !ok || bpm <= 0 {
			return Term{}, bad
		}
		// Half and double time are the same groove to a DJ or a stretcher.
		var ranges []Range
		for _, c := range []float64{bpm, bpm / 2, bpm * 2} {
			ranges = append(ranges, Range{Lo: c * (1 - bpmApproxTolerance), Hi: c * (1 + bpmApproxTolerance)})
		}
		return Term{Kind: KindBPM, Ranges: ranges}, nil
	}
	r, exact, ok := parseRange(val, parseNumber)
	if !ok {
		return Term{}, bad
	}
	if exact {
		r.Lo, r.Hi = r.Lo-bpmExactTolerance, r.Hi+bpmExactTolerance
	}
	return Term{Kind: KindBPM, Ranges: []Range{r}}, nil
}

func parseLen(val string) (Term, error) {
	// "1-4s" means one to four seconds: a bare lower bound takes the unit
	// of the upper one.
	if lo, hi, found := strings.Cut(val, "-"); found {
		if _, bare := parseNumber(lo); bare {
			val = lo + strings.TrimLeft(hi, "0123456789.") + "-" + hi
		}
	}
	r, exact, ok := parseRange(val, parseDuration)
	if !ok {
		return Term{}, fmt.Errorf("len: %q is not a length (try <2s, >10s, 1s-4s, 500ms)", val)
	}
	if exact {
		r.Lo, r.Hi = r.Lo*(1-lenExactTolerance), r.Hi*(1+lenExactTolerance)
	}
	return Term{Kind: KindLen, Ranges: []Range{r}}, nil
}

func parseKeyTerm(val string) (Term, error) {
	if rest, ok := cutPrefixFold(val, "compatible:"); ok {
		keys := CompatibleKeys(rest)
		if keys == nil {
			return Term{}, fmt.Errorf("key: %q is not a key (try key:compatible:Am)", rest)
		}
		return Term{Kind: KindKey, Text: keys[0], Keys: keys}, nil
	}
	key, ok := NormalizeKey(val)
	if !ok {
		return Term{}, fmt.Errorf("key: %q is not a key (try Am, F#, Bb minor)", val)
	}
	return Term{Kind: KindKey, Text: key, Keys: []string{key}}, nil
}

func parseType(val string) (Term, error) {
	switch strings.ToLower(val) {
	case "loop", "loops":
		return Term{Kind: KindType, Text: "loop"}, nil
	case "oneshot", "oneshots", "one-shot", "one_shot", "shot":
		return Term{Kind: KindType, Text: "oneshot"}, nil
	}
	return Term{}, fmt.Errorf("type: %q is not loop or oneshot", val)
}

func parseFlag(val string) (Term, error) {
	var flag string
	switch strings.ToLower(val) {
	case "fav", "favs", "favorite", "favorites", "favourite", "favourites":
		flag = FlagFav
	case "untagged":
		flag = FlagUntagged
	case "missing":
		flag = FlagMissing
	case "recent":
		flag = FlagRecent
	case "unanalyzed", "unanalysed":
		flag = FlagUnanalyzed
	default:
		return Term{}, fmt.Errorf("is: %q is not one of fav, untagged, missing, recent, unanalyzed", val)
	}
	return Term{Kind: KindFlag, Text: flag}, nil
}

// parseRange reads ">x", ">=x", "<x", "<=x", "a-b" or "x". exact is set for
// the last form, where the caller decides the tolerance.
func parseRange(val string, num func(string) (float64, bool)) (r Range, exact, ok bool) {
	r = Range{Lo: math.Inf(-1), Hi: math.Inf(1)}
	for _, op := range []string{">=", "<=", ">", "<"} {
		rest, found := strings.CutPrefix(val, op)
		if !found {
			continue
		}
		v, ok := num(rest)
		if !ok {
			return r, false, false
		}
		if op[0] == '>' {
			r.Lo, r.LoExcl = v, op == ">"
		} else {
			r.Hi, r.HiExcl = v, op == "<"
		}
		return r, false, true
	}
	if lo, hi, found := strings.Cut(val, "-"); found {
		a, okA := num(lo)
		b, okB := num(hi)
		if !okA || !okB {
			return r, false, false
		}
		r.Lo, r.Hi = min(a, b), max(a, b)
		return r, false, true
	}
	v, ok := num(val)
	if !ok {
		return r, false, false
	}
	r.Lo, r.Hi = v, v
	return r, true, true
}

// parseNumber accepts plain decimals only; strconv on its own would also take
// "inf", "nan", hex floats and exponents.
func parseNumber(s string) (float64, bool) {
	if s == "" || len(s) > 16 {
		return 0, false
	}
	digits, dots := 0, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '.':
			dots++
		default:
			return 0, false
		}
	}
	if digits == 0 || dots > 1 {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

// parseDuration reads a length in seconds: "2", "2s", "500ms", "1.5m".
func parseDuration(s string) (float64, bool) {
	s = strings.ToLower(s)
	scale := 1.0
	for _, u := range []struct {
		suffix string
		scale  float64
	}{{"ms", 0.001}, {"sec", 1}, {"s", 1}, {"min", 60}, {"m", 60}} {
		if rest, ok := strings.CutSuffix(s, u.suffix); ok {
			s, scale = rest, u.scale
			break
		}
	}
	v, ok := parseNumber(s)
	return v * scale, ok
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}
