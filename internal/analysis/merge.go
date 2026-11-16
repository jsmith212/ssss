package analysis

import "math"

// Sources of a stored value, strongest first.
const (
	SourceUser     = "user"
	SourceSplice   = "splice"
	SourceFilename = "filename"
	SourceDetected = "detected"
)

// Meta is the stored type, tempo and key of a sample with where each came
// from. The zero value means nothing is known yet.
type Meta struct {
	Type    string
	TypeSrc string
	BPM     float64
	BPMSrc  string
	Key     string
	KeySrc  string
}

// Merged is the outcome of Merge.
type Merged struct {
	Meta
	// NeedsReview is set when the stored tempo comes from the name or from
	// Splice and the audio disagrees with it.
	NeedsReview bool
}

// Merge combines the values already stored for a sample with a fresh
// analysis. A value is only replaced by one from an equal or stronger
// source: user edits always stand, Splice values stand while they are set,
// and everything derived from the name or the audio is recomputed.
func Merge(existing Meta, r Result) Merged {
	out := Merged{Meta: existing}

	if !keeps(existing.TypeSrc, existing.Type != "") {
		out.Type, out.TypeSrc = r.Type, ""
		switch {
		case r.Type == "":
		case r.Type == r.Parsed.TypeHint:
			out.TypeSrc = SourceFilename
		default:
			out.TypeSrc = SourceDetected
		}
	}

	if !keeps(existing.BPMSrc, existing.BPM > 0) {
		out.BPM, out.BPMSrc = mergeBPM(r, out.Type)
	}
	if out.BPM > 0 && (out.BPMSrc == SourceFilename || out.BPMSrc == SourceSplice) {
		out.NeedsReview = r.BPM > 0 && !tempoAgrees(out.BPM, r)
	}

	if !keeps(existing.KeySrc, existing.Key != "") {
		out.Key, out.KeySrc = mergeKey(r)
	}
	return out
}

// keeps reports whether a stored value outranks anything analysis can
// produce. A user's choice stands even when it is "unknown".
func keeps(source string, set bool) bool {
	return source == SourceUser || (source == SourceSplice && set)
}

func mergeBPM(r Result, typ string) (float64, string) {
	name := r.Parsed.BPM
	switch {
	case typ == TypeOneShot && (r.Parsed.BPMFromFolder || r.Parsed.BPMWeak):
		// "Trap 140 BPM/Kick_01.wav": the folder's tempo is not the kick's.
		// "CR-78 Snare.wav": nor is every number in a name a tempo.
		name = 0
	case r.Parsed.BPMWeak && !tempoAgrees(name, r):
		// A bare number is only taken for the tempo when the audio bears
		// it out.
		name = 0
	}
	switch {
	case name > 0:
		return name, SourceFilename
	case r.BPM > 0:
		return r.BPM, SourceDetected
	}
	return 0, ""
}

// tempoAgrees reports whether a stated tempo is consistent with the audio:
// it makes the sample a whole number of beats long, or it is the detected
// tempo counted at the same, half or double speed.
func tempoAgrees(bpm float64, r Result) bool {
	for _, c := range r.BarBPMs {
		if within(bpm, c, 0.01) {
			return true
		}
	}
	return within(bpm, r.BPM, 0.02) || within(bpm, 2*r.BPM, 0.02) || within(bpm, r.BPM/2, 0.02)
}

func within(a, b, tolerance float64) bool {
	return b > 0 && math.Abs(a-b) <= tolerance*b
}

func mergeKey(r Result) (string, string) {
	name, detected := r.Parsed.Key, r.Key
	switch {
	case name != "" && !r.Parsed.KeyWeak:
		return name, SourceFilename
	case name != "" && detected == "":
		return name, SourceFilename
	case name != "" && r.KeyRootOnly && sameTonic(name, detected):
		// "Gmin7" plus a detected root of G: the name knows the mode.
		return name, SourceFilename
	case detected != "":
		return detected, SourceDetected
	}
	return "", ""
}

func sameTonic(a, b string) bool {
	pa, _, okA := splitKey(a)
	pb, _, okB := splitKey(b)
	return okA && okB && pa == pb
}
