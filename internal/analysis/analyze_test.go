package analysis

import (
	"math"
	"sync"
	"testing"
)

func TestAnalyzeEndToEnd(t *testing.T) {
	shots := oneShots()
	tests := []struct {
		name  string
		audio []float32
		rate  int
		path  string
		hints Hints
		check func(t *testing.T, r Result, m Merged)
	}{
		{
			name:  "named loop, name and audio agree",
			audio: tonalLoop(chords, 9, true, minorProgressions[1], 128, 2, true),
			path:  "Loops/Synth_Loop_128_Am.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if r.Type != TypeLoop || r.BPM != 128 || r.Key != "Am" {
					t.Errorf("detected type=%s bpm=%g key=%q", r.Type, r.BPM, r.Key)
				}
				want := Merged{Meta: Meta{TypeLoop, SourceFilename, 128, SourceFilename, "Am", SourceFilename}}
				if m != want {
					t.Errorf("merged %+v", m)
				}
			},
		},
		{
			name:  "unnamed loop: everything detected",
			audio: tonalLoop(bassline, 4, true, minorProgressions[1], 100, 2, true),
			path:  "audio_0042.wav",
			check: func(t *testing.T, r Result, m Merged) {
				want := Merged{Meta: Meta{TypeLoop, SourceDetected, 100, SourceDetected, "Em", SourceDetected}}
				if m != want {
					t.Errorf("merged %+v", m)
				}
				if r.BPMConf <= 0 || r.BPMConf > 1 || r.KeyConf <= 0 || r.KeyConf > 1 {
					t.Errorf("confidences bpm=%g key=%g", r.BPMConf, r.KeyConf)
				}
			},
		},
		{
			name:  "name tempo is the half-time reading",
			audio: drumLoop(backbeat, 70, 2, testRate, 0),
			path:  "Drum_Loop_70.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if m.BPM != 70 || m.BPMSrc != SourceFilename || m.NeedsReview {
					t.Errorf("merged bpm=%g src=%s review=%v (detected %g)", m.BPM, m.BPMSrc, m.NeedsReview, r.BPM)
				}
				if r.Key != "" {
					t.Errorf("drum loop got key %q", r.Key)
				}
			},
		},
		{
			name:  "name tempo contradicts the audio",
			audio: drumLoop(fourOnFloor, 128, 2, testRate, 0),
			path:  "Top_Loop_100.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if m.BPM != 100 || !m.NeedsReview || r.BPM != 128 {
					t.Errorf("merged bpm=%g review=%v detected=%g", m.BPM, m.NeedsReview, r.BPM)
				}
			},
		},
		{
			name:  "kick in a tempo folder",
			audio: shots["kick"],
			path:  "Trap 140 BPM Gm/Kick_01.wav",
			check: func(t *testing.T, r Result, m Merged) {
				want := Merged{Meta: Meta{Type: TypeOneShot, TypeSrc: SourceDetected}}
				if m != want {
					t.Errorf("merged %+v", m)
				}
			},
		},
		{
			name:  "tonal one-shot gets its root",
			audio: shots["pluck"], // A3
			path:  "Pluck_Soft.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if r.Type != TypeOneShot || r.Key != "A" || !r.KeyRootOnly || r.BPM != 0 {
					t.Errorf("type=%s key=%q rootOnly=%v bpm=%g", r.Type, r.Key, r.KeyRootOnly, r.BPM)
				}
				if m.Key != "A" || m.KeySrc != SourceDetected {
					t.Errorf("merged key %q (%s)", m.Key, m.KeySrc)
				}
			},
		},
		{
			name:  "chord name gives the mode",
			audio: shots["pluck"],
			path:  "Keys_Amin7.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if m.Key != "Am" || m.KeySrc != SourceFilename {
					t.Errorf("merged key %q (%s)", m.Key, m.KeySrc)
				}
			},
		},
		{
			name:  "velocity in a multisample name is not a tempo",
			audio: shots["piano chord"][:3*testRate/2],
			path:  "Rhodes_F3_100.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if m.Type != TypeOneShot || m.BPM != 0 || m.BPMSrc != "" {
					t.Errorf("merged %+v", m)
				}
			},
		},
		{
			name:  "model number is not a tempo",
			audio: shots["pad swell"],
			path:  "Roland Juno-106/Pads/Juno-106 Pad C3.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if m.Type != TypeOneShot || m.BPM != 0 {
					t.Errorf("merged %+v", m)
				}
			},
		},
		{
			name:  "bare number that the audio bears out is the tempo",
			audio: drumLoop(backbeat, 128, 2, testRate, 0),
			path:  "Dusty 128 Dark.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if !r.Parsed.BPMWeak || m.Type != TypeLoop || m.BPM != 128 || m.BPMSrc != SourceFilename || m.NeedsReview {
					t.Errorf("weak=%v merged %+v", r.Parsed.BPMWeak, m)
				}
			},
		},
		{
			name:  "bare number that the audio contradicts is not",
			audio: drumLoop(backbeat, 128, 2, testRate, 0),
			path:  "Blink 182 Style Drums.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if m.Type != TypeLoop || m.BPM != 128 || m.BPMSrc != SourceDetected || m.NeedsReview {
					t.Errorf("merged %+v", m)
				}
			},
		},
		{
			name:  "chord stab gets its key, not its loudest note",
			audio: chordStab(60, 64, 67),
			path:  "Stab_07.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if m.Type != TypeOneShot || m.Key != "C" || m.KeySrc != SourceDetected || r.KeyRootOnly {
					t.Errorf("key %q (%s) rootOnly=%v type=%s", m.Key, m.KeySrc, r.KeyRootOnly, m.Type)
				}
			},
		},
		{
			name:  "minor chord stab",
			audio: chordStab(57, 60, 64),
			path:  "Stab_08.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if m.Key != "Am" || m.KeySrc != SourceDetected {
					t.Errorf("key %q (%s)", m.Key, m.KeySrc)
				}
			},
		},
		{
			name:  "embedded metadata fills the gaps",
			audio: drumLoop(breakbeat, 172, 2, testRate, 0),
			path:  "amen.wav",
			hints: Hints{BPM: 172, Key: "f# minor", Type: TypeLoop},
			check: func(t *testing.T, r Result, m Merged) {
				want := Merged{Meta: Meta{TypeLoop, SourceFilename, 172, SourceFilename, "F#m", SourceFilename}}
				if m != want {
					t.Errorf("merged %+v", m)
				}
			},
		},
		{
			name:  "48 kHz",
			audio: drumLoop(backbeat, 126, 2, 48000, 0),
			rate:  48000,
			path:  "x.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if r.BPM != 126 || math.Abs(r.Dur-loopSeconds(126, 8)) > 1e-3 {
					t.Errorf("bpm=%g dur=%g", r.BPM, r.Dur)
				}
			},
		},
		{
			name:  "silence",
			audio: make([]float32, testRate),
			path:  "Bass_Loop_120_Am.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if r.Type != "" || r.BPM != 0 || r.Key != "" || r.Dur != 1 || r.TrimmedDur != 0 {
					t.Errorf("silence: %+v", r)
				}
				// The name still counts.
				if m.BPM != 120 || m.Key != "Am" {
					t.Errorf("merged %+v", m)
				}
			},
		},
		{
			name: "no audio",
			path: "Bass_Loop_120_Am.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if r.Dur != 0 || r.Type != "" || r.Parsed.BPM != 120 {
					t.Errorf("%+v", r)
				}
			},
		},
		{
			name:  "NaN samples do not poison the result",
			audio: []float32{0, float32(math.NaN()), 0.5, float32(math.Inf(1)), -0.5},
			path:  "x.wav",
			check: func(t *testing.T, r Result, m Merged) {
				if math.IsNaN(r.BPM) || math.IsNaN(r.BPMConf) || math.IsNaN(r.KeyConf) || r.Key != "" {
					t.Errorf("%+v", r)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rate := tc.rate
			if rate == 0 {
				rate = testRate
			}
			r := Analyze(tc.audio, rate, tc.path, tc.hints)
			tc.check(t, r, Merge(Meta{}, r))
		})
	}
}

func chordStab(notes ...int) []float32 {
	s := newSynth(1, testRate, 1)
	for _, n := range notes {
		s.pluck(0, 1, n, 0.2, 4)
	}
	return s.out()
}

func TestAnalyzeTrimmedDuration(t *testing.T) {
	s := newSynth(3, testRate, 1)
	s.pluck(0.5, 1, 60, 0.5, 3)
	r := Analyze(s.out(), testRate, "x.wav", Hints{})
	if r.Dur != 3 || math.Abs(r.TrimmedDur-1) > 0.05 {
		t.Errorf("dur=%g trimmed=%g, want 3 and about 1", r.Dur, r.TrimmedDur)
	}
}

// Long files are analysed on their opening only, but measured in full.
func TestAnalyzeLongFile(t *testing.T) {
	x := drumLoop(fourOnFloor, 120, 32, testRate, 0) // 64 s
	r := Analyze(x, testRate, "x.wav", Hints{})
	if r.Dur != 64 || r.Type != TypeLoop || r.BPM != 120 {
		t.Errorf("dur=%g type=%s bpm=%g", r.Dur, r.Type, r.BPM)
	}
}

// The beginning of a longer file is not a sample of its own: its length and
// the place it was cut say nothing about the whole.
func TestAnalyzeCutOffBeginning(t *testing.T) {
	whole := drumLoop(fourOnFloor, 120, 4, testRate, 0) // 8 s
	cut := whole[:5*testRate+testRate/3]
	r := Analyze(cut, testRate, "x.wav", Hints{Dur: 8})
	if !r.Truncated || r.Dur != 8 || r.Features.Dur != 8 {
		t.Errorf("truncated=%v dur=%g features.dur=%g, want true, 8, 8", r.Truncated, r.Dur, r.Features.Dur)
	}
	if !math.IsInf(r.Features.TailDB, -1) || r.Features.MonotonicDecay {
		t.Errorf("tail=%g decay=%v: the cut was read as the sample's ending", r.Features.TailDB, r.Features.MonotonicDecay)
	}
	if r.Type != TypeLoop || r.BPM != 120 {
		t.Errorf("type=%s bpm=%g, want a loop at 120", r.Type, r.BPM)
	}
	for _, bpm := range r.BarBPMs {
		if beats := bpm * 8 / 60; math.Abs(beats-math.Round(beats)) > 0.01 {
			t.Errorf("bar tempo %g does not fit the real length", bpm)
		}
	}
	// The same audio without the hint is measured as it stands.
	if r := Analyze(cut, testRate, "x.wav", Hints{}); r.Truncated || r.Dur >= 6 {
		t.Errorf("no hint: truncated=%v dur=%g", r.Truncated, r.Dur)
	}
	// A length that merely rounds differently is not a cut.
	if r := Analyze(whole, testRate, "x.wav", Hints{Dur: 8.01}); r.Truncated || r.Dur != 8 {
		t.Errorf("rounding: truncated=%v dur=%g", r.Truncated, r.Dur)
	}
}

func TestAnalyzeConcurrent(t *testing.T) {
	x := tonalLoop(arpeggio, 2, false, majorProgressions[0], 120, 2, true)
	want := Analyze(x, testRate, "x.wav", Hints{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if got := Analyze(x, testRate, "x.wav", Hints{}); got.BPM != want.BPM || got.Key != want.Key {
				t.Errorf("concurrent run differs: %g %q", got.BPM, got.Key)
			}
		})
	}
	wg.Wait()
}
