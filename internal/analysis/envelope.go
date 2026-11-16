package analysis

import "math"

// minOnsetFlux is the least spectral flux that counts as an attack. For
// scale: a kick or plucked note from silence measures 40 to 200, the ripple
// of a slowly swelling pad or noise riser under 10.
const minOnsetFlux = 12.0

// onsetEnvelope is the log-magnitude spectral flux of x: how much energy
// appears between consecutive short frames, with slow level changes removed.
type onsetEnvelope struct {
	flux []float64
	fps  float64
}

func newOnsetEnvelope(x []float64, rate int, peak float64) onsetEnvelope {
	env := onsetEnvelope{fps: float64(rate) / onsetHop}
	if peak == 0 || len(x) < onsetHop {
		return env
	}
	plan := onsetPlan()
	const bins = onsetFFT/2 + 1
	re := make([]float64, onsetFFT)
	im := make([]float64, onsetFFT)
	prev := make([]float64, bins)
	cur := make([]float64, bins)
	// A full-scale sine reaches n/4 through a Hann window; scale so that
	// maps to ~log(1+100).
	gain := 100 / (peak * onsetFFT / 4)

	// Frames are centred on t*hop, so the attack at sample 0 of a loop
	// lands in frame 0 and is measured against silence.
	frames := len(x)/onsetHop + 1
	raw := make([]float64, frames)
	for t := range frames {
		off := t*onsetHop - onsetFFT/2
		for i := range re {
			if j := off + i; j >= 0 && j < len(x) {
				re[i] = x[j] * plan.window[i]
			} else {
				re[i] = 0
			}
			im[i] = 0
		}
		plan.transform(re, im)
		sum := 0.0
		for k := range bins {
			cur[k] = math.Log1p(gain * math.Sqrt(re[k]*re[k]+im[k]*im[k]))
			if d := cur[k] - prev[k]; d > 0 {
				sum += d
			}
		}
		raw[t] = sum
		prev, cur = cur, prev
	}

	// Subtract a moving average and rectify, leaving only the peaks.
	const radius = 8
	env.flux = make([]float64, frames)
	strongest := 0.0
	window, count := 0.0, 0
	for i := 0; i < min(radius, frames); i++ {
		window += raw[i]
		count++
	}
	// A frame whose window runs past the end of the audio sees the cut
	// itself: a sustained sound that simply stops splatters across the
	// spectrum there, which is the end of the file and not an attack.
	whole := frames
	for whole > 0 && (whole-1)*onsetHop+onsetFFT/2 > len(x) {
		whole--
	}
	for t := range whole {
		if hi := t + radius; hi < frames {
			window += raw[hi]
			count++
		}
		if lo := t - radius - 1; lo >= 0 {
			window -= raw[lo]
			count--
		}
		if v := raw[t] - window/float64(count); v > 0 {
			env.flux[t] = v
			strongest = math.Max(strongest, v)
		}
	}
	if strongest < minOnsetFlux {
		// Swells, drones and noise beds ripple without ever attacking;
		// normalised, those ripples would pass for a rhythm.
		env.flux = nil
	}
	return env
}

// onsets counts distinct attacks: local maxima of the envelope that reach a
// fraction of the strongest one and sit at least 60 ms apart.
func (e onsetEnvelope) onsets() int {
	maxFlux := 0.0
	for _, v := range e.flux {
		maxFlux = math.Max(maxFlux, v)
	}
	if maxFlux == 0 {
		return 0
	}
	thr := math.Max(0.15*maxFlux, minOnsetFlux)
	gap := max(1, int(math.Round(0.06*e.fps)))
	count, last := 0, -gap
	for t, v := range e.flux {
		if v < thr || t-last < gap {
			continue
		}
		if (t > 0 && e.flux[t-1] > v) || (t+1 < len(e.flux) && e.flux[t+1] >= v) {
			continue
		}
		count++
		last = t
	}
	return count
}

// autocorrelation returns the unbiased, mean-removed autocorrelation of the
// envelope normalised to 1 at lag 0, for lags up to maxLag.
func (e onsetEnvelope) autocorrelation(maxLag int) []float64 {
	n := len(e.flux)
	maxLag = min(maxLag, n-1)
	if maxLag < 1 {
		return nil
	}
	mean := 0.0
	for _, v := range e.flux {
		mean += v
	}
	mean /= float64(n)
	y := make([]float64, n)
	energy := 0.0
	for i, v := range e.flux {
		y[i] = v - mean
		energy += y[i] * y[i]
	}
	if energy == 0 {
		return nil
	}
	energy /= float64(n)
	acf := make([]float64, maxLag+1)
	for lag := range acf {
		sum := 0.0
		a, b := y[:n-lag], y[lag:]
		for i, v := range a {
			sum += v * b[i]
		}
		acf[lag] = sum / float64(n-lag) / energy
	}
	return acf
}

// amplitudeShape describes the level contour of a sample.
type amplitudeShape struct {
	tailDB         float64 // RMS of the last 50 ms relative to peak, dB
	monotonicDecay bool    // level only falls after its maximum
}

func newAmplitudeShape(x []float64, rate int, peak float64, truncated bool) amplitudeShape {
	shape := amplitudeShape{tailDB: math.Inf(-1)}
	if peak == 0 || len(x) == 0 {
		return shape
	}
	tail := min(len(x), rate/20)
	if r := rms(x[len(x)-tail:]); r > 0 {
		shape.tailDB = 20 * math.Log10(r/peak)
	}
	if truncated {
		// The end of the audio was not looked at, so nothing can be said
		// about how it ends.
		shape.tailDB = math.Inf(-1)
		return shape
	}

	block := max(1, rate/50) // 20 ms
	var levels []float64
	for i := 0; i+block <= len(x); i += block {
		levels = append(levels, rms(x[i:i+block]))
	}
	if len(levels) == 0 {
		return shape
	}
	top := 0
	for i, v := range levels {
		if v > levels[top] {
			top = i
		}
	}
	// A rise of more than ~3.5 dB over the lowest level seen since the
	// maximum is a second event, unless it happens down in the noise.
	floor := 0.02 * levels[top]
	low := levels[top]
	shape.monotonicDecay = true
	for _, v := range levels[top+1:] {
		if v > 1.5*low && v > floor {
			shape.monotonicDecay = false
			break
		}
		low = math.Min(low, v)
	}
	return shape
}
