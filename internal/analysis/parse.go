package analysis

import (
	"path"
	"regexp"
	"strings"
)

// Parsed is what a sample's path says about it.
type Parsed struct {
	BPM float64 // 0 = none found
	// BPMWeak marks a tempo read from a bare number with nothing in the
	// name to say it is one: no "bpm", no loop word, no key beside it
	// ("Rhodes_F3_100", "Blink 182 Guitar"). It is a guess for the audio to
	// confirm; on its own it neither makes a sample a loop nor gets stored.
	BPMWeak bool
	Key     string // library notation, "" = none found
	// KeyWeak marks a key read from a chord name ("Gmin7"), a note with an
	// octave ("C3") or a lone trailing letter ("Loop_E"). It is a hint to
	// fall back on, not something to trust over a detected key.
	KeyWeak bool
	// BPMFromFolder and KeyFromFolder are set when the value came from a
	// parent folder rather than the file name.
	BPMFromFolder bool
	KeyFromFolder bool
	TypeHint      string   // TypeLoop, TypeOneShot or ""
	SuggestedTags []string // most specific first: file name, then folders
}

// Drum reports whether the name marks the sample as unpitched percussion.
func (p Parsed) Drum() bool {
	for _, t := range p.SuggestedTags {
		if t == "drums" || strings.HasPrefix(t, "drums/") {
			return true
		}
	}
	return false
}

// tokenPattern matches one token: a decimal number ("133.33"), kept whole
// although "." separates tokens everywhere else, or a run of anything but
// separators.
var tokenPattern = regexp.MustCompile(`\d+\.\d+|[^\s_\-.,+&()\[\]{}]+`)

var audioExts = map[string]bool{
	".wav": true, ".wave": true, ".aif": true, ".aiff": true, ".aifc": true,
	".flac": true, ".mp3": true, ".ogg": true,
}

// ParseName extracts tempo, key, type hint and tag suggestions from a
// slash-separated path. The file name is authoritative; parent folders fill
// in whatever it leaves open.
func ParseName(relPath string) Parsed {
	relPath = strings.ReplaceAll(relPath, `\`, "/")
	parts := strings.FieldsFunc(relPath, func(r rune) bool { return r == '/' })
	if len(parts) == 0 {
		return Parsed{}
	}
	name := parts[len(parts)-1]
	if ext := path.Ext(name); audioExts[strings.ToLower(ext)] {
		name = strings.TrimSuffix(name, ext)
	}

	fileTokens := tokenize(name)
	fileWords := scanWords(fileTokens.text)

	var folderTokens []nameTokens // deepest first
	var folderWords []wordInfo
	loopFolder := false
	for i := len(parts) - 2; i >= 0; i-- {
		t := tokenize(parts[i])
		w := scanWords(t.text)
		folderTokens = append(folderTokens, t)
		folderWords = append(folderWords, w)
		loopFolder = loopFolder || w.loop
	}

	var p Parsed
	p.SuggestedTags = append(p.SuggestedTags, fileWords.tags...)
	p.TypeHint = fileWords.typeHint()
	for _, w := range folderWords {
		for _, t := range w.tags {
			p.SuggestedTags = appendUnique(p.SuggestedTags, t)
		}
		// A file name that mentions both words has spoken; do not let a
		// folder break the tie.
		if p.TypeHint == "" && !fileWords.loop && !fileWords.oneShot {
			p.TypeHint = w.typeHint()
		}
	}
	p.SuggestedTags = pruneParents(p.SuggestedTags)

	// The file's own words decide whether it is percussion; folders only
	// count when the file name says nothing ("Drums/Loop_03.wav").
	drum := fileWords.hasPrefix("drums")
	if len(fileWords.tags) == 0 {
		for _, w := range folderWords {
			if w.hasPrefix("drums") {
				drum = true
				break
			}
		}
	}

	file := parseComponent(fileTokens, fileWords, component{isFile: true, drum: drum, loopFolder: loopFolder})
	p.BPM, p.BPMWeak, p.Key, p.KeyWeak = file.bpm, file.bpmWeak, file.key, file.keyWeak
	for i, toks := range folderTokens {
		if p.BPM != 0 && !p.BPMWeak && p.Key != "" {
			break
		}
		c := parseComponent(toks, folderWords[i], component{drum: drum})
		// A folder that plainly states a tempo outranks a guess from the
		// file name; otherwise the file name comes first.
		if c.bpm != 0 && (p.BPM == 0 || (p.BPMWeak && !c.bpmWeak)) {
			p.BPM, p.BPMWeak, p.BPMFromFolder = c.bpm, c.bpmWeak, true
		}
		// A weak file-name key still outranks a folder: folders describe
		// the pack, and "Dm" on a pack says little about one drum hit.
		if p.Key == "" && c.key != "" && !c.keyWeak && !drum {
			p.Key, p.KeyFromFolder = c.key, true
		}
	}
	return p
}

// nameTokens are the words of one path component. glue[i] is the separator
// text in front of text[i], which tells "Juno-106" from "Juno 106".
type nameTokens struct {
	text []string
	glue []string
}

func tokenize(s string) nameTokens {
	var t nameTokens
	end := 0
	for _, m := range tokenPattern.FindAllStringIndex(s, -1) {
		t.text = append(t.text, s[m[0]:m[1]])
		t.glue = append(t.glue, s[end:m[0]])
		end = m[1]
	}
	return t
}

// hyphenJoins reports whether a hyphen in this component joins two parts of
// one name ("SH-101 Bass") rather than being how the name separates all its
// words ("Deep-House-124-Loop").
func (t nameTokens) hyphenJoins() bool {
	for _, g := range t.glue[min(1, len(t.glue)):] {
		if g != "-" {
			return true
		}
	}
	return false
}

type span struct{ start, end int } // token indexes, inclusive

func (a span) adjacent(b span) bool { return a.start == b.end+1 || b.start == a.end+1 }

// component says where a path component sits and what surrounds it.
type component struct {
	isFile     bool
	drum       bool // the sample is unpitched percussion
	loopFolder bool // a parent folder says "loops"
}

type componentResult struct {
	bpm     float64
	bpmWeak bool
	key     string
	keyWeak bool
}

func parseComponent(nt nameTokens, info wordInfo, c component) componentResult {
	tokens := nt.text
	isFile, drum := c.isFile, c.drum
	lower := make([]string, len(tokens))
	for i, t := range tokens {
		lower[i] = strings.ToLower(t)
	}

	var keys []keyCand
	for i := 0; i < len(tokens); i++ {
		k, ok := keyAt(tokens, lower, i)
		if !ok {
			continue
		}
		i = k.end
		// "Kick_120_E": E is a variation, kicks are not in E, and the
		// letter must not vouch for the number beside it either.
		if !(k.bare && drum) {
			keys = append(keys, k)
		}
	}

	var res componentResult
	bpm, haveBPM := pickBPM(numbers(nt, lower, isFile), keys, info, c, len(tokens))
	if haveBPM {
		res.bpm, res.bpmWeak = bpm.value, bpm.weak
	}

	// Rank: 3 = certain, 1 = weak hint, 0 = not a key here.
	best, bestRank := keyCand{}, 0
	for _, k := range keys {
		rank := 0
		switch k.strength {
		case keyExplicit:
			rank = 3
		case keyContextual:
			switch {
			case haveBPM && k.adjacent(bpm.span):
				rank = 3
			case k.bare && isFile && k.end == len(tokens)-1 && len(tokens) > 1:
				rank = 1
				if info.hasPrefix("bass/808") {
					rank = 3 // tuned 808s are named by their note
				}
			}
		case keyChord:
			if !drum {
				rank = 1
			}
		}
		if rank > 0 && rank >= bestRank {
			best, bestRank = k, rank
		}
	}
	if bestRank > 0 {
		res.key, res.keyWeak = best.key, bestRank < 3
	}
	return res
}
