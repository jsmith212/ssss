package audio

import (
	"bytes"
	"errors"
	"fmt"
	"iter"
	"runtime"
	"slices"
	"sync"
	"testing"
)

// corpus returns one valid file per decoder path.
func corpus(t testing.TB) map[string][]byte {
	return map[string][]byte{
		"wav s16":     buildWAV(wavSpec{enc: encIntLE, size: 2, frames: 600}),
		"wav u8 meta": buildWAV(wavSpec{enc: encU8, size: 1, channels: 1, frames: 601, after: [][]byte{acidChunk(2, 60, 4, 120), smplChunk(60, [2]uint32{0, 500})}}),
		"wav ext f32": buildWAV(wavSpec{enc: encF32LE, size: 4, channels: 6, extensible: true, frames: 300}),
		"wav rf64":    buildWAV(wavSpec{enc: encIntLE, size: 3, rf64: true, frames: 300}),
		"aiff":        buildAIFF(aiffSpec{enc: encIntBE, size: 2, extra: [][]byte{instChunk(60, 1, 1, 2), markChunk(map[uint16]uint32{1: 10, 2: 500})}}),
		"aifc sowt":   buildAIFF(aiffSpec{enc: encIntLE, size: 2, codec: "sowt"}),
		"aifc fl32":   buildAIFF(aiffSpec{enc: encF32BE, size: 4, codec: "fl32"}),
		"flac":        buildFLAC(t, 16, 2),
		"mp3 mono":    testdata(t, "mono.mp3"),
		"mp3 id3":     testdata(t, "stereo_id3.mp3"),
		"ogg":         testdata(t, "stereo.ogg"),
		"ogg 3ch":     testdata(t, "three.ogg"),
		"opus":        testdata(t, "opus.ogg"),
		"adpcm":       testdata(t, "adpcm.wav"),
	}
}

// exercise decodes and probes data and checks the invariants every caller
// relies on. Errors are fine; panics and inconsistent results are not.
func exercise(t testing.TB, data []byte) {
	t.Helper()
	if err := check(data); err != nil {
		t.Fatal(err)
	}
}

func check(data []byte) error {
	pcm, info, err := readBytes(data, options{})
	if err != nil {
		if pcm != nil {
			return errors.New("PCM returned with an error")
		}
	} else {
		if pcm.Channels < 1 || pcm.Rate < 1 || len(pcm.Data) != pcm.Channels || pcm.Frames > info.Frames {
			return fmt.Errorf("inconsistent PCM: %d ch, %d Hz, %d planes, %d of %d frames", pcm.Channels, pcm.Rate, len(pcm.Data), pcm.Frames, info.Frames)
		}
		for c, ch := range pcm.Data {
			if len(ch) != pcm.Frames {
				return fmt.Errorf("channel %d has %d samples, want %d", c, len(ch), pcm.Frames)
			}
		}
		for _, l := range info.Meta.Loops {
			if l.Start < 0 || l.Start >= l.End || l.End > info.Frames {
				return fmt.Errorf("loop %+v outside %d frames", l, info.Frames)
			}
		}
		st := pcm.Stereo()
		if len(Mini(st)) != MiniBuckets || len(Full(st)) != FullSize || len(pcm.Mono()) != pcm.Frames {
			return errors.New("peaks or mono have the wrong length")
		}
	}
	// Probe reads headers only, so it may accept a file whose audio a
	// decode rejects; it just has to return.
	if _, pinfo, err := readBytes(data, options{probe: true}); err == nil && (pinfo.Channels < 1 || pinfo.Rate < 1 || pinfo.Frames < 0) {
		return fmt.Errorf("probe accepted %+v", pinfo)
	}
	_, _, _ = readBytes(data, options{maxFrames: 100})
	return nil
}

// exerciseAll runs check on every input on all CPUs: there are thousands,
// and under the race detector each takes milliseconds.
func exerciseAll(t *testing.T, inputs iter.Seq2[string, []byte]) {
	t.Helper()
	type input struct {
		name string
		data []byte
	}
	work := make(chan input)
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		failed []string
	)
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for in := range work {
				if err := check(in.data); err != nil {
					mu.Lock()
					failed = append(failed, fmt.Sprintf("%s: %v", in.name, err))
					mu.Unlock()
				}
			}
		})
	}
	for name, data := range inputs {
		work <- input{name, data}
	}
	close(work)
	wg.Wait()
	slices.Sort(failed)
	for _, f := range failed[:min(len(failed), 20)] {
		t.Error(f)
	}
}

// Every prefix of every format must come back as audio or an error.
func TestTruncatedNeverPanics(t *testing.T) {
	for name, data := range corpus(t) {
		t.Run(name, func(t *testing.T) {
			exerciseAll(t, func(yield func(string, []byte) bool) {
				step := max(1, len(data)/150)
				for n := 0; n < len(data); n += step {
					if !yield(fmt.Sprintf("cut to %d bytes", n), data[:n]) {
						return
					}
				}
				for n := range min(len(data), 128) {
					if !yield(fmt.Sprintf("cut to %d bytes", n), data[:n]) {
						return
					}
				}
			})
		})
	}
}

// Headers with every byte in turn overwritten must not panic either.
func TestMutatedHeadersNeverPanic(t *testing.T) {
	for name, data := range corpus(t) {
		t.Run(name, func(t *testing.T) {
			exerciseAll(t, func(yield func(string, []byte) bool) {
				for i := range min(len(data), 160) {
					for _, v := range []byte{0x00, 0xff} {
						mutated := bytes.Clone(data)
						mutated[i] = v
						if !yield(fmt.Sprintf("byte %d = %#02x", i, v), mutated) {
							return
						}
					}
				}
			})
		})
	}
}

func TestTruncatedHeadersAreErrors(t *testing.T) {
	// Shorter than this, no format has room for its mandatory header.
	minimum := map[string]int{"wav s16": 44, "aiff": 12 + 26, "aifc sowt": 12 + 26, "flac": 42, "mp3 mono": 4, "ogg": 58}
	for name, data := range corpus(t) {
		limit, ok := minimum[name]
		if !ok {
			continue
		}
		for n := range limit {
			if _, _, err := readBytes(data[:n], options{}); err == nil {
				t.Errorf("%s cut to %d bytes decoded without error", name, n)
			}
			if _, _, err := readBytes(data[:n], options{probe: true}); err == nil {
				t.Errorf("%s cut to %d bytes probed without error", name, n)
			}
		}
	}
}

func FuzzRead(f *testing.F) {
	for _, data := range corpus(f) {
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		exercise(t, data)
	})
}
