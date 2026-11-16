package analysis

import (
	"fmt"
	"math"
	"testing"
)

func tempoOK(got, want float64) bool { return math.Abs(got-want) <= 0.01*want }

// Onsets alone cannot tell a beat from its half or double, so the detector
// settles octaves by convention into roughly 89..178 BPM. A loop written
// below that may come back at its own tempo or at double time (70 -> 140);
// both are right.
func tempoAccepted(got, written float64) bool {
	return tempoOK(got, written) || (written < 89 && tempoOK(got, 2*written))
}

// tally counts detector outcomes over a batch of loops.
type tally struct{ total, exact, doubled int }

func (c *tally) check(t *testing.T, got, written float64, format string, args ...any) {
	t.Helper()
	c.total++
	switch {
	case tempoOK(got, written):
		c.exact++
	case tempoAccepted(got, written):
		c.doubled++
	default:
		t.Errorf(format+": got %g, want %g", append(args, got, written)...)
	}
}

func (c tally) String() string {
	return fmt.Sprintf("%d/%d within 1%% of the written tempo, %d more at double time by convention (all written below 89 BPM)",
		c.exact, c.total, c.doubled)
}

var testTempos = []float64{70, 80, 87, 90, 95, 100, 110, 120, 124, 128, 135, 140, 150, 160, 170, 174, 175}

func TestTempoBarCutLoops(t *testing.T) {
	cases := []struct {
		name    string
		pattern drumPattern
		swing   float64
		rate    int
	}{
		{"four-on-floor", fourOnFloor, 0, testRate},
		{"backbeat", backbeat, 0, testRate},
		{"breakbeat", breakbeat, 0, testRate},
		{"four-on-floor swing", fourOnFloor, 0.3, testRate},
		{"breakbeat swing", breakbeat, 0.33, testRate},
		{"backbeat 48k", backbeat, 0, 48000},
	}
	type job struct {
		name    string
		pattern drumPattern
		swing   float64
		rate    int
		bpm     float64
		bars    int
	}
	var jobs []job
	for _, c := range cases {
		for _, bpm := range testTempos {
			for _, bars := range []int{1, 2, 4, 8} {
				jobs = append(jobs, job{c.name, c.pattern, c.swing, c.rate, bpm, bars})
			}
		}
	}
	results := parallelMap(len(jobs), func(i int) TempoEstimate {
		j := jobs[i]
		return DetectTempo(drumLoop(j.pattern, j.bpm, j.bars, j.rate, j.swing), j.rate)
	})
	var counts tally
	for i, j := range jobs {
		counts.check(t, results[i].BPM, j.bpm, "%s %g BPM %d bars", j.name, j.bpm, j.bars)
		if !results[i].BarCut {
			t.Errorf("%s %g BPM %d bars: not recognised as bar-cut", j.name, j.bpm, j.bars)
		}
	}
	t.Logf("bar-cut drum loops: %v", counts)
}

// A loop cut to whole beats at a whole tempo keeps that tempo when the
// octave nearer the centre would be a fraction: 185 is not 92.5, and 195 is
// neither 97.5 nor 98.
func TestTempoKeepsExactOctave(t *testing.T) {
	for _, pattern := range []drumPattern{fourOnFloor, backbeat, breakbeat} {
		for _, bpm := range []float64{181, 185, 187, 195, 199} {
			for _, bars := range []int{1, 2, 4} {
				got := DetectTempo(drumLoop(pattern, bpm, bars, testRate, 0), testRate)
				if got.BPM != bpm || !got.ExactFit {
					t.Errorf("pattern %d %g BPM %d bars: got %g (exact fit %v)", pattern, bpm, bars, got.BPM, got.ExactFit)
				}
			}
		}
	}
}

// Loops that are not a whole number of beats long: a tail left ringing,
// silence in front, or a cut in the middle of a bar.
func TestTempoNotBarCut(t *testing.T) {
	type job struct {
		name        string
		pattern     drumPattern
		bpm         float64
		beats       int
		lead, extra float64 // seconds of silence before, audio after
	}
	var jobs []job
	for _, pattern := range []drumPattern{fourOnFloor, backbeat, breakbeat} {
		for _, bpm := range testTempos {
			jobs = append(jobs,
				job{"tail", pattern, bpm, 8, 0, 0.37 * 60 / bpm},
				job{"lead-in", pattern, bpm, 16, 0.113, 0.2},
				job{"mid-bar cut", pattern, bpm, 13, 0, 0.61 * 60 / bpm},
			)
		}
	}
	results := parallelMap(len(jobs), func(i int) TempoEstimate {
		j := jobs[i]
		s := newSynth(j.lead+loopSeconds(j.bpm, j.beats)+j.extra, testRate, uint64(j.bpm))
		s.drums(j.pattern, j.bpm, j.beats+2, j.lead, 0)
		return DetectTempo(s.out(), testRate)
	})
	var counts tally
	for i, j := range jobs {
		counts.check(t, results[i].BPM, j.bpm, "pattern %d %g BPM %s", j.pattern, j.bpm, j.name)
		if results[i].BarCut {
			t.Errorf("pattern %d %g BPM %s: taken for bar-cut", j.pattern, j.bpm, j.name)
		}
	}
	t.Logf("not bar-cut: %v", counts)
}

func TestTempoTonalLoops(t *testing.T) {
	var counts tally
	for i, bpm := range []float64{70, 90, 100, 120, 128, 140, 160, 175} {
		for _, style := range []harmonyStyle{arpeggio, bassline} {
			for _, bars := range []int{1, 2, 4} {
				x := tonalLoop(style, i, i%2 == 0, minorProgressions[i%4], bpm, bars, false)
				got := DetectTempo(x, testRate)
				counts.check(t, got.BPM, bpm, "style %d %g BPM %d bars", style, bpm, bars)
			}
		}
	}
	t.Logf("tonal loops without drums: %v", counts)
}

func TestTempoSustainedPad(t *testing.T) {
	// One chord per bar and no transients inside the bar: little more than
	// the length gives the tempo away.
	x := tonalLoop(chords, 9, true, []int{0}, 120, 4, false)
	got := DetectTempo(x, testRate)
	// The chords change once a bar, which supports 60 as well as 120.
	if !tempoOK(got.BPM, 120) && !tempoOK(got.BPM, 60) {
		t.Errorf("pad loop: got %g, want 120 or 60", got.BPM)
	}
	if got.Confidence > 0.6 {
		t.Errorf("pad loop: confidence %g is too sure", got.Confidence)
	}
}

// An MP3 encoder pads the audio, so the length no longer divides into beats
// at a whole tempo; the rhythm should pull the answer back to one.
func TestTempoPaddedLoop(t *testing.T) {
	for _, bpm := range []float64{100, 124, 140, 174} {
		loop := drumLoop(backbeat, bpm, 2, testRate, 0)
		padded := append(make([]float32, 1105), loop...)
		padded = append(padded, make([]float32, 700)...)
		if got := DetectTempo(padded, testRate); got.BPM != bpm {
			t.Errorf("padded %g BPM loop: got %g", bpm, got.BPM)
		}
	}
}

func TestTempoDegenerateInput(t *testing.T) {
	for name, x := range map[string][]float32{
		"empty":   nil,
		"silence": make([]float32, testRate*2),
		"click":   {1},
		"short":   make([]float32, 100),
	} {
		if got := DetectTempo(x, testRate); got.BPM != 0 {
			t.Errorf("%s: got %g BPM", name, got.BPM)
		}
	}
	if got := DetectTempo(make([]float32, 100), 0); got.BPM != 0 {
		t.Errorf("zero rate: got %g BPM", got.BPM)
	}
}

func TestBarCandidates(t *testing.T) {
	tests := []struct {
		dur  float64
		want []float64
	}{
		{4, []float64{60, 90, 120, 180}}, // 4, 6, 8, 12 beats
		{2, []float64{60, 90, 120, 180}}, // 2, 3, 4, 6 beats
		{60.0 / 140 * 16, []float64{70, 105, 140}},
		{0.2, nil},
		{0, nil},
	}
	for _, tc := range tests {
		var got []float64
		for _, c := range barCandidates(tc.dur) {
			got = append(got, math.Round(c.bpm*100)/100)
		}
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("barCandidates(%g) = %v, want %v", tc.dur, got, tc.want)
		}
	}
	exact := func(dur float64) bool {
		for _, c := range barCandidates(dur) {
			if c.exact {
				return true
			}
		}
		return false
	}
	if !exact(4) || !exact(60.0/174*16) {
		t.Error("an exact loop length was not marked exact")
	}
	if exact(4.0731) || exact(0.31) {
		t.Error("an arbitrary length was marked exact")
	}
}

func TestChooseTempo(t *testing.T) {
	tests := []struct {
		name    string
		choices []tempoChoice
		want    float64
	}{
		{"none", nil, 0},
		{"single", []tempoChoice{{100, 0.9, 1, true}}, 100},
		{"octave pair settles near the centre", []tempoChoice{{70, 0.9, 1, true}, {140, 0.85, 1, true}}, 140},
		{"174 is not halved", []tempoChoice{{87, 0.95, 1, true}, {174, 0.9, 1, true}}, 174},
		{"unsupported double is not taken", []tempoChoice{{70, 0.9, 1, true}, {140, 0.4, 1, true}}, 70},
		{"stronger pulse beats a dotted rival", []tempoChoice{{120, 0.9, 1, true}, {90, 0.5, 1, true}, {180, 0.3, 1, true}}, 120},
		{"bonus breaks a near tie", []tempoChoice{{90, 0.80, 1, true}, {120, 0.78, 1.15, true}}, 120},
		{"exact octave beats an inexact one nearer the centre", []tempoChoice{{92.5, 0.9, 1, false}, {185, 0.88, 1, true}}, 185},
		{"inexact octaves still settle near the centre", []tempoChoice{{92.5, 0.9, 1, false}, {185, 0.88, 1, false}}, 92.5},
		{"unsupported exact octave is not taken", []tempoChoice{{92.5, 0.9, 1, false}, {185, 0.4, 1, true}}, 92.5},
		{"no support", []tempoChoice{{120, -0.1, 1, true}}, 0},
	}
	for _, tc := range tests {
		got, conf := choose(tc.choices)
		if got.bpm != tc.want {
			t.Errorf("%s: got %g, want %g", tc.name, got.bpm, tc.want)
		}
		if conf < 0 || conf > 1 || math.IsNaN(conf) {
			t.Errorf("%s: confidence %g out of range", tc.name, conf)
		}
	}
}
