package analysis

import (
	"strings"
	"unicode"
)

// Type values shared with the rest of the app.
const (
	TypeLoop    = "loop"
	TypeOneShot = "oneshot"
)

// keywordTags maps a lower-cased whole word to the tag it suggests. Plurals
// are handled by the lookup, so only singular forms are listed.
var keywordTags = map[string]string{
	"kick": "drums/kick", "kik": "drums/kick", "bd": "drums/kick",
	"snare": "drums/snare", "snr": "drums/snare", "sd": "drums/snare", "rim": "drums/snare", "rimshot": "drums/snare",
	"clap": "drums/clap", "clp": "drums/clap",
	"snap": "drums/snap",
	"hh":   "drums/hihat", "hat": "drums/hihat", "hihat": "drums/hihat", "ohh": "drums/hihat", "chh": "drums/hihat",
	"cym": "drums/cymbal", "cymbal": "drums/cymbal", "crash": "drums/cymbal", "ride": "drums/cymbal",
	"tom":  "drums/tom",
	"perc": "drums/percussion", "percussion": "drums/percussion", "conga": "drums/percussion",
	"bongo": "drums/percussion", "shaker": "drums/percussion", "tamb": "drums/percussion",
	"tambourine": "drums/percussion", "cowbell": "drums/percussion", "clave": "drums/percussion",
	"drum": "drums", "break": "drums", "breakbeat": "drums", "top": "drums", "fill": "drums",
	"808":  "bass/808",
	"bass": "bass", "sub": "bass", "reese": "bass",
	"vox": "vocals", "vocal": "vocals", "acapella": "vocals", "chant": "vocals", "adlib": "vocals",
	"fx": "fx", "sfx": "fx",
	"riser": "fx/riser", "uplifter": "fx/riser", "downlifter": "fx/downlifter",
	"impact": "fx/impact", "sweep": "fx/sweep", "whoosh": "fx/whoosh", "foley": "fx/foley",
	"texture": "fx/texture", "atmos": "fx/atmosphere", "atmosphere": "fx/atmosphere", "noise": "fx/noise",
	"synth": "synth", "lead": "synth/lead", "pluck": "synth/pluck", "arp": "synth/arp",
	"pad": "synth/pad", "chord": "synth/chord", "stab": "synth/stab",
	"piano": "keys/piano", "keys": "keys", "rhodes": "keys/rhodes", "organ": "keys/organ",
	"guitar": "guitar", "gtr": "guitar",
	"strings": "orchestral/strings", "violin": "orchestral/strings", "cello": "orchestral/strings",
	"brass": "orchestral/brass", "horn": "orchestral/brass", "sax": "orchestral/sax", "flute": "orchestral/flute",
	"wet": "processing/wet", "dry": "processing/dry",
}

var loopWords = map[string]bool{"loop": true}

var oneShotWords = map[string]bool{"oneshot": true, "shot": true, "hit": true, "stab": true}

// loopishWords suggest tempo-locked material without stating a type; a
// trailing number after one of them is more likely a tempo than an index.
var loopishWords = map[string]bool{
	"loop": true, "top": true, "break": true, "breakbeat": true, "fill": true,
	"groove": true, "beat": true, "riff": true, "melody": true, "phrase": true,
}

// oneShotDrumTags are the drum tags that name a single hit rather than a kit
// or a groove.
var oneShotDrumTags = map[string]bool{
	"drums/kick": true, "drums/snare": true, "drums/clap": true, "drums/snap": true,
	"drums/hihat": true, "drums/cymbal": true, "drums/tom": true, "drums/percussion": true,
}

// lookupWord resolves a lower-cased word against a keyword set, also trying
// the singular of a simple plural.
func lookupWord[V any](m map[string]V, w string) (V, bool) {
	if v, ok := m[w]; ok {
		return v, true
	}
	if len(w) > 3 && strings.HasSuffix(w, "s") {
		if v, ok := m[w[:len(w)-1]]; ok {
			return v, true
		}
	}
	var zero V
	return zero, false
}

// words splits tokens further for keyword matching only: camelCase and
// letter/digit boundaries ("HiHat" -> hi, hat; "808Bass" -> 808, bass). The
// unsplit token is kept too so "hihat" and "oneshot" still match.
func words(tokens []string) []string {
	var out []string
	for _, t := range tokens {
		parts := splitCamel(t)
		if len(parts) > 1 {
			out = append(out, strings.ToLower(t))
		}
		for _, p := range parts {
			out = append(out, strings.ToLower(p))
		}
	}
	return out
}

func splitCamel(s string) []string {
	rs := []rune(s)
	var parts []string
	start := 0
	for i := 1; i < len(rs); i++ {
		prev, cur := rs[i-1], rs[i]
		boundary := false
		switch {
		case unicode.IsDigit(prev) != unicode.IsDigit(cur) && (unicode.IsLetter(prev) || unicode.IsLetter(cur)):
			boundary = true
		case unicode.IsLower(prev) && unicode.IsUpper(cur):
			boundary = true
		case unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(rs) && unicode.IsLower(rs[i+1]):
			boundary = true // "FXLoop" -> FX, Loop
		}
		if boundary {
			parts = append(parts, string(rs[start:i]))
			start = i
		}
	}
	return append(parts, string(rs[start:]))
}

// wordInfo is what the keyword table says about one path component.
type wordInfo struct {
	tags    []string
	loop    bool
	oneShot bool
	loopish bool
}

func scanWords(tokens []string) wordInfo {
	var info wordInfo
	for _, w := range words(tokens) {
		if tag, ok := lookupWord(keywordTags, w); ok {
			info.tags = appendUnique(info.tags, tag)
		}
		if _, ok := lookupWord(loopWords, w); ok {
			info.loop = true
		}
		if _, ok := lookupWord(oneShotWords, w); ok {
			info.oneShot = true
		}
		if _, ok := lookupWord(loopishWords, w); ok {
			info.loopish = true
		}
	}
	return info
}

func (w wordInfo) typeHint() string {
	switch {
	case w.loop && !w.oneShot:
		return TypeLoop
	case w.oneShot && !w.loop:
		return TypeOneShot
	}
	return ""
}

func (w wordInfo) hasPrefix(prefix string) bool {
	for _, t := range w.tags {
		if t == prefix || strings.HasPrefix(t, prefix+"/") {
			return true
		}
	}
	return false
}

func (w wordInfo) oneShotDrum() bool {
	for _, t := range w.tags {
		if oneShotDrumTags[t] {
			return true
		}
	}
	return false
}

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}

// pruneParents drops a tag when a more specific descendant is present
// ("drums" next to "drums/kick").
func pruneParents(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		covered := false
		for _, o := range tags {
			if strings.HasPrefix(o, t+"/") {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, t)
		}
	}
	return out
}
