package index

import "strings"

var pitchNames = [12]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

var naturalPitch = map[byte]int{'c': 0, 'd': 2, 'e': 4, 'f': 5, 'g': 7, 'a': 9, 'b': 11}

type musicalKey struct {
	pc    int // pitch class, 0 = C
	minor bool
}

func (k musicalKey) String() string {
	if k.minor {
		return pitchNames[k.pc] + "m"
	}
	return pitchNames[k.pc]
}

// ord orders keys chromatically, major before minor.
func (k musicalKey) ord() int {
	o := k.pc * 2
	if k.minor {
		o++
	}
	return o
}

// parseKey reads the spellings people type: "Am", "a minor", "Bb", "Dbmaj",
// "F#min", "G♯m". Matching is case-insensitive, so a lone "m" suffix always
// means minor.
func parseKey(s string) (musicalKey, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("♯", "#", "♭", "b").Replace(s)
	if s == "" {
		return musicalKey{}, false
	}
	pc, ok := naturalPitch[s[0]]
	if !ok {
		return musicalKey{}, false
	}
	rest := s[1:]
	switch {
	case strings.HasPrefix(rest, "#"):
		pc, rest = pc+1, rest[1:]
	case strings.HasPrefix(rest, "b"):
		pc, rest = pc+11, rest[1:]
	}
	k := musicalKey{pc: pc % 12}
	switch strings.Trim(rest, " -_") {
	case "", "maj", "major":
	case "m", "min", "minor":
		k.minor = true
	default:
		return musicalKey{}, false
	}
	return k, true
}

// NormalizeKey converts any accepted key spelling to the library notation:
// sharps only, "C" for major and "Cm" for minor. ok is false when s is not a
// key.
func NormalizeKey(s string) (key string, ok bool) {
	k, ok := parseKey(s)
	if !ok {
		return "", false
	}
	return k.String(), true
}

// CompatibleKeys returns the keys that mix with key: itself, its relative
// major or minor, and one step either way on the circle of fifths. It returns
// nil when key is not a key.
func CompatibleKeys(key string) []string {
	k, ok := parseKey(key)
	if !ok {
		return nil
	}
	relative := musicalKey{pc: (k.pc + 3) % 12, minor: false}
	if !k.minor {
		relative = musicalKey{pc: (k.pc + 9) % 12, minor: true}
	}
	up := musicalKey{pc: (k.pc + 7) % 12, minor: k.minor}
	down := musicalKey{pc: (k.pc + 5) % 12, minor: k.minor}
	return []string{k.String(), relative.String(), up.String(), down.String()}
}

// keyOrd is the sort position of a stored key, -1 when unknown.
func keyOrd(key string) int {
	k, ok := parseKey(key)
	if !ok {
		return -1
	}
	return k.ord()
}
