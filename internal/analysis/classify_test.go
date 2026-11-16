package analysis

import (
	"math"
	"slices"
	"testing"
)

func TestClassifyScore(t *testing.T) {
	loopName := Parsed{TypeHint: TypeLoop}
	shotName := Parsed{TypeHint: TypeOneShot}
	tests := []struct {
		name  string
		p     Parsed
		f     Features
		typ   string
		score int
	}{
		{"short hit", Parsed{}, Features{Dur: 0.4, Onsets: 1, TailDB: -70, MonotonicDecay: true}, TypeOneShot, -3},
		{"long decaying hit", Parsed{}, Features{Dur: 3, Onsets: 1, TailDB: -65, MonotonicDecay: true}, TypeOneShot, -1},
		{"exact rhythmic loop", Parsed{}, Features{Dur: 4, ExactBars: true, Onsets: 16, Periodicity: 0.9, TailDB: -50}, TypeLoop, 3},
		{"loose rhythmic loop", Parsed{}, Features{Dur: 4.3, Onsets: 16, Periodicity: 0.9, TailDB: -50}, TypeLoop, 2},
		{"exact length and no fade alone", Parsed{}, Features{Dur: 8, ExactBars: true, Onsets: 2, TailDB: -12}, TypeOneShot, 2},
		{"exact pad loop by name", loopName, Features{Dur: 8, ExactBars: true, Onsets: 1, TailDB: -12}, TypeLoop, 4},
		{"irregular phrase", Parsed{}, Features{Dur: 2.4, Onsets: 6, Periodicity: 0.15, TailDB: -30}, TypeOneShot, 2},
		{"guessed tempo does not count", Parsed{BPM: 100, BPMWeak: true}, Features{Dur: 3, Onsets: 1, TailDB: -60, MonotonicDecay: true}, TypeOneShot, -1},
		{"guessed tempo, rhythmic audio", Parsed{BPM: 100, BPMWeak: true}, Features{Dur: 4.8, ExactBars: true, Onsets: 16, Periodicity: 0.9, TailDB: -50}, TypeLoop, 3},
		{"guessed folder tempo", Parsed{BPM: 140, BPMFromFolder: true, BPMWeak: true}, Features{Dur: 2, ExactBars: true, Onsets: 2, TailDB: -60}, TypeOneShot, 1},
		{"sustained note cut off", Parsed{}, Features{Dur: 2.3, Onsets: 1, TailDB: -10}, TypeOneShot, 1},
		{"named loop, nothing else", loopName, Features{Dur: 2.5, Onsets: 2, TailDB: -60}, TypeLoop, 2},
		{"named loop but a short hit", loopName, Features{Dur: 0.3, Onsets: 1, TailDB: -60, MonotonicDecay: true}, TypeOneShot, -1},
		{"tempo in the name", Parsed{BPM: 120}, Features{Dur: 2.5, Onsets: 2, TailDB: -60}, TypeLoop, 2},
		{"tempo only on the folder", Parsed{BPM: 140, BPMFromFolder: true}, Features{Dur: 1.4, Onsets: 1, TailDB: -60, MonotonicDecay: true}, TypeOneShot, 0},
		{"named one-shot with busy audio", shotName, Features{Dur: 2, Onsets: 6, Periodicity: 0.3, TailDB: -30}, TypeOneShot, 0},
		{"named one-shot, exact busy loop", shotName, Features{Dur: 4, ExactBars: true, Onsets: 16, Periodicity: 0.9, TailDB: -20}, TypeLoop, 2},
	}
	for _, tc := range tests {
		typ, score := Classify(tc.p, tc.f)
		if typ != tc.typ || score != tc.score {
			t.Errorf("%s: got %s (%d), want %s (%d)", tc.name, typ, score, tc.typ, tc.score)
		}
	}
}

// oneShots are single events of the kinds found in sample packs, from
// clicks to long tails.
func oneShots() map[string][]float32 {
	out := map[string][]float32{}
	add := func(name string, seconds float64, build func(s *synth)) {
		s := newSynth(seconds, testRate, uint64(len(name)))
		build(s)
		out[name] = s.out()
	}
	add("kick", 0.45, func(s *synth) { s.kick(0, 0.9) })
	add("snare", 0.35, func(s *synth) { s.snare(0, 0.8) })
	add("hat", 0.12, func(s *synth) { s.hat(0, 0.6) })
	add("clap with room", 0.9, func(s *synth) {
		for i := range 3 {
			s.noise(float64(i)*0.012, 0.5, 60, 0.05)
		}
		s.noise(0.03, 0.2, 6, 0.85)
	})
	add("crash", 3.2, func(s *synth) { s.noise(0, 0.6, 1.8, 3.2) })
	add("808", 1.8, func(s *synth) {
		s.add(0, int(1.8*testRate), func(t float64) float64 {
			return 0.8 * sin(2*math.Pi*(55*t+40*(1-math.Exp(-t*30))/30)) * math.Exp(-t*2.5)
		})
	})
	add("pluck", 1.2, func(s *synth) { s.pluck(0, 1.2, 57, 0.5, 4) })
	add("piano chord", 2.7, func(s *synth) {
		for _, m := range []int{48, 55, 60, 64} {
			s.pluck(0, 2.7, m, 0.2, 1.6)
		}
	})
	add("bass note", 0.8, func(s *synth) { s.note(0, 0.8, 40, 0.4) })
	add("pad swell", 3.1, func(s *synth) {
		s.add(0, int(3.1*testRate), func(t float64) float64 {
			env := math.Min(1, t/0.8) * math.Min(1, (3.1-t)/1.5)
			return 0.2 * env * (sin(2*math.Pi*220*t) + sin(2*math.Pi*277.2*t) + sin(2*math.Pi*329.6*t))
		})
	})
	add("impact with tail", 4.3, func(s *synth) {
		s.kick(0, 0.9)
		s.noise(0, 0.5, 1.2, 4.3)
	})
	add("hit with silence after", 2.0, func(s *synth) { s.snare(0, 0.8) })
	add("riser", 3.7, func(s *synth) {
		prev := 0.0
		s.add(0, int(3.7*testRate), func(t float64) float64 {
			v := s.rng.Float64()*2 - 1
			hp := v - prev
			prev = v
			return 0.4 * (t / 3.7) * (t / 3.7) * hp
		})
	})
	return out
}

func loops() map[string][]float32 {
	out := map[string][]float32{
		"house 2 bars":     drumLoop(fourOnFloor, 124, 2, testRate, 0),
		"hip hop 1 bar":    drumLoop(backbeat, 90, 1, testRate, 0),
		"dnb 4 bars":       drumLoop(breakbeat, 174, 4, testRate, 0),
		"swung 2 bars":     drumLoop(breakbeat, 100, 2, testRate, 0.33),
		"arp 2 bars":       tonalLoop(arpeggio, 9, true, minorProgressions[1], 128, 2, false),
		"bassline 1 bar":   tonalLoop(bassline, 4, true, minorProgressions[0], 140, 1, false),
		"full loop 4 bars": tonalLoop(chords, 0, false, majorProgressions[0], 110, 4, true),
		"pad 4 bars":       tonalLoop(chords, 5, false, majorProgressions[1], 120, 4, false),
		"48k loop":         drumLoop(backbeat, 128, 2, 48000, 0),
	}
	loose := newSynth(loopSeconds(120, 8)+0.31, testRate, 3)
	loose.drums(backbeat, 120, 9, 0, 0)
	out["loop with a tail"] = loose.out()
	lead := newSynth(0.09+loopSeconds(95, 9)+0.1, testRate, 4)
	lead.drums(breakbeat, 95, 10, 0.09, 0)
	out["loop cut loosely"] = lead.out()
	return out
}

// Audio alone, with a name that says nothing.
func TestClassifyAudio(t *testing.T) {
	check := func(kind string, set map[string][]float32, want string) {
		correct := 0
		for name, x := range set {
			rate := testRate
			if name == "48k loop" {
				rate = 48000
			}
			r := Analyze(x, rate, "untitled.wav", Hints{})
			if r.Type == want {
				correct++
			} else {
				t.Errorf("%s %q: classified %s (score %d, %+v)", kind, name, r.Type, r.TypeScore, r.Features)
			}
		}
		t.Logf("%s: %d/%d classified %s from audio alone", kind, correct, len(set), want)
	}
	check("one-shot", oneShots(), TypeOneShot)
	check("loop", loops(), TypeLoop)
}

// Sounds that are as long as some whole number of beats and do not fade
// out, but have no rhythm: length and level alone must not make a loop, and
// a pitched one keeps its root.
func TestClassifySustainedSoundsAreNotLoops(t *testing.T) {
	sine := func(seconds float64) []float32 {
		s := newSynth(seconds, testRate, 1)
		s.add(0, len(s.x), func(t float64) float64 { return 0.5 * sin(2*math.Pi*440*t) })
		return s.out()
	}
	riser := func(seconds float64) []float32 {
		s := newSynth(seconds, testRate, 9)
		prev := 0.0
		s.add(0, len(s.x), func(t float64) float64 {
			v := s.rng.Float64()*2 - 1
			hp := v - prev
			prev = v
			return 0.4 * (t / seconds) * (t / seconds) * hp
		})
		return s.out()
	}
	reverseCymbal := func(seconds float64) []float32 {
		s := newSynth(seconds, testRate, 5)
		s.noise(0, 0.6, 2.5, seconds)
		x := s.out()
		slices.Reverse(x)
		return x
	}
	phrase := func() []float32 {
		// Six syllables at uneven intervals.
		s := newSynth(2.4, testRate, 3)
		for i, at := range []float64{0.05, 0.42, 0.61, 1.1, 1.52, 1.83} {
			f := 180 + 30*float64(i%3)
			s.add(at, int(0.25*testRate), func(t float64) float64 {
				env := math.Min(1, t/0.03) * math.Min(1, (0.25-t)/0.08)
				return 0.3 * env * (sin(2*math.Pi*f*t) + 0.5*sin(4*math.Pi*f*t) + 0.3*sin(6*math.Pi*f*t))
			})
		}
		return s.out()
	}
	cases := []struct {
		name  string
		audio []float32
		key   string
	}{
		{"sine 1.5 s", sine(1.5), "A"}, {"sine 2 s", sine(2), "A"}, {"sine 3 s", sine(3), "A"},
		{"sine 4 s", sine(4), "A"}, {"sine 6 s", sine(6), "A"}, {"sine 8 s", sine(8), "A"},
		{"riser 8 s", riser(8), ""}, {"reverse cymbal 2 s", reverseCymbal(2), ""},
		{"spoken phrase", phrase(), "*"},
	}
	for _, tc := range cases {
		r := Analyze(tc.audio, testRate, "Pack/Sound_07.wav", Hints{})
		m := Merge(Meta{}, r)
		if m.Type != TypeOneShot || m.BPM != 0 {
			t.Errorf("%s: type=%s bpm=%g (score %d, %+v)", tc.name, m.Type, m.BPM, r.TypeScore, r.Features)
		}
		if tc.key != "*" && m.Key != tc.key {
			t.Errorf("%s: key %q, want %q", tc.name, m.Key, tc.key)
		}
	}
	// The end of a sound that simply stops is not an attack.
	if r := Analyze(sine(2), testRate, "x.wav", Hints{}); r.Features.Onsets != 1 {
		t.Errorf("hard-cut sine: %d onsets, want 1", r.Features.Onsets)
	}
	// Named a loop, the same audio is one, at the tempo its length implies.
	r := Analyze(sine(4), testRate, "Pads/Drone Loop.wav", Hints{})
	if m := Merge(Meta{}, r); m.Type != TypeLoop || m.BPM != 120 {
		t.Errorf("named drone loop: type=%s bpm=%g", m.Type, m.BPM)
	}
}

func TestClassifyUsesName(t *testing.T) {
	shots := oneShots()
	// A long pad swell that happens to end abruptly is a loop if the name
	// says so, and a short kick stays a one-shot whatever the folder says.
	if r := Analyze(shots["pad swell"], testRate, "Pads/Pad_Loop_Am_90.wav", Hints{}); r.Type != TypeLoop {
		t.Errorf("named pad loop: classified %s (score %d)", r.Type, r.TypeScore)
	}
	if r := Analyze(shots["kick"], testRate, "Drum Loops 128 BPM/Kick_01.wav", Hints{}); r.Type != TypeOneShot {
		t.Errorf("kick in a loop folder: classified %s (score %d)", r.Type, r.TypeScore)
	}
	// Embedded metadata counts like the name does.
	tail := loops()["loop with a tail"]
	if r := Analyze(tail, testRate, "untitled.wav", Hints{Type: TypeOneShot}); r.Type != TypeOneShot {
		t.Errorf("loose loop flagged one-shot by its metadata: classified %s (score %d)", r.Type, r.TypeScore)
	}
}
