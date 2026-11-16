package audio

import "testing"

func BenchmarkDecodeWAV(b *testing.B) {
	data := buildWAV(wavSpec{enc: encIntLE, size: 3, frames: 44100 * 10})
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := readBytes(data, options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProbeWAV(b *testing.B) {
	path := writeTemp(b, "probe.wav", buildWAV(wavSpec{enc: encIntLE, size: 3, frames: 44100 * 10, after: [][]byte{acidChunk(2, 60, 16, 120)}}))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Probe(path); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPeaks(b *testing.B) {
	p := newPCM(44100, 2, 44100*10)
	b.ReportAllocs()
	for b.Loop() {
		Mini(p)
		Full(p)
	}
}
