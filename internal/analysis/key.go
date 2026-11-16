package analysis

import "math"

const (
	// Chroma is gathered between roughly B1 and C7. Below that FFT bins
	// are wider than a semitone; above it there is little but overtones.
	chromaMinHz = 60.0
	chromaMaxHz = 2100.0
	c0Hz        = 16.351597831287414

	// fineBins subdivide each semitone so that the tuning offset of the
	// recording can be estimated before folding to twelve pitch classes.
	fineBins = 10

	// minKeySeconds is the shortest audio a pitch can be read from.
	minKeySeconds = 0.25
	// minChromaContrast is the peak-to-mean ratio of the chroma vector
	// below which the material is treated as unpitched.
	minChromaContrast = 1.6
	// minRootContrast is the same ratio required before a single root note
	// is looked for.
	minRootContrast = 2.5
	// minKeyCorrelation and minKeyMargin gate a detected key: how well the
	// best profile must fit, and by how much it must beat the runner-up.
	minKeyCorrelation = 0.65
	minKeyMargin      = 0.02
	// openingSeconds is how much of the start of a loop is read for the
	// note it opens on and minOpeningContrast how clearly that note must
	// stand out. openingSlack and relativeSlack are how much worse than
	// the best key a key on that note may fit and still be preferred.
	openingSeconds     = 0.3
	minOpeningContrast = 2.0
	openingSlack       = 0.1
	relativeSlack      = 0.3
)

// Key profiles after Temperley (Kostka-Payne corpus): the expected weight
// of each scale degree, tonic first.
var (
	majorProfile = [12]float64{0.748, 0.060, 0.488, 0.082, 0.670, 0.460, 0.096, 0.715, 0.104, 0.366, 0.057, 0.400}
	minorProfile = [12]float64{0.712, 0.084, 0.474, 0.618, 0.049, 0.460, 0.105, 0.747, 0.404, 0.067, 0.133, 0.330}
)

// KeyEstimate is the outcome of key detection.
type KeyEstimate struct {
	Key        string  // library notation, "" = unpitched or undecided
	Confidence float64 // 0..1
	// RootOnly is set when only the root note was determined and Key is
	// that note without a mode (written like a major key).
	RootOnly bool
	// Contrast is the peak-to-mean ratio of the chroma vector: about 1 for
	// noise and drums, 3 and above for clearly pitched material.
	Contrast float64
}

// pitchHistogram collects spectral peak energy by pitch, fineBins to the
// semitone, folded into one octave (C = 0).
type pitchHistogram [12 * fineBins]float64

// chromaBuilder turns audio frames into pitch histograms.
type chromaBuilder struct {
	plan     *fftPlan
	re, im   []float64
	mag      []float64
	lo, hi   int
	hzPerBin float64
}

// newChromaBuilder returns a builder that reads spectral peaks from minHz
// up to chromaMaxHz.
func newChromaBuilder(rate int, minHz float64) *chromaBuilder {
	hzPerBin := float64(rate) / keyFFT
	return &chromaBuilder{
		plan:     keyPlan(),
		re:       make([]float64, keyFFT),
		im:       make([]float64, keyFFT),
		mag:      make([]float64, keyFFT/2),
		lo:       max(2, int(math.Ceil(minHz/hzPerBin))),
		hi:       min(keyFFT/2-2, int(chromaMaxHz/hzPerBin)),
		hzPerBin: hzPerBin,
	}
}

// add analyses one frame of up to keyFFT samples and adds its spectral
// peaks to hist, which is indexed by pitch above C0 in steps of 1/fineBins
// of a semitone; a histogram shorter than the pitch range wraps around,
// which folds octaves together when its length is one octave. Shorter
// frames are zero-padded with the window stretched over what audio there is.
func (b *chromaBuilder) add(seg []float64, hist []float64) {
	n := len(seg)
	clear(b.re)
	clear(b.im)
	if n == keyFFT {
		for i, v := range seg {
			b.re[i] = v * b.plan.window[i]
		}
	} else {
		for i, v := range seg {
			b.re[i] = v * (0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n)))
		}
	}
	b.plan.transform(b.re, b.im)
	top, total := 0.0, 0.0
	for k := b.lo - 1; k <= b.hi+1; k++ {
		b.mag[k] = math.Sqrt(b.re[k]*b.re[k] + b.im[k]*b.im[k])
		top = math.Max(top, b.mag[k])
		total += b.mag[k]
	}
	if top == 0 {
		return
	}
	// Only spectral peaks count: partials stand out as local maxima while
	// drums and noise smear across bins. Dividing by the root of the frame
	// level keeps loud hits from drowning the harmony.
	floor, norm := 0.05*top, math.Sqrt(total)
	for k := b.lo; k <= b.hi; k++ {
		m := b.mag[k]
		if m < floor || m <= b.mag[k-1] || m < b.mag[k+1] {
			continue
		}
		// Parabolic interpolation of the peak position.
		lo, mid, hi := math.Log(b.mag[k-1]+1e-12), math.Log(m), math.Log(b.mag[k+1]+1e-12)
		offset := 0.0
		if d := lo - 2*mid + hi; d < 0 {
			offset = 0.5 * (lo - hi) / d
		}
		pitch := 12 * math.Log2((float64(k)+offset)*b.hzPerBin/c0Hz)
		hist[int(math.Round(pitch*fineBins))%len(hist)] += m / norm
	}
}

// addAll analyses x in overlapping frames of the given length, at most
// keyFFT, or as one short frame.
func (b *chromaBuilder) addAll(x []float64, hist []float64, frame int) {
	if len(x) < frame {
		b.add(x, hist)
		return
	}
	for start := 0; start+frame <= len(x); start += frame / 4 {
		b.add(x[start:start+frame], hist)
	}
}

// tuning is the circular mean of every peak's deviation from the nearest
// equal-tempered semitone, in semitones (-0.5..0.5).
func (h *pitchHistogram) tuning() (offset float64, ok bool) {
	var sx, sy float64
	for i, w := range h {
		ang := 2 * math.Pi * float64(i%fineBins) / fineBins
		sx += w * math.Cos(ang)
		sy += w * math.Sin(ang)
	}
	if sx == 0 && sy == 0 {
		return 0, false
	}
	return math.Atan2(sy, sx) / (2 * math.Pi), true
}

// fold reduces the histogram to twelve pitch classes for a given tuning.
func (h *pitchHistogram) fold(tuning float64) (chroma [12]float64) {
	for i, w := range h {
		if w == 0 {
			continue
		}
		pitch := float64(i)/fineBins - tuning
		pc := math.Round(pitch)
		// Peaks between two semitones count for less.
		c := math.Cos(math.Pi * (pitch - pc))
		chroma[((int(pc)%12)+12)%12] += w * c * c
	}
	return chroma
}

// chromagram folds the spectral peaks of x into twelve pitch classes
// (C = 0), corrected for the recording's tuning offset. opening is the same
// for just the first moment of the audio.
func chromagram(x []float64, rate int) (chroma, opening [12]float64, ok bool) {
	if float64(len(x)) < minKeySeconds*float64(rate) {
		return chroma, opening, false
	}
	b := newChromaBuilder(rate, chromaMinHz)
	var whole, head pitchHistogram
	b.addAll(x, whole[:], keyFFT)
	tuning, ok := whole.tuning()
	if !ok {
		return chroma, opening, false
	}
	b.add(x[:min(len(x), int(openingSeconds*float64(rate)))], head[:])
	return whole.fold(tuning), head.fold(tuning), true
}

func chromaContrast(chroma [12]float64) (contrast float64, strongest int) {
	sum := 0.0
	for i, v := range chroma {
		sum += v
		if v > chroma[strongest] {
			strongest = i
		}
	}
	if sum == 0 {
		return 0, 0
	}
	return chroma[strongest] / (sum / 12), strongest
}

// correlate is the Pearson correlation of the chroma vector with a key
// profile rotated to the given tonic.
func correlate(chroma, profile [12]float64, tonic int) float64 {
	var mc, mp float64
	for i := range 12 {
		mc += chroma[i]
		mp += profile[i]
	}
	mc /= 12
	mp /= 12
	var num, dc, dp float64
	for i := range 12 {
		c := chroma[(i+tonic)%12] - mc
		p := profile[i] - mp
		num += c * p
		dc += c * c
		dp += p * p
	}
	if dc == 0 || dp == 0 {
		return 0
	}
	return num / math.Sqrt(dc*dp)
}

// estimateKey reads a key from audio already reduced to about keyRate. A
// one-shot is read as notes: a single note gives its root, a plain triad its
// key, anything else nothing. A loop is matched against the key profiles,
// and one that fits none of them but sits on a single note gets that root.
func estimateKey(x []float64, rate int, oneShot bool) KeyEstimate {
	if oneShot {
		return estimateNotes(x, rate, true)
	}
	est := estimateLoopKey(x, rate)
	if est.Key == "" && est.Contrast >= minRootContrast {
		if root := estimateNotes(x, rate, false); root.RootOnly {
			est.Key, est.Confidence, est.RootOnly = root.Key, root.Confidence, true
		}
	}
	return est
}

func estimateLoopKey(x []float64, rate int) KeyEstimate {
	chroma, opening, ok := chromagram(x, rate)
	if !ok {
		return KeyEstimate{}
	}
	contrast, _ := chromaContrast(chroma)
	est := KeyEstimate{Contrast: contrast}
	if contrast < minChromaContrast {
		return est
	}

	fit := func(tonic int, minor bool) float64 {
		if minor {
			return correlate(chroma, minorProfile, tonic)
		}
		return correlate(chroma, majorProfile, tonic)
	}
	best, second := math.Inf(-1), math.Inf(-1)
	tonic, minor := 0, false
	for t := range 12 {
		for _, m := range []bool{false, true} {
			switch r := fit(t, m); {
			case r > best:
				second = best
				best, tonic, minor = r, t, m
			case r > second:
				second = r
			}
		}
	}
	if best < minKeyCorrelation {
		return est
	}

	// Loops overwhelmingly start on their tonic, so a key on the opening
	// note is preferred when it fits about as well as the best one. It
	// gets far more leeway against its own relative: a minor key and its
	// relative major use the same notes and the profiles separate them
	// poorly.
	margin := best - second
	if c, opened := chromaContrast(opening); c >= minOpeningContrast {
		openedMinor := fit(opened, true) > fit(opened, false)
		slack := openingSlack
		if openedMinor != minor && opened == relativeTonic(tonic, minor) {
			slack = relativeSlack
		}
		if fit(opened, openedMinor) >= best-slack {
			tonic, minor = opened, openedMinor
			margin = math.Max(margin, minKeyMargin)
		}
	}
	if margin < minKeyMargin {
		return est
	}
	est.Key = formatKey(tonic, minor)
	est.Confidence = math.Min(1, best) * (0.5 + 0.5*math.Min(1, margin/0.1))
	return est
}

func relativeTonic(tonic int, minor bool) int {
	if minor {
		return (tonic + 3) % 12
	}
	return (tonic + 9) % 12
}

// DetectKey estimates the musical key of mono audio. oneShot selects
// root-note detection for single hits.
func DetectKey(mono []float32, rate int, oneShot bool) KeyEstimate {
	if len(mono) == 0 || rate <= 0 {
		return KeyEstimate{}
	}
	s := newSignal(mono, rate)
	if s.peak == 0 {
		return KeyEstimate{}
	}
	x, r := s.forKey()
	return estimateKey(x, r, oneShot)
}
