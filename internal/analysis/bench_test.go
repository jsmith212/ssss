package analysis

import "testing"

// A 4 s loop at 44.1 kHz with drums and harmony: the common case at ingest.
func benchLoop() []float32 {
	return tonalLoop(chords, 9, true, minorProgressions[0], 120, 2, true)
}

func BenchmarkAnalyzeLoop4s(b *testing.B) {
	x := benchLoop()
	b.ReportAllocs()
	for b.Loop() {
		Analyze(x, testRate, "Loops/Synth_Loop_120_Am.wav", Hints{})
	}
}

func BenchmarkAnalyzeOneShot(b *testing.B) {
	s := newSynth(0.5, testRate, 1)
	s.kick(0, 0.9)
	x := s.out()
	b.ReportAllocs()
	for b.Loop() {
		Analyze(x, testRate, "Kick_01.wav", Hints{})
	}
}

func BenchmarkAnalyzeLoop30s(b *testing.B) {
	x := tonalLoop(arpeggio, 2, false, majorProgressions[0], 128, 16, true)
	b.ReportAllocs()
	for b.Loop() {
		Analyze(x, testRate, "long.wav", Hints{})
	}
}

func BenchmarkDetectTempo4s(b *testing.B) {
	x := benchLoop()
	for b.Loop() {
		DetectTempo(x, testRate)
	}
}

func BenchmarkDetectKey4s(b *testing.B) {
	x := benchLoop()
	for b.Loop() {
		DetectKey(x, testRate, false)
	}
}

func BenchmarkParseName(b *testing.B) {
	for b.Loop() {
		ParseName("Loops/Synth Loops 120 Bpm/OS_TR_140_Fmin_Melody_Loop_Dreams.wav")
	}
}
