package analysis

import (
	"math"
	"math/rand/v2"
	"runtime"
	"sync"
	"testing"
)

// parallelMap runs f for 0..n-1 on all CPUs and returns the results in
// order. Synthesising the test corpora is far slower than analysing them.
func parallelMap[T any](n int, f func(i int) T) []T {
	out := make([]T, n)
	var wg sync.WaitGroup
	next := make(chan int)
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for i := range next {
				out[i] = f(i)
			}
		})
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
	return out
}

// sin is math.Sin to about 1e-9, which is all test audio needs. math.Sin
// reads its coefficients from package-level tables, and under the race
// detector those reads, made from every CPU at once by parallelMap, fight
// over the same shadow memory: synthesising the corpora took four times as
// long as everything else together. This one touches no shared memory.
func sin(x float64) float64 {
	const (
		twoPi = 2 * math.Pi
		c3    = -1.0 / 6
		c5    = 1.0 / 120
		c7    = -1.0 / 5040
		c9    = 1.0 / 362880
		c11   = -1.0 / 39916800
		c13   = 1.0 / 6227020800
	)
	x -= twoPi * math.Round(x/twoPi) // now in [-pi, pi]
	if x > math.Pi/2 {
		x = math.Pi - x
	} else if x < -math.Pi/2 {
		x = -math.Pi - x
	}
	x2 := x * x
	return x * (1 + x2*(c3+x2*(c5+x2*(c7+x2*(c9+x2*(c11+x2*c13))))))
}

func TestSynthSine(t *testing.T) {
	for x := -40.0; x < 40; x += 0.0137 {
		if d := math.Abs(sin(x) - math.Sin(x)); d > 2e-9 {
			t.Fatalf("sin(%g) is off by %g", x, d)
		}
	}
}

// Test audio is synthesised here: drum hits, notes and the loops built from
// them. Everything is deterministic.

const testRate = 44100

type synth struct {
	x    []float64
	rate int
	rng  *rand.Rand
}

func newSynth(seconds float64, rate int, seed uint64) *synth {
	return &synth{
		x:    make([]float64, int(math.Round(seconds*float64(rate)))),
		rate: rate,
		rng:  rand.New(rand.NewPCG(seed, 0x5eed)),
	}
}

func (s *synth) add(at float64, n int, f func(t float64) float64) {
	start := int(math.Round(at * float64(s.rate)))
	for i := 0; i < n && start+i < len(s.x); i++ {
		if start+i >= 0 {
			s.x[start+i] += f(float64(i) / float64(s.rate))
		}
	}
}

func (s *synth) kick(at, amp float64) {
	s.add(at, s.rate*3/10, func(t float64) float64 {
		phase := 2 * math.Pi * (48*t + 90*(1-math.Exp(-t*35))/35)
		return amp * sin(phase) * math.Exp(-t*16)
	})
}

func (s *synth) noise(at, amp, decay float64, seconds float64) {
	prev := 0.0
	s.add(at, int(seconds*float64(s.rate)), func(t float64) float64 {
		v := s.rng.Float64()*2 - 1
		hp := v - prev // crude high-pass
		prev = v
		return amp * hp * math.Exp(-t*decay)
	})
}

func (s *synth) hat(at, amp float64) { s.noise(at, amp, 90, 0.06) }

func (s *synth) snare(at, amp float64) {
	s.noise(at, amp*0.6, 28, 0.18)
	// The drum's body: a short tone that sags in pitch as it decays.
	s.add(at, s.rate/8, func(t float64) float64 {
		phase := 2 * math.Pi * (165*t + 60*(1-math.Exp(-t*25))/25)
		return amp * 0.5 * sin(phase) * math.Exp(-t*40)
	})
}

// note adds a band-limited saw-like tone with a short attack and release.
func (s *synth) note(at, seconds float64, midi int, amp float64) {
	f := 440 * math.Pow(2, float64(midi-69)/12)
	s.add(at, int(seconds*float64(s.rate)), func(t float64) float64 {
		env := math.Min(1, t/0.005) * math.Min(1, (seconds-t)/0.02)
		v := 0.0
		for h := 1; h <= 6 && f*float64(h) < 8000; h++ {
			v += sin(2*math.Pi*f*float64(h)*t) / float64(h)
		}
		return amp * env * v
	})
}

// pluck is a note that decays, for arps and one-shots.
func (s *synth) pluck(at, seconds float64, midi int, amp, decay float64) {
	f := 440 * math.Pow(2, float64(midi-69)/12)
	s.add(at, int(seconds*float64(s.rate)), func(t float64) float64 {
		v := 0.0
		for h := 1; h <= 5; h++ {
			v += sin(2*math.Pi*f*float64(h)*t) / float64(h)
		}
		return amp * math.Min(1, t/0.003) * math.Exp(-t*decay) * v
	})
}

func (s *synth) out() []float32 {
	out := make([]float32, len(s.x))
	for i, v := range s.x {
		out[i] = float32(v)
	}
	return out
}

type drumPattern int

const (
	fourOnFloor drumPattern = iota // kick every beat, hats on the off-beat eighths
	backbeat                       // kick 1 and 3, snare 2 and 4, eighth hats
	breakbeat                      // syncopated kicks, snare 2 and 4, sixteenth hats
)

// drums writes a pattern over the given number of beats starting at offset
// seconds. swing delays every second subdivision by that fraction of a
// subdivision.
func (s *synth) drums(p drumPattern, bpm float64, beats int, offset, swing float64) {
	beat := 60 / bpm
	for b := range beats {
		t := offset + float64(b)*beat
		inBar := b % 4
		switch p {
		case fourOnFloor:
			s.kick(t, 0.8)
			s.hat(t+beat/2*(1+swing), 0.25)
		case backbeat:
			if inBar%2 == 0 {
				s.kick(t, 0.8)
			} else {
				s.snare(t, 0.6)
			}
			s.hat(t, 0.2)
			s.hat(t+beat/2*(1+swing), 0.2)
		case breakbeat:
			switch inBar {
			case 0:
				s.kick(t, 0.8)
				s.kick(t+beat*0.75, 0.6)
			case 1, 3:
				s.snare(t, 0.6)
			case 2:
				s.kick(t+beat/2, 0.7)
			}
			for i := range 4 {
				d := 0.0
				if i%2 == 1 {
					d = swing * beat / 4
				}
				s.hat(t+float64(i)*beat/4+d, 0.15+0.1*float64((i+1)%2))
			}
		}
	}
}

// Chord progressions as scale-degree roots with the triad quality implied
// by the mode, one chord per bar.
var (
	majorProgressions = [][]int{{0, 9, 5, 7}, {0, 5, 7, 0}, {0, 7, 9, 5}, {0, 5, 0, 7}} // I vi IV V, ...
	minorProgressions = [][]int{{0, 8, 3, 10}, {0, 5, 7, 0}, {0, 10, 8, 7}, {0, 3, 5, 0}}
)

var (
	majorScale = [7]int{0, 2, 4, 5, 7, 9, 11}
	minorScale = [7]int{0, 2, 3, 5, 7, 8, 10}
)

// triad returns the diatonic triad on a root given as semitones above the
// tonic.
func triad(root int, minor bool) [3]int {
	scale := majorScale
	if minor {
		scale = minorScale
	}
	degree := 0
	for i, s := range scale {
		if s == root {
			degree = i
		}
	}
	var out [3]int
	for i := range out {
		step := degree + 2*i
		out[i] = scale[step%7] + 12*(step/7)
	}
	return out
}

type harmonyStyle int

const (
	chords   harmonyStyle = iota // sustained triads with a bass note
	arpeggio                     // eighth-note broken chords
	bassline                     // root-fifth bass with scale passing notes
)

// harmony writes a progression in the given key over whole bars.
func (s *synth) harmony(style harmonyStyle, tonic int, minor bool, progression []int, bpm float64, bars int, offset float64) {
	beat := 60 / bpm
	for bar := range bars {
		root := progression[bar%len(progression)]
		tri := triad(root, minor)
		t := offset + float64(bar)*4*beat
		switch style {
		case chords:
			for _, iv := range tri {
				s.note(t, 4*beat, 60+tonic+iv, 0.07)
			}
			s.note(t, 4*beat, 36+tonic+root, 0.12)
		case arpeggio:
			for i := range 8 {
				iv := tri[i%3] + 12*(i/3%2)
				s.pluck(t+float64(i)*beat/2, beat/2, 60+tonic+iv, 0.12, 6)
			}
		case bassline:
			seq := []int{tri[0], tri[0], tri[2], tri[0], tri[1], tri[0], tri[2], tri[0] + 12}
			for i, iv := range seq {
				s.pluck(t+float64(i)*beat/2, beat/2, 36+tonic+iv, 0.25, 5)
			}
		}
	}
}

func loopSeconds(bpm float64, beats int) float64 { return float64(beats) * 60 / bpm }

// drumLoop is an exactly bar-cut drum loop.
func drumLoop(p drumPattern, bpm float64, bars, rate int, swing float64) []float32 {
	s := newSynth(loopSeconds(bpm, bars*4), rate, uint64(bpm)*31+uint64(bars))
	s.drums(p, bpm, bars*4, 0, swing)
	return s.out()
}

// tonalLoop is an exactly bar-cut loop in a key, optionally with drums.
func tonalLoop(style harmonyStyle, tonic int, minor bool, progression []int, bpm float64, bars int, withDrums bool) []float32 {
	s := newSynth(loopSeconds(bpm, bars*4), testRate, uint64(tonic)*7+uint64(bars))
	s.harmony(style, tonic, minor, progression, bpm, bars, 0)
	if withDrums {
		s.drums(backbeat, bpm, bars*4, 0, 0)
	}
	return s.out()
}
