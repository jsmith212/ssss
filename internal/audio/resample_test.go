package audio

import (
	"math"
	"testing"
)

func sine32(freq float64, rate, n int, amp float64) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(amp * math.Sin(2*math.Pi*freq*float64(i)/float64(rate)))
	}
	return out
}

func TestResample(t *testing.T) {
	tests := []struct {
		name     string
		from, to int
		freq     float64
		wantAmp  float64 // of a 0.5 amplitude input
		tol      float64
	}{
		{"halve passband", 44100, 22050, 1000, 0.5, 0.005},
		{"quarter passband", 44100, 11025, 440, 0.5, 0.005},
		{"quarter near cutoff", 44100, 11025, 4000, 0.5, 0.01},
		{"48k to 22.05k", 48000, 22050, 3000, 0.5, 0.005},
		{"96k to 11.025k", 96000, 11025, 1000, 0.5, 0.005},
		{"upsample", 22050, 44100, 2000, 0.5, 0.005},
		{"inexact ratio", 44101, 22050, 1000, 0.5, 0.005},
		// Above the new Nyquist: must be removed, not folded back.
		{"halve stopband", 44100, 22050, 15000, 0, 0.003},
		{"quarter stopband", 44100, 11025, 7000, 0, 0.003},
		{"48k stopband", 48000, 22050, 13000, 0, 0.003},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := sine32(tt.freq, tt.from, tt.from, 0.5) // one second
			out := Resample(in, tt.from, tt.to)
			if d := len(out) - tt.to; d < -1 || d > 1 {
				t.Fatalf("len = %d, want %d", len(out), tt.to)
			}
			// Skip the filter's edge transients.
			mid := out[len(out)/8 : len(out)*7/8]
			if tt.wantAmp > 0 {
				if got := toneAmp(mid, tt.to, tt.freq); math.Abs(got-tt.wantAmp) > tt.tol {
					t.Errorf("amplitude at %.0f Hz = %.4f, want %.4f", tt.freq, got, tt.wantAmp)
				}
				if got := peakOf(mid); got > tt.wantAmp+2*tt.tol {
					t.Errorf("peak = %.4f", got)
				}
			} else if got := peakOf(mid); got > tt.tol {
				t.Errorf("residual peak = %.5f (%.1f dB), want below %.4f", got, 20*math.Log10(got/0.5), tt.tol)
			}
		})
	}
}

func TestResampleEdges(t *testing.T) {
	in := []float32{1, 2, 3}
	if out := Resample(in, 44100, 44100); &out[0] != &in[0] {
		t.Error("equal rates copied the input")
	}
	if out := Resample(nil, 44100, 22050); len(out) != 0 {
		t.Errorf("empty input gave %d samples", len(out))
	}
	if out := Resample(in, 0, 22050); len(out) != 3 {
		t.Errorf("invalid rate gave %d samples", len(out))
	}
	if out := Resample([]float32{1}, 44100, 11025); len(out) != 1 {
		t.Errorf("single sample gave %d samples", len(out))
	}

	// DC survives exactly away from the ends.
	dc := make([]float32, 4000)
	for i := range dc {
		dc[i] = 0.25
	}
	out := Resample(dc, 48000, 22050)
	for i, v := range out[200 : len(out)-200] {
		if math.Abs(float64(v)-0.25) > 1e-5 {
			t.Fatalf("out[%d] = %v, want 0.25", i+200, v)
		}
	}
}

func BenchmarkResample(b *testing.B) {
	in := sine32(440, 44100, 44100*10, 0.5)
	b.SetBytes(int64(len(in) * 4))
	for b.Loop() {
		Resample(in, 44100, 11025)
	}
}
