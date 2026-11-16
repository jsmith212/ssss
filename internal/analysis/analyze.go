package analysis

import "math"

// Hints carry metadata embedded in the file itself (WAV acid/smpl chunks,
// tags). They are trusted like the file name and fill in whatever the name
// leaves open.
type Hints struct {
	BPM  float64 // 0 = none
	Key  string  // any spelling NormalizeKey accepts, "" = none
	Type string  // TypeLoop, TypeOneShot or ""
	// Dur is the length of the whole file in seconds when the audio handed
	// to Analyze is only its beginning; 0 means the audio is all of it.
	Dur float64
}

// truncatedSlack is how much longer than the audio Hints.Dur must be before
// the audio counts as a cut-off beginning; container lengths are not exact.
const truncatedSlack = 0.05

// Result is everything analysis learned about one sample. The detected
// values are independent of the name, so the two can be compared; Merge
// turns a Result into the values to store.
type Result struct {
	// Parsed is what the path (and Hints) say.
	Parsed Parsed

	Dur        float64 // seconds, of the whole file
	TrimmedDur float64 // without leading and trailing silence
	// Truncated is set when only the beginning of the file was analysed
	// (Hints.Dur): the trimmed length then ends where the audio was cut.
	Truncated bool

	// Type is the classification, which does take the name into account.
	Type      string
	TypeScore int
	Features  Features

	BPM     float64 // detected, 0 = none; only loops get one
	BPMConf float64
	// BarBPMs are the tempos at which the sample is a whole number of
	// beats long.
	BarBPMs []float64

	Key         string // detected, "" = none
	KeyConf     float64
	KeyRootOnly bool // Key is a one-shot's root note, mode unknown
}

// Analyze runs the whole pipeline on decoded mono audio. relPath is the
// sample's slash-separated path inside its pack. It never fails: whatever
// cannot be determined is left zero.
func Analyze(mono []float32, rate int, relPath string, hints Hints) Result {
	r := Result{Parsed: applyHints(ParseName(relPath), hints)}
	if len(mono) == 0 || rate <= 0 {
		return r
	}
	s := newSignal(mono, rate)
	r.Dur, r.TrimmedDur = s.dur, s.trimmedDur
	// Where the audio stops is only where the file stops when all of it
	// was passed in. For a cut-off beginning the cut says nothing: its
	// length is not the sample's, and its last 50 ms are not a tail.
	if hints.Dur > s.dur+truncatedSlack {
		r.Dur, r.Truncated = hints.Dur, true
	}
	if s.peak == 0 {
		return r
	}

	env := s.envelope()
	shape := s.shape()
	dur, trimmed := s.dur, s.trimmedDur
	if r.Truncated {
		dur, trimmed = r.Dur, 0
		shape.tailDB, shape.monotonicDecay = math.Inf(-1), false
	}
	tempo := estimateTempo(env, dur, trimmed)
	r.BarBPMs = tempo.Candidates
	r.Features = Features{
		Dur:            r.Dur,
		ExactBars:      tempo.ExactFit,
		Onsets:         env.onsets(),
		Periodicity:    tempo.Periodicity,
		TailDB:         shape.tailDB,
		MonotonicDecay: shape.monotonicDecay,
	}
	r.Type, r.TypeScore = Classify(r.Parsed, r.Features)
	if r.Type == TypeLoop {
		r.BPM, r.BPMConf = tempo.BPM, tempo.Confidence
	}

	if !r.Parsed.Drum() {
		x, rate := s.forKey()
		key := estimateKey(x, rate, r.Type == TypeOneShot)
		r.Key, r.KeyConf, r.KeyRootOnly = key.Key, key.Confidence, key.RootOnly
	}
	return r
}

func applyHints(p Parsed, h Hints) Parsed {
	if (p.BPM == 0 || p.BPMWeak) && h.BPM > 0 {
		p.BPM, p.BPMWeak, p.BPMFromFolder = h.BPM, false, false
	}
	if p.Key == "" || p.KeyWeak {
		if key, ok := NormalizeKey(h.Key); ok {
			p.Key, p.KeyWeak, p.KeyFromFolder = key, false, false
		}
	}
	if p.TypeHint == "" && (h.Type == TypeLoop || h.Type == TypeOneShot) {
		p.TypeHint = h.Type
	}
	return p
}
