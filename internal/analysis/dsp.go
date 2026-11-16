package analysis

import (
	"math"
	"sync"
)

// fftPlan is an in-place radix-2 FFT for one power-of-two size, with a
// matching Hann window.
type fftPlan struct {
	n        int
	rev      []int
	cos, sin []float64
	window   []float64
}

func newFFTPlan(n int) *fftPlan {
	p := &fftPlan{n: n, rev: make([]int, n), cos: make([]float64, n/2), sin: make([]float64, n/2), window: make([]float64, n)}
	bits := 0
	for 1<<bits < n {
		bits++
	}
	for i := range p.rev {
		r := 0
		for b := 0; b < bits; b++ {
			if i&(1<<b) != 0 {
				r |= 1 << (bits - 1 - b)
			}
		}
		p.rev[i] = r
	}
	for i := range p.cos {
		a := -2 * math.Pi * float64(i) / float64(n)
		p.cos[i], p.sin[i] = math.Cos(a), math.Sin(a)
	}
	for i := range p.window {
		p.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))
	}
	return p
}

// transform computes the forward FFT of re+i*im in place.
//
// It is exempt from the race detector. It touches only the caller's two
// buffers and the plan's tables, which never change once built, so there is
// nothing here for the detector to find; but a plan is shared by every
// analysis worker, and instrumenting millions of reads of the same tables
// from all of them at once makes them fight over the detector's shadow
// memory. Race-enabled test runs took minutes for that alone.
//
//go:norace
func (p *fftPlan) transform(re, im []float64) {
	n := p.n
	for i, j := range p.rev {
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half, step := size>>1, n/size
		for start := 0; start < n; start += size {
			for k := 0; k < half; k++ {
				wr, wi := p.cos[k*step], p.sin[k*step]
				a, b := start+k, start+k+half
				tr := wr*re[b] - wi*im[b]
				ti := wr*im[b] + wi*re[b]
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
			}
		}
	}
}

const (
	onsetFFT = 1024
	onsetHop = 256
	keyFFT   = 8192
)

var (
	onsetPlan = sync.OnceValue(func() *fftPlan { return newFFTPlan(onsetFFT) })
	keyPlan   = sync.OnceValue(func() *fftPlan { return newFFTPlan(keyFFT) })
)

// halve low-passes with a short half-band filter and drops every other
// sample.
func halve(x []float64) []float64 {
	out := make([]float64, len(x)/2)
	at := func(i int) float64 {
		if i < 0 || i >= len(x) {
			return 0
		}
		return x[i]
	}
	for i := range out {
		c := 2 * i
		if c >= 3 && c+3 < len(x) {
			out[i] = (16*x[c] + 9*(x[c-1]+x[c+1]) - (x[c-3] + x[c+3])) / 32
		} else {
			out[i] = (16*at(c) + 9*(at(c-1)+at(c+1)) - (at(c-3) + at(c+3))) / 32
		}
	}
	return out
}

// reduce halves the sample rate until it is as close to target as whole
// octaves allow without dropping more than 10% below it.
func reduce(x []float64, rate, target int) ([]float64, int) {
	for rate/2 >= target*9/10 {
		x, rate = halve(x), rate/2
	}
	return x, rate
}

func toFloat64(x []float32) []float64 {
	out := make([]float64, len(x))
	for i, v := range x {
		out[i] = float64(v)
	}
	return out
}

func rms(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range x {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(x)))
}
