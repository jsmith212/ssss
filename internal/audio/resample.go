package audio

import "math"

const (
	// resampleZeros is the number of sinc zero crossings kept on each side
	// of the kernel centre.
	resampleZeros = 12
	// resamplePhases caps the polyphase table. Ratios that reduce to at
	// most this many phases (every pairing of the standard rates) are
	// exact; others use the nearest phase.
	resamplePhases = 512
)

// Resample converts a mono signal from one sample rate to another with a
// Hann-windowed sinc polyphase filter (12 zero crossings a side). When
// downsampling, the cutoff sits at 92% of the new Nyquist frequency, so
// content above it is attenuated rather than aliased. It is meant for
// analysis input, where it is comfortably transparent, and not for playback.
// The input is returned as is when the rates are equal.
func Resample(in []float32, from, to int) []float32 {
	if from == to || from <= 0 || to <= 0 || len(in) == 0 {
		return in
	}
	g := gcd(from, to)
	up, down := to/g, from/g // output n sits at input position n*down/up

	scale := min(1, float64(to)/float64(from))
	cutoff := 0.92 * scale // as a fraction of the input Nyquist
	half := int(math.Ceil(resampleZeros / scale))
	taps := 2 * half

	phases := min(up, resamplePhases)
	table := make([]float32, phases*taps)
	for p := range phases {
		frac := float64(p) / float64(phases)
		row := table[p*taps : (p+1)*taps]
		var sum float64
		for k := range row {
			// Tap k weighs input sample i0 - half + 1 + k.
			t := float64(k-half+1) - frac
			w := 0.5 + 0.5*math.Cos(math.Pi*t/float64(half))
			if math.Abs(t) >= float64(half) {
				w = 0
			}
			v := cutoff * sinc(cutoff*t) * w
			row[k] = float32(v)
			sum += v
		}
		// Unity gain at DC for every phase, so levels survive exactly.
		for k := range row {
			row[k] = float32(float64(row[k]) / sum)
		}
	}

	n := int((int64(len(in))*int64(up) + int64(down) - 1) / int64(down))
	out := make([]float32, n)
	for i := range out {
		num := int64(i) * int64(down)
		i0 := int(num / int64(up))
		rem := int(num % int64(up))
		phase := rem
		if phases != up {
			phase = int(math.Round(float64(rem) / float64(up) * float64(phases)))
			if phase == phases {
				phase = 0
				i0++
			}
		}
		row := table[phase*taps : (phase+1)*taps]
		first := i0 - half + 1
		lo, hi := max(0, -first), min(taps, len(in)-first)
		var acc float32
		for k := lo; k < hi; k++ {
			acc += in[first+k] * row[k]
		}
		out[i] = acc
	}
	return out
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	return math.Sin(math.Pi*x) / (math.Pi * x)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
