package analysis

import "math"

const (
	minBPM, maxBPM = 60.0, 200.0

	// tempoCentre is the middle of the octave that half/double decisions
	// are settled into, about 89 to 178 BPM: 174 stays 174 and 70 becomes
	// 140, provided the rhythm supports both readings.
	tempoCentre = 126.0
	// snapBPM is how close a result must be to a whole number to be
	// rounded; produced loops are almost always at integer tempos.
	snapBPM = 0.3
	// supportBeats is how many multiples of the beat must repeat.
	supportBeats = 4
	// beatRatio is how much of the pulse's support its half or double must
	// keep to be considered as the beat.
	beatRatio = 0.75
	// barCutRatio is how close a bar-length candidate must be to the
	// nearest peak of the support curve to be believed.
	barCutRatio = 0.85
	// plausibleRatio is the share of the best support below which a
	// bar-length candidate is ruled out altogether.
	plausibleRatio = 0.4
	// minPeriodicity is the periodicity below which the onset envelope
	// says nothing useful about tempo (pads, drones, single hits).
	minPeriodicity = 0.2
	// exactFitBPM is how close to an integer a bar candidate computed from
	// the untrimmed length must be to count as an exact export. Tight on
	// purpose: with several beat counts in range, a random length lands
	// within 0.5% of some whole tempo more often than not.
	exactFitBPM = 0.01
)

// barBeats are the loop lengths, in beats, worth considering: whole bars of
// 4/4 plus the common 3/4 and 6/8 lengths.
var barBeats = [...]float64{1, 2, 3, 4, 6, 8, 12, 16, 24, 32, 48, 64, 96, 128, 192, 256}

// TempoEstimate is the outcome of tempo detection.
type TempoEstimate struct {
	BPM        float64 // 0 = no tempo found
	Confidence float64 // 0..1
	// BarCut is set when the tempo follows from the sample being an exact
	// number of beats long, which makes it precise to the sample.
	BarCut bool
	// ExactFit is set when, on top of BarCut, the length is a whole number
	// of beats at a whole-number tempo, as loops exported from a DAW are,
	// and the rhythm repeats at that tempo. The length alone proves little:
	// most round durations fit some whole tempo.
	ExactFit bool
	// Candidates are the tempos at which the sample would be a whole
	// number of beats long, less those the rhythm plainly rules out. Merge
	// uses them to vet a tempo stated in the name.
	Candidates []float64
	// Periodicity is the strength of the strongest beat-like repetition in
	// the onset envelope: about 0 for unrhythmic material, 1 for a steady
	// pulse.
	Periodicity float64
}

type barCandidate struct {
	bpm   float64
	beats float64
	exact bool // from the untrimmed length and within exactFitBPM of an integer
}

// barCandidates lists the tempos at which a sample of the given lengths
// (untrimmed first) spans a whole number of beats.
func barCandidates(durations ...float64) []barCandidate {
	var out []barCandidate
	for i, dur := range durations {
		if dur <= 0 {
			continue
		}
	beats:
		for _, b := range barBeats {
			bpm := b * 60 / dur
			if bpm < minBPM || bpm > maxBPM {
				continue
			}
			for _, c := range out {
				if math.Abs(c.bpm-bpm) < 0.002*bpm {
					continue beats
				}
			}
			out = append(out, barCandidate{
				bpm:   bpm,
				beats: b,
				exact: i == 0 && math.Abs(bpm-math.Round(bpm)) < exactFitBPM,
			})
		}
	}
	return out
}

func isPowerOfTwo(v float64) bool {
	_, frac := math.Modf(math.Log2(v))
	return frac == 0
}

// periodicity scores tempos against the autocorrelation of an onset
// envelope.
type periodicity struct {
	acf    []float64
	fps    float64
	usable float64 // lags beyond this have too little overlap to trust
}

func newPeriodicity(env onsetEnvelope) periodicity {
	n := len(env.flux)
	// 8 s covers eight beats at 60 BPM, the most the refinement uses.
	acf := env.autocorrelation(int(8 * env.fps))
	return periodicity{acf: acf, fps: env.fps, usable: math.Min(float64(len(acf)-1), 0.8*float64(n))}
}

// at reads the autocorrelation at a fractional lag (Catmull-Rom).
func (p periodicity) at(lag float64) float64 {
	if lag < 1 || lag > p.usable {
		return 0
	}
	i := int(lag)
	if i+2 >= len(p.acf) {
		return p.acf[min(i, len(p.acf)-1)]
	}
	t := lag - float64(i)
	y0, y1, y2, y3 := p.acf[i-1], p.acf[i], p.acf[i+1], p.acf[i+2]
	return y1 + 0.5*t*(y2-y0+t*(2*y0-5*y1+4*y2-y3+t*(3*(y1-y2)+y3-y0)))
}

func (p periodicity) lag(bpm float64) float64 { return p.fps * 60 / bpm }

// comb averages the autocorrelation over the first multiples of a lag, as
// many as the envelope is long enough for.
func (p periodicity) comb(lag float64, multiples int) float64 {
	sum, n := 0.0, 0
	for k := 1; k <= multiples && float64(k)*lag <= p.usable; k++ {
		sum += p.at(float64(k) * lag)
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// support is how well the envelope repeats at a tempo and the next three
// multiples of its beat: about 1 for a steady pulse, about 0 for none.
func (p periodicity) support(bpm float64) float64 { return p.comb(p.lag(bpm), supportBeats) }

// localPeak is the best support within a few percent of a tempo.
func (p periodicity) localPeak(bpm float64) float64 {
	best := math.Inf(-1)
	for b := bpm / 1.04; b <= bpm*1.04; b *= 1.004 {
		best = math.Max(best, p.support(b))
	}
	return best
}

// refine sharpens a tempo by lining up as many beat multiples as the
// envelope allows; later multiples divide the lag error down.
func (p periodicity) refine(bpm float64) float64 {
	centre := p.lag(bpm)
	bestLag, bestSum := centre, math.Inf(-1)
	for l := centre / 1.03; l <= centre*1.03; l += 0.01 {
		if sum := p.comb(l, 8); sum > bestSum {
			bestLag, bestSum = l, sum
		}
	}
	return p.fps * 60 / bestLag
}

// tempoChoice is one tempo under consideration.
type tempoChoice struct {
	bpm     float64
	support float64
	bonus   float64 // multiplier for candidates with something extra going for them
	// exact marks a tempo that makes the sample a whole number of beats
	// long at a whole-number tempo.
	exact bool
}

// choose settles on a tempo among rivals in two steps. The best-supported
// choice fixes the pulse. A rhythm repeats just as well at half or double
// that speed, and which of those is "the" beat cannot be heard from the
// onsets alone, so among the octaves of the pulse that are still well
// supported the one nearest tempoCentre wins. An exact octave is preferred
// to an inexact one first: a loop cut to four beats at 185 is not a loop at
// 92.5.
func choose(choices []tempoChoice) (best tempoChoice, confidence float64) {
	var pulse tempoChoice
	for _, c := range choices {
		if c.support*c.bonus > pulse.support*pulse.bonus {
			pulse = c
		}
	}
	if pulse.support <= 0 {
		return tempoChoice{}, 0
	}

	bestDist, octaveRival, otherRival := math.Inf(1), math.Inf(1), 0.0
	for _, c := range choices {
		if !isOctaveOf(c.bpm, pulse.bpm) {
			otherRival = math.Max(otherRival, c.support)
			continue
		}
		if c.support < beatRatio*pulse.support {
			continue
		}
		d := centreDistance(c.bpm)
		if !c.exact {
			d += inexactPenalty
		}
		switch {
		case d < bestDist:
			octaveRival = bestDist
			best, bestDist = c, d
		case d < octaveRival:
			octaveRival = d
		}
	}
	// An octave that lost only for being inexact was never a real rival.
	confidence = math.Min(1, best.support) *
		(1 - 0.5*math.Min(1, otherRival/pulse.support)) *
		(0.5 + 0.5*math.Min(1, octaveRival-bestDist))
	return best, confidence
}

// inexactPenalty, in octaves, puts every inexact tempo behind every exact
// one when octaves of a pulse are ranked by distance from tempoCentre. The
// octaves on offer span at most four.
const inexactPenalty = 8

// centreDistance is how far, in octaves, a tempo lies from tempoCentre.
func centreDistance(bpm float64) float64 { return math.Abs(math.Log2(bpm / tempoCentre)) }

// isOctaveOf reports whether a is b counted at the same, half, quarter,
// double or quadruple speed.
func isOctaveOf(a, b float64) bool {
	for _, m := range [...]float64{0.25, 0.5, 1, 2, 4} {
		if math.Abs(a-b*m) <= 0.015*b*m {
			return true
		}
	}
	return false
}

// free finds the beat without any assumption about the sample length, from
// the peaks of the support curve. It also returns the highest support seen.
func (p periodicity) free() (best tempoChoice, confidence, peak float64) {
	if len(p.acf) == 0 {
		return tempoChoice{}, 0, 0
	}
	var grid []tempoChoice
	for b := minBPM; b <= maxBPM; b *= 1.003 {
		s := p.support(b)
		grid = append(grid, tempoChoice{bpm: b, support: s, bonus: 1})
		peak = math.Max(peak, s)
	}
	var peaks []tempoChoice
	for i := 1; i+1 < len(grid); i++ {
		if grid[i].support > grid[i-1].support && grid[i].support >= grid[i+1].support {
			peaks = append(peaks, grid[i])
		}
	}
	best, confidence = choose(peaks)
	return best, confidence, peak
}

func snap(bpm float64) float64 {
	if r := math.Round(bpm); math.Abs(bpm-r) <= snapBPM {
		return r
	}
	return bpm
}

// estimateTempo picks a tempo for a loop. Loops exported from a DAW are an
// exact number of beats long, so the tempos implied by the length are tried
// first, keeping those the onsets actually repeat at; only when none of
// them explains the rhythm is the unconstrained estimate used.
func estimateTempo(env onsetEnvelope, dur, trimmedDur float64) TempoEstimate {
	cands := barCandidates(dur, trimmedDur)
	p := newPeriodicity(env)
	free, freeConfidence, peak := p.free()
	est := TempoEstimate{Periodicity: math.Max(0, peak)}
	for _, c := range cands {
		// Without a rhythm to check against, every candidate stays.
		if est.Periodicity < minPeriodicity || p.support(c.bpm) >= plausibleRatio*peak {
			est.Candidates = append(est.Candidates, c.bpm)
		}
	}

	if est.Periodicity < minPeriodicity {
		// Nothing rhythmic to go on. An exact whole-number fit is still
		// decent evidence for a sustained loop.
		best, bestDist := 0.0, math.Inf(1)
		for _, c := range cands {
			d := centreDistance(c.bpm)
			if !isPowerOfTwo(c.beats) {
				d += 2 // whole bars of 4/4 first
			}
			if c.exact && d < bestDist {
				best, bestDist = c.bpm, d
			}
		}
		if best > 0 {
			est.BPM, est.BarCut, est.Confidence = math.Round(best), true, 0.2
		}
		return est
	}

	var fitting []tempoChoice
	for _, c := range cands {
		s := p.support(c.bpm)
		// A candidate on the slope of a peak rather than on it is a
		// coincidence of the length, not the tempo.
		if s < beatRatio*peak || s < barCutRatio*p.localPeak(c.bpm) {
			continue
		}
		bonus := 1.0
		if isPowerOfTwo(c.beats) {
			bonus *= 1.15
		}
		if c.exact {
			bonus *= 1.1
		}
		fitting = append(fitting, tempoChoice{c.bpm, s, bonus, c.exact})
	}
	if best, confidence := choose(fitting); best.bpm > 0 {
		bpm := best.bpm
		if math.Abs(bpm-math.Round(bpm)) > snapBPM {
			// Not a whole number: the length is probably padded (MP3) or
			// cut loosely. Trust the rhythm if it points to one.
			if r := snap(p.refine(bpm)); r == math.Round(r) && math.Abs(r-bpm) < 0.015*bpm {
				bpm = r
			}
		}
		est.BPM, est.BarCut, est.ExactFit, est.Confidence = snap(bpm), true, best.exact, confidence
		if !best.exact {
			est.Confidence *= 0.8
		}
		return est
	}

	if free.bpm > 0 {
		est.BPM = snap(p.refine(free.bpm))
		est.Confidence = 0.6 * freeConfidence
	}
	return est
}

// DetectTempo estimates the tempo of a mono loop. It does not judge whether
// the audio is a loop at all; Analyze does that first.
func DetectTempo(mono []float32, rate int) TempoEstimate {
	if len(mono) == 0 || rate <= 0 {
		return TempoEstimate{}
	}
	s := newSignal(mono, rate)
	if s.peak == 0 {
		return TempoEstimate{}
	}
	return estimateTempo(s.envelope(), s.dur, s.trimmedDur)
}
