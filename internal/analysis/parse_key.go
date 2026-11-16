package analysis

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var chordQuality = regexp.MustCompile(`^(maj|min|m|M|dim|aug|sus|add)?(\d{1,2})?(sus[24]|add\d{1,2})?$`)

type keyStrength int

const (
	keyExplicit   keyStrength = iota // accidental or mode spelled out
	keyContextual                    // "A", "am", "Eb": only a key next to a tempo
	keyChord                         // "Gmin7", "C3": root of something else
)

type keyCand struct {
	span
	key      string
	strength keyStrength
	bare     bool // a lone upper-case letter
	note     bool // a note with an octave ("C3"), not a chord name
}

var modeWords = map[string]bool{"min": true, "minor": true, "maj": true, "major": true}

// Transposing instruments are named by their pitch: a "Bb Clarinet" or an
// "Eb Alto Sax" says what the instrument is, not what key the recording is
// in. instrumentSizes may stand between the pitch and the instrument.
var (
	pitchedInstruments = map[string]bool{
		"clarinet": true, "trumpet": true, "cornet": true, "flugelhorn": true, "horn": true,
		"sax": true, "saxophone": true, "tuba": true, "flute": true, "piccolo": true,
		"trombone": true, "euphonium": true, "recorder": true, "whistle": true, "harmonica": true,
	}
	instrumentSizes = map[string]bool{
		"soprano": true, "alto": true, "tenor": true, "baritone": true, "bari": true,
		"bass": true, "french": true, "english": true, "tin": true,
	}
)

// namesInstrument reports whether the tokens after a pitch name a
// transposing instrument.
func namesInstrument(next func(offset int) string) bool {
	n := next(1)
	if instrumentSizes[n] {
		n = next(2)
	}
	if _, ok := lookupWord(pitchedInstruments, n); ok {
		return true
	}
	return false
}

// keyAt reads a key spelling starting at token i, applying the case and
// context rules that keep ordinary words and version letters out.
func keyAt(tokens, lower []string, i int) (keyCand, bool) {
	tok := tokens[i]
	k, ok := parseSpelling(tok)
	if !ok {
		return keyCand{}, false
	}
	cand := keyCand{span: span{i, i}}
	next := func(offset int) string {
		if i+offset < len(lower) {
			return lower[i+offset]
		}
		return ""
	}
	// takeMode absorbs a following "min"/"major" token.
	takeMode := func() {
		if n := next(cand.end - i + 1); modeWords[n] {
			k.minor = strings.HasPrefix(n, "min")
			cand.end++
		}
	}

	if k.rest != "" {
		minor, ok := parseChordQuality(k.rest)
		if !ok || !k.upper {
			return keyCand{}, false
		}
		cand.key, cand.strength = formatKey(k.pc, minor), keyChord
		cand.note = strings.Trim(k.rest, "0123456789") == ""
		return cand, true
	}
	if k.upper && k.mode == "" && namesInstrument(next) {
		return keyCand{}, false // "Bb Clarinet", "Eb Alto Sax", "F Horn"
	}
	if tok == "Am" && (next(1) == "i" || (i > 0 && lower[i-1] == "i")) {
		return keyCand{}, false // "Am I Dreaming", "I Am"
	}

	wordMode := modeWords[k.mode]
	switch {
	case k.mode == "M" && (k.accidental == "" || !k.upper):
		return keyCand{}, false // "AM", "FM", "DM" are words

	case k.mode != "" && (k.upper || wordMode):
		// "Am", "F#m", "Cmaj", "amin"
		cand.strength = keyExplicit

	case k.mode != "":
		// "am", "ebm": too close to ordinary words on their own
		if k.accidental == "#" || k.accidental == "word" {
			cand.strength = keyExplicit
		} else {
			cand.strength = keyContextual
		}

	case k.accidental == "#" || k.accidental == "word" || (k.accidental == "b" && k.upper):
		// "F#", "Bb", "Fsharp", "f#"
		cand.strength = keyExplicit
		takeMode()

	case k.accidental == "b":
		cand.strength = keyContextual // "eb", "ab"

	default: // a lone letter
		switch n := next(1); {
		case n == "sharp" || n == "flat":
			if n == "sharp" {
				k.pc = (k.pc + 1) % 12
			} else {
				k.pc = (k.pc + 11) % 12
			}
			cand.end++
			cand.strength = keyExplicit
			takeMode()
		case modeWords[n]:
			cand.strength = keyExplicit
			takeMode()
		case !k.upper:
			return keyCand{}, false
		case tok == "A" && isOrdinaryWord(n):
			return keyCand{}, false // the article: "A Day In"
		default:
			cand.strength, cand.bare = keyContextual, true
		}
	}
	cand.key = formatKey(k.pc, k.minor)
	return cand, true
}

// isOrdinaryWord reports whether a lower-cased token is a plain word the
// keyword table knows nothing about.
func isOrdinaryWord(w string) bool {
	if len(w) < 2 {
		return false
	}
	for _, r := range w {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	if _, ok := lookupWord(keywordTags, w); ok {
		return false
	}
	if _, ok := lookupWord(loopishWords, w); ok {
		return false
	}
	_, ok := lookupWord(oneShotWords, w)
	return !ok
}

// parseChordQuality accepts what may follow a root in a chord or note name:
// "min7", "maj9", "7", "sus4", "add9", "dim", or an octave digit.
func parseChordQuality(s string) (minor, ok bool) {
	m := chordQuality.FindStringSubmatch(s)
	if s == "" || m == nil {
		return false, false
	}
	if m[2] != "" {
		switch n, _ := strconv.Atoi(m[2]); {
		case n <= 9, n == 11, n == 13:
		default:
			return false, false
		}
	}
	return m[1] == "min" || m[1] == "m" || m[1] == "dim", true
}
