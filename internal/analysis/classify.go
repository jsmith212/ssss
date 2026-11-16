package analysis

// Features are the measurements Classify weighs. Analyze fills them in.
type Features struct {
	Dur float64 // seconds, untrimmed
	// ExactBars is set when the length is a whole number of beats at a
	// whole-number tempo that the rhythm repeats at (TempoEstimate.ExactFit).
	ExactBars      bool
	Onsets         int
	Periodicity    float64 // TempoEstimate.Periodicity
	TailDB         float64 // RMS of the last 50 ms relative to peak
	MonotonicDecay bool
}

const (
	loopThreshold = 2
	// minLoopPeriodicity and minLoopOnsets are what the audio must show to
	// be called a loop without a name to say so: several attacks, repeating
	// at a steady pace.
	minLoopPeriodicity = 0.3
	minLoopOnsets      = 4
)

// Classify decides between loop and one-shot. Each piece of evidence moves a
// score; TypeLoop needs loopThreshold points, and it needs a reason: either
// the name (or embedded metadata) says loop or states a tempo, or the audio
// has a rhythm. Length and level alone do not make a loop. A sustained note,
// a riser or a reverse cymbal rendered at a round length is as long as some
// whole number of beats and does not fade out either.
func Classify(p Parsed, f Features) (typ string, score int) {
	named := false
	switch tempo := p.BPM > 0 && !p.BPMWeak; {
	case p.TypeHint == TypeLoop || (tempo && !p.BPMFromFolder):
		score += 2
		named = true
	case tempo:
		// A tempo on the folder covers the kicks in it too.
		score++
		named = true
	}
	if f.Dur >= 1.5 && f.ExactBars {
		score++
	}
	if f.Onsets >= minLoopOnsets {
		score++
	}
	if f.Periodicity >= 0.5 && f.Onsets >= minLoopOnsets {
		score++
	}
	if f.TailDB > -40 {
		score++
	}
	if f.Dur < 1.0 || p.TypeHint == TypeOneShot {
		score -= 2
	}
	if f.Onsets == 1 && f.MonotonicDecay {
		score--
	}
	rhythmic := f.Onsets >= minLoopOnsets && f.Periodicity >= minLoopPeriodicity
	if score >= loopThreshold && (named || rhythmic) {
		return TypeLoop, score
	}
	return TypeOneShot, score
}
