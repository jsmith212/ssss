package analysis

import "strings"

var noteNames = [12]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

// naturalPC maps the letters A..G to pitch classes (C = 0).
var naturalPC = [7]int{9, 11, 0, 2, 4, 5, 7}

// formatKey renders a pitch class and mode in the library notation:
// sharps only, "F#" for major and "F#m" for minor.
func formatKey(pc int, minor bool) string {
	name := noteNames[((pc%12)+12)%12]
	if minor {
		return name + "m"
	}
	return name
}

// splitKey is the inverse of formatKey. ok is false for anything that is not
// already in library notation.
func splitKey(key string) (pc int, minor, ok bool) {
	name, minor := strings.CutSuffix(key, "m")
	for i, n := range noteNames {
		if n == name {
			return i, minor, true
		}
	}
	return 0, false, false
}

// keySpelling is one key as written by a human, before any judgement about
// whether the text was meant as a key at all.
type keySpelling struct {
	pc         int
	minor      bool
	upper      bool   // note letter was upper case
	accidental string // "", "#", "b" (ASCII or unicode sign), "word" (sharp/flat)
	mode       string // "", "m", "M", or the lower-cased word (min, minor, maj, major)
	rest       string // unparsed remainder, e.g. "7" in "Gmin7"
}

// parseSpelling reads a note letter, an optional accidental and an optional
// mode from the start of s. Anything left over is returned in rest.
func parseSpelling(s string) (keySpelling, bool) {
	if s == "" {
		return keySpelling{}, false
	}
	var k keySpelling
	c := s[0]
	switch {
	case c >= 'A' && c <= 'G':
		k.upper = true
		k.pc = naturalPC[c-'A']
	case c >= 'a' && c <= 'g':
		k.pc = naturalPC[c-'a']
	default:
		return keySpelling{}, false
	}
	rest := s[1:]

	switch {
	case strings.HasPrefix(rest, "#"):
		k.pc, k.accidental, rest = k.pc+1, "#", rest[1:]
	case strings.HasPrefix(rest, "♯"):
		k.pc, k.accidental, rest = k.pc+1, "#", rest[len("♯"):]
	case strings.HasPrefix(rest, "♭"):
		k.pc, k.accidental, rest = k.pc+11, "b", rest[len("♭"):]
	case hasPrefixFold(rest, "sharp"):
		k.pc, k.accidental, rest = k.pc+1, "word", rest[len("sharp"):]
	case hasPrefixFold(rest, "flat"):
		k.pc, k.accidental, rest = k.pc+11, "word", rest[len("flat"):]
	case strings.HasPrefix(rest, "b"):
		// Only a flat when what follows still reads as a mode ("Bb",
		// "Bbm", "Bbmaj7"); "Abby" must not lose its b.
		if _, _, ok := parseMode(rest[1:]); ok || startsChordQuality(rest[1:]) {
			k.pc, k.accidental, rest = k.pc+11, "b", rest[1:]
		}
	}
	k.pc %= 12

	mode, minor, ok := parseMode(rest)
	if ok {
		k.mode, k.minor, rest = mode, minor, ""
	}
	k.rest = rest
	return k, true
}

// parseMode recognises a whole-string mode suffix.
func parseMode(s string) (mode string, minor, ok bool) {
	switch s {
	case "":
		return "", false, true
	case "m":
		return "m", true, true
	case "M":
		return "M", false, true
	}
	switch l := strings.ToLower(s); l {
	case "min", "minor":
		return l, true, true
	case "maj", "major":
		return l, false, true
	}
	return "", false, false
}

func startsChordQuality(s string) bool {
	if s == "" {
		return false
	}
	_, ok := parseChordQuality(s)
	return ok
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// NormalizeKey converts a human spelling of a musical key ("Bb minor",
// "f#m", "C♯ maj", "Gb", "A min") to the library notation: sharps only,
// "F#" for major and "F#m" for minor. It is lenient about case and
// separators and is meant for trusted input such as user edits, query terms
// and Splice metadata; filename parsing applies stricter rules.
func NormalizeKey(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	// Split "A minor" / "F#_min" / "c sharp major" on the first separator
	// and glue the pieces back so parseSpelling sees one word.
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '_' || r == '-' || r == '\t'
	}), "")
	k, ok := parseSpelling(s)
	if !ok || k.rest != "" {
		return "", false
	}
	return formatKey(k.pc, k.minor), true
}
