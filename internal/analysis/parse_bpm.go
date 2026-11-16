package analysis

import (
	"regexp"
	"strconv"
)

var (
	bpmJoined  = regexp.MustCompile(`(?i)^(?:(\d{2,3})bpm|bpm(\d{2,3}))$`)
	bareNumber = regexp.MustCompile(`^\d{2,3}$`)
	// decimalNumber is a tempo like "133.33". It only counts with a "bpm"
	// marker beside it.
	decimalNumber = regexp.MustCompile(`^\d{2,3}\.\d{1,3}$`)
)

// A number followed by one of these is a measurement, not a tempo.
var unitWords = map[string]bool{
	"hz": true, "khz": true, "ms": true, "bit": true, "k": true, "db": true,
	"bar": true, "bars": true, "st": true, "sec": true, "s": true, "ct": true, "cents": true,
	"rpm": true, "percent": true, "pct": true,
}

// A number preceded by one of these is a counter, not a tempo.
var counterWords = map[string]bool{
	"vol": true, "volume": true, "pt": true, "part": true, "kit": true, "v": true,
	"ver": true, "version": true, "take": true, "no": true, "nr": true, "cd": true,
	"disc": true, "track": true, "preset": true, "patch": true,
	// Multisampled instruments: velocity layers, MIDI notes, round robins.
	"vel": true, "velocity": true, "note": true, "midi": true, "rr": true,
}

const (
	bareBPMMin, bareBPMMax         = 60, 200
	explicitBPMMin, explicitBPMMax = 30, 300
)

type numCand struct {
	span
	value    float64
	explicit bool // carries a "bpm" marker
	weak     bool // a bare number nothing in the name vouches for
	digits   int
}

// numbers lists every token that could be a tempo.
func numbers(nt nameTokens, lower []string, isFile bool) []numCand {
	tokens := nt.text
	isNumber := func(t string) bool { return bareNumber.MatchString(t) || decimalNumber.MatchString(t) }
	var out []numCand
	for i, t := range tokens {
		if m := bpmJoined.FindStringSubmatch(t); m != nil {
			if v, ok := numberInRange(m[1]+m[2], explicitBPMMin, explicitBPMMax); ok {
				out = append(out, numCand{span: span{i, i}, value: v, explicit: true, digits: len(m[1] + m[2])})
			}
			continue
		}
		if !isNumber(t) {
			continue
		}
		next, prev := "", ""
		if i+1 < len(tokens) {
			next = lower[i+1]
		}
		if i > 0 {
			prev = lower[i-1]
		}
		switch {
		case next == "bpm":
			if v, ok := numberInRange(t, explicitBPMMin, explicitBPMMax); ok {
				out = append(out, numCand{span: span{i, i + 1}, value: v, explicit: true, digits: len(t)})
			}
		case prev == "bpm" && (i < 2 || !isNumber(tokens[i-2])):
			if v, ok := numberInRange(t, explicitBPMMin, explicitBPMMax); ok {
				out = append(out, numCand{span: span{i - 1, i}, value: v, explicit: true, digits: len(t)})
			}
		case !bareNumber.MatchString(t):
		case unitWords[next] || counterWords[prev]:
		case !isFile && prev == "top":
			// "Top 100 EDM Kicks" is a chart, not a tempo.
		case modelNumber(nt, lower, i):
		default:
			if v, ok := numberInRange(t, bareBPMMin, bareBPMMax); ok {
				out = append(out, numCand{span: span{i, i}, value: v, digits: len(t)})
			}
		}
	}
	return out
}

// modelNumber reports whether the number at token i is the tail of a product
// name: glued by a hyphen to a word that is not musical vocabulary, in a name
// that separates its words some other way ("Juno-106 Pad", "SH-101 Bass",
// "CR-78 Snare"). Range alone does not keep these out; only the TR-808 and
// its kin are too big to be tempos.
func modelNumber(nt nameTokens, lower []string, i int) bool {
	if i == 0 || nt.glue[i] != "-" || !nt.hyphenJoins() {
		return false
	}
	return isOrdinaryWord(lower[i-1])
}

func numberInRange(s string, lo, hi float64) (float64, bool) {
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n < lo || n > hi {
		return 0, false
	}
	return n, true
}

// pickBPM prefers an explicit "bpm" marker, then a number sitting next to a
// key, then the last plausible bare number, three digits before two. A bare
// number that only its position speaks for comes last and is marked weak.
func pickBPM(nums []numCand, keys []keyCand, info wordInfo, c component, tokenCount int) (numCand, bool) {
	best, bestScore := numCand{}, -1
	for _, n := range nums {
		score := 0
		switch {
		case n.explicit:
			score = 10
		case nextToKey(n, keys):
			score = 5
		case !plausibleBare(n, info, c.isFile, tokenCount):
			continue
		default:
			n.weak = !vouchedFor(info, c)
			if !n.weak {
				score += 2
			}
			if n.digits == 3 {
				score++
			}
		}
		if score >= bestScore {
			best, bestScore = n, score
		}
	}
	return best, bestScore >= 0
}

// nextToKey reports whether a key sits beside the number, as in
// "Bass_120_Am". A note with an octave ("Rhodes_F3_100") is the pitch of a
// multisample and says nothing about the number after it.
func nextToKey(n numCand, keys []keyCand) bool {
	for _, k := range keys {
		if !k.note && k.adjacent(n.span) {
			return true
		}
	}
	return false
}

// vouchedFor reports whether the words around a bare number make it a tempo:
// the name, or for a file a folder above it, speaks of loops. Names of single
// drum hits are the exception: "Snare_Top_64" and "Kick 120" in a loops
// folder are counted hits unless the file itself says "loop".
func vouchedFor(info wordInfo, c component) bool {
	if info.oneShotDrum() && !info.loop {
		return false
	}
	return info.loopish || (c.isFile && c.loopFolder)
}

// plausibleBare weeds out bare numbers that are more likely an index.
func plausibleBare(n numCand, info wordInfo, isFile bool, tokenCount int) bool {
	if !isFile {
		// Folder names carry all sorts of numbers; two digits need more
		// evidence than position.
		return n.digits == 3
	}
	if tokenCount == 1 {
		return false
	}
	if n.end != tokenCount-1 {
		return true
	}
	// Trailing numbers are usually indexes ("Kick_64", "Snare_101").
	if n.digits == 2 {
		return info.loopish
	}
	return info.loopish || !info.oneShotDrum()
}
