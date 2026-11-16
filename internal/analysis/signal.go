package analysis

import "math"

const (
	tempoRate = 22050
	keyRate   = 11025
	// maxAnalysisSeconds bounds the work on long files; the opening of a
	// recording says enough about its tempo and key.
	maxAnalysisSeconds = 45
	// silenceRatio is -60 dB, relative to the file's own peak so quietly
	// mastered files trim the same way as loud ones.
	silenceRatio = 0.001
)

// signal is the decoded audio prepared for analysis: measured once at full
// length, then cut to maxAnalysisSeconds and reduced to about tempoRate.
type signal struct {
	x          []float64
	rate       int
	peak       float64 // of the whole file
	dur        float64 // whole file, seconds
	trimmedDur float64 // without leading and trailing silence
	truncated  bool    // x does not reach the end of the file
}

func newSignal(mono []float32, rate int) signal {
	s := signal{dur: float64(len(mono)) / float64(rate)}
	var peak float32
	for _, v := range mono {
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	s.peak = float64(peak)
	if peak == 0 || math.IsNaN(s.peak) || math.IsInf(s.peak, 0) {
		s.peak = 0
		return s
	}

	thr := peak * silenceRatio
	start, end := 0, len(mono)
	for start < end && abs32(mono[start]) < thr {
		start++
	}
	for end > start && abs32(mono[end-1]) < thr {
		end--
	}
	s.trimmedDur = float64(end-start) / float64(rate)

	if limit := maxAnalysisSeconds * rate; len(mono) > limit {
		mono, s.truncated = mono[:limit], true
	}
	s.x, s.rate = reduce(toFloat64(mono), rate, tempoRate)
	return s
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func (s signal) envelope() onsetEnvelope { return newOnsetEnvelope(s.x, s.rate, s.peak) }

func (s signal) shape() amplitudeShape { return newAmplitudeShape(s.x, s.rate, s.peak, s.truncated) }

// forKey returns the audio reduced further for key detection.
func (s signal) forKey() ([]float64, int) { return reduce(s.x, s.rate, keyRate) }
