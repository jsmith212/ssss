package audio

import (
	"encoding/binary"
	"errors"
	"io/fs"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestDecodeWAV(t *testing.T) {
	odd := riffChunk("LIST", []byte("INFO!"), true)
	oddNoPad := riffChunk("junk", []byte("12345"), false)
	tests := []struct {
		name     string
		spec     wavSpec
		bitDepth int
		float    bool
		tol      float64
		frames   int // 0 = fixFrames
	}{
		{name: "u8", spec: wavSpec{enc: encU8, size: 1}, bitDepth: 8, tol: 0.01},
		{name: "s16", spec: wavSpec{enc: encIntLE, size: 2}, bitDepth: 16, tol: 1e-3},
		{name: "s24", spec: wavSpec{enc: encIntLE, size: 3}, bitDepth: 24, tol: 1e-4},
		{name: "s32", spec: wavSpec{enc: encIntLE, size: 4}, bitDepth: 32, tol: 1e-4},
		{name: "f32", spec: wavSpec{enc: encF32LE, size: 4}, bitDepth: 32, float: true, tol: 1e-4},
		{name: "f64", spec: wavSpec{enc: encF64LE, size: 8}, bitDepth: 64, float: true, tol: 1e-4},
		{name: "mono s16", spec: wavSpec{enc: encIntLE, size: 2, channels: 1}, bitDepth: 16, tol: 1e-3},
		{name: "extensible s16 6ch", spec: wavSpec{enc: encIntLE, size: 2, channels: 6, extensible: true}, bitDepth: 16, tol: 1e-3},
		{name: "extensible 24 in 32", spec: wavSpec{enc: encIntLE, size: 4, extensible: true, validBits: 24}, bitDepth: 24, tol: 1e-4},
		{name: "extensible f32", spec: wavSpec{enc: encF32LE, size: 4, extensible: true}, bitDepth: 32, float: true, tol: 1e-4},
		{name: "odd chunk padded", spec: wavSpec{enc: encIntLE, size: 2, before: [][]byte{odd}}, bitDepth: 16, tol: 1e-3},
		{name: "odd chunk unpadded", spec: wavSpec{enc: encIntLE, size: 2, before: [][]byte{oddNoPad}}, bitDepth: 16, tol: 1e-3},
		{name: "odd data then chunk", spec: wavSpec{enc: encU8, size: 1, channels: 1, after: [][]byte{acidChunk(1, 0, 0, 0)}}, bitDepth: 8, tol: 0.01},
		{name: "odd data unpadded then chunk", spec: wavSpec{enc: encU8, size: 1, channels: 1, noDataPad: true, after: [][]byte{acidChunk(1, 0, 0, 0)}}, bitDepth: 8, tol: 0.01},
		{name: "data size zero", spec: wavSpec{enc: encIntLE, size: 2, dataSize: u32p(0)}, bitDepth: 16, tol: 1e-3},
		{name: "data size max", spec: wavSpec{enc: encIntLE, size: 2, dataSize: u32p(math.MaxUint32)}, bitDepth: 16, tol: 1e-3},
		{name: "data size too big", spec: wavSpec{enc: encIntLE, size: 2, dataSize: u32p(fixFrames*4 + 4000)}, bitDepth: 16, tol: 1e-3},
		{name: "data size not whole frames", spec: wavSpec{enc: encIntLE, size: 2, dataSize: u32p(fixFrames*4 - 3)}, bitDepth: 16, tol: 1e-3, frames: fixFrames - 1},
		{name: "rf64", spec: wavSpec{enc: encIntLE, size: 3, rf64: true, dataSize: u32p(math.MaxUint32)}, bitDepth: 24, tol: 1e-4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := buildWAV(tt.spec)
			spec := tt.spec.withDefaults()
			wantFrames := fixFrames
			if tt.frames != 0 {
				wantFrames = tt.frames
			}

			pcm, info, err := readBytes(data, options{})
			if err != nil {
				t.Fatal(err)
			}
			want := Info{Format: WAV, Rate: fixRate, Channels: spec.channels, Frames: wantFrames, BitDepth: tt.bitDepth, Float: tt.float}
			got := info
			got.Meta = Meta{}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("info = %+v, want %+v", got, want)
			}
			if pcm.Rate != fixRate || pcm.Channels != spec.channels || pcm.Frames != wantFrames || len(pcm.Data) != spec.channels {
				t.Fatalf("pcm = %d Hz, %d ch, %d frames", pcm.Rate, pcm.Channels, pcm.Frames)
			}
			checkTones(t, pcm, tt.tol)

			_, pinfo, err := readBytes(data, options{probe: true})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(pinfo, info) {
				t.Errorf("probe info = %+v, decode info = %+v", pinfo, info)
			}
		})
	}
}

func TestWAVMetadata(t *testing.T) {
	tests := []struct {
		name   string
		chunks [][]byte
		before bool
		want   Meta
	}{
		{
			name:   "none",
			chunks: nil,
			want:   Meta{RootNote: -1, UnityNote: -1},
		},
		{
			name:   "acid loop",
			chunks: [][]byte{acidChunk(2, 57, 8, 128)},
			want:   Meta{HasAcid: true, Tempo: 128, Beats: 8, RootNote: 57, UnityNote: -1},
		},
		{
			name:   "acid one-shot without root",
			chunks: [][]byte{acidChunk(1, 60, 0, 0)},
			want:   Meta{HasAcid: true, OneShot: true, RootNote: -1, UnityNote: -1},
		},
		{
			name:   "acid garbage tempo",
			chunks: [][]byte{acidChunk(0, 0, 4, float32(math.Inf(1)))},
			want:   Meta{HasAcid: true, Beats: 4, RootNote: -1, UnityNote: -1},
		},
		{
			name:   "smpl loop inclusive end",
			chunks: [][]byte{smplChunk(48, [2]uint32{100, 9999})},
			want:   Meta{RootNote: -1, UnityNote: 48, Loops: []Loop{{100, 10000}}},
		},
		{
			name:   "smpl loop to last frame and exclusive-style end",
			chunks: [][]byte{smplChunk(60, [2]uint32{0, fixFrames - 1}, [2]uint32{10, fixFrames})},
			want:   Meta{RootNote: -1, UnityNote: 60, Loops: []Loop{{0, fixFrames}, {10, fixFrames}}},
		},
		{
			name:   "smpl loops out of range dropped",
			chunks: [][]byte{smplChunk(200, [2]uint32{500, 100}, [2]uint32{0, fixFrames + 50})},
			want:   Meta{RootNote: -1, UnityNote: -1},
		},
		{
			name:   "both before data",
			chunks: [][]byte{acidChunk(2, 40, 16, 174.5), smplChunk(40, [2]uint32{0, 4409})},
			before: true,
			want:   Meta{HasAcid: true, Tempo: 174.5, Beats: 16, RootNote: 40, UnityNote: 40, Loops: []Loop{{0, 4410}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := wavSpec{enc: encIntLE, size: 2, after: tt.chunks}
			if tt.before {
				spec.after, spec.before = nil, tt.chunks
			}
			data := buildWAV(spec)
			for _, probe := range []bool{false, true} {
				_, info, err := readBytes(data, options{probe: probe})
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(info.Meta, tt.want) {
					t.Errorf("probe=%v: meta = %+v, want %+v", probe, info.Meta, tt.want)
				}
			}
		})
	}
}

func TestDecodeAIFF(t *testing.T) {
	tests := []struct {
		name     string
		spec     aiffSpec
		bitDepth int
		float    bool
		tol      float64
	}{
		{name: "s8", spec: aiffSpec{enc: encS8, size: 1}, bitDepth: 8, tol: 0.01},
		{name: "s16", spec: aiffSpec{enc: encIntBE, size: 2}, bitDepth: 16, tol: 1e-3},
		{name: "s24", spec: aiffSpec{enc: encIntBE, size: 3}, bitDepth: 24, tol: 1e-4},
		{name: "s32", spec: aiffSpec{enc: encIntBE, size: 4}, bitDepth: 32, tol: 1e-4},
		{name: "mono s16", spec: aiffSpec{enc: encIntBE, size: 2, channels: 1}, bitDepth: 16, tol: 1e-3},
		{name: "aifc NONE", spec: aiffSpec{enc: encIntBE, size: 2, codec: "NONE"}, bitDepth: 16, tol: 1e-3},
		{name: "aifc sowt", spec: aiffSpec{enc: encIntLE, size: 2, codec: "sowt"}, bitDepth: 16, tol: 1e-3},
		{name: "aifc fl32", spec: aiffSpec{enc: encF32BE, size: 4, codec: "fl32"}, bitDepth: 32, float: true, tol: 1e-4},
		{name: "aifc FL32", spec: aiffSpec{enc: encF32BE, size: 4, codec: "FL32"}, bitDepth: 32, float: true, tol: 1e-4},
		{name: "aifc fl64", spec: aiffSpec{enc: encF64BE, size: 8, codec: "fl64"}, bitDepth: 64, float: true, tol: 1e-4},
		{name: "ssnd before comm", spec: aiffSpec{enc: encIntBE, size: 2, ssndFirst: true}, bitDepth: 16, tol: 1e-3},
		{name: "ssnd offset", spec: aiffSpec{enc: encIntBE, size: 2, ssndSkip: 6}, bitDepth: 16, tol: 1e-3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := buildAIFF(tt.spec)
			channels := 2
			if tt.spec.channels != 0 {
				channels = tt.spec.channels
			}
			pcm, info, err := readBytes(data, options{})
			if err != nil {
				t.Fatal(err)
			}
			want := Info{Format: AIFF, Rate: fixRate, Channels: channels, Frames: fixFrames, BitDepth: tt.bitDepth, Float: tt.float, Meta: newMeta()}
			if !reflect.DeepEqual(info, want) {
				t.Errorf("info = %+v, want %+v", info, want)
			}
			checkTones(t, pcm, tt.tol)

			_, pinfo, err := readBytes(data, options{probe: true})
			if err != nil || !reflect.DeepEqual(pinfo, info) {
				t.Errorf("probe = %+v, %v; want %+v", pinfo, err, info)
			}
		})
	}
}

func TestAIFFLoop(t *testing.T) {
	data := buildAIFF(aiffSpec{enc: encIntBE, size: 2, extra: [][]byte{
		instChunk(64, 1, 7, 9),
		markChunk(map[uint16]uint32{7: 1000, 9: 8000, 3: 5}),
	}})
	_, info, err := readBytes(data, options{probe: true})
	if err != nil {
		t.Fatal(err)
	}
	want := Meta{RootNote: -1, UnityNote: 64, Loops: []Loop{{1000, 8000}}}
	if !reflect.DeepEqual(info.Meta, want) {
		t.Errorf("meta = %+v, want %+v", info.Meta, want)
	}

	// A loop that is switched off must not be reported.
	data = buildAIFF(aiffSpec{enc: encIntBE, size: 2, extra: [][]byte{
		instChunk(60, 0, 7, 9),
		markChunk(map[uint16]uint32{7: 1000, 9: 8000}),
	}})
	if _, info, _ = readBytes(data, options{probe: true}); info.Meta.Loops != nil {
		t.Errorf("loops = %v, want none", info.Meta.Loops)
	}
}

func TestDecodeFLAC(t *testing.T) {
	for _, tt := range []struct {
		name           string
		bits, channels int
		tol            float64
	}{
		{"16 stereo", 16, 2, 1e-3},
		{"24 stereo", 24, 2, 1e-4},
		{"16 mono", 16, 1, 1e-3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := buildFLAC(t, tt.bits, tt.channels)
			pcm, info, err := readBytes(data, options{})
			if err != nil {
				t.Fatal(err)
			}
			want := Info{Format: FLAC, Rate: fixRate, Channels: tt.channels, Frames: fixFrames, BitDepth: tt.bits, Meta: newMeta()}
			if !reflect.DeepEqual(info, want) {
				t.Errorf("info = %+v, want %+v", info, want)
			}
			if pcm.Frames != fixFrames {
				t.Fatalf("frames = %d", pcm.Frames)
			}
			checkTones(t, pcm, tt.tol)

			_, pinfo, err := readBytes(data, options{probe: true})
			if err != nil || !reflect.DeepEqual(pinfo, info) {
				t.Errorf("probe = %+v, %v", pinfo, err)
			}
		})
	}

	t.Run("behind id3 tag", func(t *testing.T) {
		tag := append([]byte("ID3\x03\x00\x00\x00\x00\x00\x10"), make([]byte, 16)...)
		data := append(tag, buildFLAC(t, 16, 2)...)
		pcm, info, err := readBytes(data, options{})
		if err != nil {
			t.Fatal(err)
		}
		if info.Format != FLAC {
			t.Errorf("format = %q", info.Format)
		}
		checkTones(t, pcm, 1e-3)
	})

	t.Run("truncated", func(t *testing.T) {
		data := buildFLAC(t, 16, 2)
		_, _, err := readBytes(data[:len(data)*2/3], options{})
		if !errors.Is(err, ErrCorrupt) {
			t.Errorf("err = %v, want ErrCorrupt", err)
		}
	})
}

// The lossy fixtures in testdata hold a 440 Hz sine starting at phase zero,
// encoded at amplitude 0.375 with ffmpeg. three.ogg carries it in the
// front-centre channel only.
const lossyAmp = 0.375

func TestDecodeLossy(t *testing.T) {
	tests := []struct {
		file     string
		format   Format
		rate     int
		channels int
		frames   int   // length of the source
		slack    int   // extra frames a codec without gapless info may add
		tone     []int // channels carrying the tone; the rest are silent
	}{
		// No Xing tag: encoder delay and padding cannot be removed.
		{file: "mono.mp3", format: MP3, rate: 44100, channels: 1, frames: 22050, slack: 2304, tone: []int{0}},
		{file: "stereo_id3.mp3", format: MP3, rate: 44100, channels: 2, frames: 22050, tone: []int{0, 1}},
		{file: "stereo.ogg", format: OGG, rate: 44100, channels: 2, frames: 22050, tone: []int{0, 1}},
		// Vorbis orders three channels L C R; WAVE order is L R C.
		{file: "three.ogg", format: OGG, rate: 48000, channels: 3, frames: 12000, tone: []int{2}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			data := testdata(t, tt.file)
			pcm, info, err := readBytes(data, options{})
			if err != nil {
				t.Fatal(err)
			}
			if info.Format != tt.format || info.Rate != tt.rate || info.Channels != tt.channels || info.BitDepth != 0 {
				t.Errorf("info = %+v", info)
			}
			if info.Frames < tt.frames || info.Frames > tt.frames+tt.slack {
				t.Errorf("frames = %d, want %d (+%d)", info.Frames, tt.frames, tt.slack)
			}
			if pcm.Frames != info.Frames || pcm.Channels != tt.channels || len(pcm.Data) != tt.channels {
				t.Fatalf("pcm frames = %d, info frames = %d, channels = %d", pcm.Frames, info.Frames, pcm.Channels)
			}
			for c, ch := range pcm.Data {
				if len(ch) != pcm.Frames {
					t.Fatalf("channel %d has %d samples", c, len(ch))
				}
				if !slices.Contains(tt.tone, c) {
					if p := peakOf(ch); p > 0.01 {
						t.Errorf("channel %d: peak %.4f, want silence", c, p)
					}
					continue
				}
				// Measure the middle, clear of codec transients.
				mid := ch[len(ch)/4 : len(ch)*3/4]
				if amp := toneAmp(mid, tt.rate, 440); math.Abs(amp-lossyAmp) > 0.03 {
					t.Errorf("channel %d: 440 Hz amplitude %.4f, want %.3f", c, amp, lossyAmp)
				}
				if p := peakOf(mid); p > lossyAmp+0.03 {
					t.Errorf("channel %d: peak %.4f", c, p)
				}
				// With the delay trimmed the tone is back at phase zero;
				// one sample of error is 0.06 rad.
				if tt.slack == 0 {
					if ph := tonePhase(ch, tt.rate, 440); math.Abs(ph) > 0.1 {
						t.Errorf("channel %d: phase %.3f rad, want 0", c, ph)
					}
				}
			}

			_, pinfo, err := readBytes(data, options{probe: true})
			if err != nil || !reflect.DeepEqual(pinfo, info) {
				t.Errorf("probe = %+v, %v; decode info %+v", pinfo, err, info)
			}
		})
	}
}

func TestUnsupported(t *testing.T) {
	mp3InWAV := buildWAV(wavSpec{enc: encF32LE, size: 4})
	mp3InWAV[20] = 0x55 // MP3-in-WAV format tag
	ulaw := buildAIFF(aiffSpec{enc: encIntBE, size: 2, codec: "ulaw"})
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{"adpcm wav", testdata(t, "adpcm.wav"), ErrUnsupported},
		{"opus in ogg", testdata(t, "opus.ogg"), ErrUnsupported},
		{"mp3 in wav", mp3InWAV, ErrUnsupported},
		{"ulaw aifc", ulaw, ErrUnsupported},
		{"empty", nil, ErrUnknownFormat},
		{"text", []byte("this is not audio, just a readme with some words in it\n"), ErrUnknownFormat},
		{"zeros", make([]byte, 4096), ErrUnknownFormat},
		{"lone sync word", append([]byte{0xff, 0xfb, 0x90, 0x00}, make([]byte, 64)...), ErrUnknownFormat},
		{"riff not wave", []byte("RIFF\x04\x00\x00\x00AVI "), ErrUnknownFormat},
		{"wav header only", []byte("RIFF\x04\x00\x00\x00WAVE"), ErrCorrupt},
		{"wav without data", buildWAV(wavSpec{enc: encIntLE, size: 2})[:36], ErrCorrupt},
		{"wav zero channels", func() []byte { b := buildWAV(wavSpec{enc: encIntLE, size: 2}); b[22], b[23] = 0, 0; return b }(), ErrCorrupt},
		{"wav zero rate", func() []byte {
			b := buildWAV(wavSpec{enc: encIntLE, size: 2})
			copy(b[24:28], []byte{0, 0, 0, 0})
			return b
		}(), ErrCorrupt},
		{"aiff without comm", []byte("FORM\x00\x00\x00\x04AIFF"), ErrCorrupt},
		{"flac header only", []byte("fLaC"), ErrCorrupt},
		{"ogg header only", []byte("OggS\x00\x02"), ErrUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, probe := range []bool{false, true} {
				pcm, _, err := readBytes(tt.data, options{probe: probe})
				if !errors.Is(err, tt.want) {
					t.Errorf("probe=%v: err = %v, want %v", probe, err, tt.want)
				}
				if pcm != nil {
					t.Errorf("probe=%v: got PCM alongside an error", probe)
				}
			}
		})
	}
}

func TestEmptyData(t *testing.T) {
	data := buildWAV(wavSpec{enc: encIntLE, size: 2, frames: 1})
	data = data[:len(data)-4] // drop the single frame
	data[len(data)-4] = 0     // and say so in the data size
	pcm, info, err := readBytes(data, options{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Frames != 0 || pcm.Frames != 0 || info.Duration() != 0 {
		t.Errorf("frames = %d/%d", info.Frames, pcm.Frames)
	}
	if got := pcm.Mono(); len(got) != 0 {
		t.Errorf("mono = %v", got)
	}
}

func TestDecodeFilesAndLimits(t *testing.T) {
	path := writeTemp(t, "loop.bin", buildWAV(wavSpec{enc: encIntLE, size: 2})) // extension must not matter

	if f, err := Sniff(path); err != nil || f != WAV {
		t.Errorf("Sniff = %q, %v", f, err)
	}
	info, err := Probe(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Frames != fixFrames || math.Abs(info.Duration()-0.25) > 1e-9 {
		t.Errorf("probe = %+v", info)
	}
	pcm, _, err := Decode(path)
	if err != nil {
		t.Fatal(err)
	}
	checkTones(t, pcm, 1e-3)

	short, info, err := DecodeN(path, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if short.Frames != 1000 || len(short.Data[1]) != 1000 || info.Frames != fixFrames {
		t.Errorf("DecodeN: %d frames, info %d", short.Frames, info.Frames)
	}

	if _, _, err := Decode(filepath.Join(t.TempDir(), "nope.wav")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: err = %v", err)
	}
	if _, err := Probe(t.TempDir()); !errors.Is(err, ErrUnknownFormat) {
		t.Errorf("directory: err = %v", err)
	}

	// The limit applies to every decoder.
	for name, data := range map[string][]byte{
		"flac": buildFLAC(t, 16, 2),
		"mp3":  testdata(t, "mono.mp3"),
		"ogg":  testdata(t, "stereo.ogg"),
		"aiff": buildAIFF(aiffSpec{enc: encIntBE, size: 2}),
	} {
		pcm, info, err := readBytes(data, options{maxFrames: 5000})
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if pcm.Frames != 5000 || len(pcm.Data[0]) != 5000 || info.Frames <= 5000 {
			t.Errorf("%s: %d frames decoded, info says %d", name, pcm.Frames, info.Frames)
		}
	}
}

func TestMonoAndStereo(t *testing.T) {
	p := &PCM{Rate: 48000, Channels: 2, Frames: 3, Data: [][]float32{{1, 0, -1}, {0, 0.5, -1}}}
	if got, want := p.Mono(), []float32{0.5, 0.25, -1}; !reflect.DeepEqual(got, want) {
		t.Errorf("Mono = %v, want %v", got, want)
	}
	if p.Stereo() != p {
		t.Error("Stereo copied a stereo signal")
	}
	mono := &PCM{Rate: 48000, Channels: 1, Frames: 2, Data: [][]float32{{0.1, 0.2}}}
	if &mono.Mono()[0] != &mono.Data[0][0] {
		t.Error("Mono copied a mono signal")
	}

	// 5.1: FL FR FC LFE BL BR. Full scale everywhere must stay at full
	// scale, and the LFE must not leak.
	six := newPCM(48000, 6, 1)
	for c := range six.Data {
		six.Data[c][0] = 1
	}
	st := six.Stereo()
	if st.Channels != 2 || st.Frames != 1 || math.Abs(float64(st.Data[0][0])-1) > 1e-6 || math.Abs(float64(st.Data[1][0])-1) > 1e-6 {
		t.Errorf("5.1 fold-down = %v", st.Data)
	}
	lfe := newPCM(48000, 6, 1)
	lfe.Data[3][0] = 1
	if st := lfe.Stereo(); st.Data[0][0] != 0 || st.Data[1][0] != 0 {
		t.Errorf("LFE leaked: %v", st.Data)
	}
	left := newPCM(48000, 6, 1)
	left.Data[0][0], left.Data[4][0] = 1, 1
	if st := left.Stereo(); math.Abs(float64(st.Data[0][0])-1) > 1e-6 || st.Data[1][0] != 0 {
		t.Errorf("left channels fold-down = %v", st.Data)
	}

	// Content in the front pair only, the usual case, keeps its level: it
	// must play as loud as the same material in a stereo file.
	for _, channels := range []int{3, 4, 5, 6, 7, 8} {
		front := newPCM(48000, channels, 2)
		front.Data[0][0], front.Data[1][0] = 0.9, -0.6
		front.Data[0][1], front.Data[1][1] = 0.1, 0.2
		st := front.Stereo()
		if st.Data[0][0] != 0.9 || st.Data[1][0] != -0.6 || st.Data[0][1] != 0.1 || st.Data[1][1] != 0.2 {
			t.Errorf("%d channels, front pair only: fold-down = %v", channels, st.Data)
		}
	}
	// A sum over full scale turns both sides down by the same gain, and a
	// quiet frame elsewhere in the clip with it.
	hot := newPCM(48000, 6, 2)
	hot.Data[0][0], hot.Data[2][0], hot.Data[1][0] = 0.8, 0.8, 0.2 // L = 0.8 + 0.8/sqrt2, R = 0.2 + 0.8/sqrt2
	hot.Data[1][1] = 0.5
	st = hot.Stereo()
	sumL, sumR := 0.8+0.8*math.Sqrt2/2, 0.2+0.8*math.Sqrt2/2
	if l, r, q := float64(st.Data[0][0]), float64(st.Data[1][0]), float64(st.Data[1][1]); math.Abs(l-1) > 1e-6 || math.Abs(r-sumR/sumL) > 1e-6 || math.Abs(q-0.5/sumL) > 1e-6 {
		t.Errorf("hot fold-down = %v", st.Data)
	}
	// Float audio already over full scale is not pulled down to it.
	over := newPCM(48000, 4, 1)
	over.Data[0][0] = 1.5
	if st := over.Stereo(); st.Data[0][0] != 1.5 {
		t.Errorf("over-range float fold-down = %v", st.Data)
	}

	// Layouts without a table alternate sides.
	wide := newPCM(48000, 10, 1)
	wide.Data[0][0], wide.Data[9][0] = 1, 1
	if st := wide.Stereo(); math.Abs(float64(st.Data[0][0])-0.2) > 1e-6 || math.Abs(float64(st.Data[1][0])-0.2) > 1e-6 {
		t.Errorf("10 channel fold-down = %v", st.Data)
	}
}

// A streaming writer leaves the data size at 0xFFFFFFFF (or zero) and may
// still append chunks after the audio. Those are metadata, not samples.
func TestWAVUnsizedDataWithTrailingChunks(t *testing.T) {
	trailer := [][]byte{smplChunk(60, [2]uint32{100, 4000}), acidChunk(2, 57, 8, 128)}
	for _, tt := range []struct {
		name string
		spec wavSpec
	}{
		{"placeholder", wavSpec{enc: encIntLE, size: 2, channels: 1, frames: 4410, dataSize: u32p(math.MaxUint32)}},
		{"zero", wavSpec{enc: encIntLE, size: 2, channels: 2, frames: 4410, dataSize: u32p(0)}},
		{"overstated", wavSpec{enc: encIntLE, size: 3, channels: 2, frames: 4410, dataSize: u32p(4410*6 + 5000)}},
		{"odd length, padded", wavSpec{enc: encU8, size: 1, channels: 1, frames: 4411, dataSize: u32p(math.MaxUint32)}},
		{"odd length, no pad", wavSpec{enc: encU8, size: 1, channels: 1, frames: 4411, noDataPad: true, dataSize: u32p(math.MaxUint32)}},
		{"24-bit mono, odd", wavSpec{enc: encIntLE, size: 3, channels: 1, frames: 4411, dataSize: u32p(math.MaxUint32)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			spec := tt.spec
			spec.after = trailer
			pcm, info, err := readBytes(buildWAV(spec), options{})
			if err != nil {
				t.Fatal(err)
			}
			if info.Frames != spec.frames || pcm.Frames != spec.frames {
				t.Errorf("%d frames (decoded %d), want %d: trailing chunks read as audio", info.Frames, pcm.Frames, spec.frames)
			}
			if want := []Loop{{100, 4001}}; !reflect.DeepEqual(info.Meta.Loops, want) {
				t.Errorf("loops = %v, want %v", info.Meta.Loops, want)
			}
			if !info.Meta.HasAcid || info.Meta.Tempo != 128 {
				t.Errorf("acid chunk lost: %+v", info.Meta)
			}
			checkTones(t, pcm, 1e-2)

			// Without trailing chunks, and cut short, the audio still runs
			// to the end of the file (which takes in a final pad byte:
			// nothing tells it apart from one more 8-bit sample).
			spec.after = nil
			plain := buildWAV(spec)
			stride := spec.size * spec.channels
			const header = 44
			if _, info, err := readBytes(plain, options{probe: true}); err != nil || info.Frames != (len(plain)-header)/stride {
				t.Errorf("no trailer: %d frames, err %v", info.Frames, err)
			}
			cut := plain[:len(plain)-10*stride]
			if _, info, err := readBytes(cut, options{probe: true}); err != nil || info.Frames != (len(cut)-header)/stride {
				t.Errorf("cut short: %d frames, err %v", info.Frames, err)
			}
		})
	}
}

// Sample bytes that happen to spell a chunk header do not end the audio.
func TestWAVUnsizedDataIgnoresLookalikes(t *testing.T) {
	spec := wavSpec{enc: encIntLE, size: 2, channels: 1, frames: 4410, dataSize: u32p(math.MaxUint32)}
	data := buildWAV(spec)
	// A plausible header near the end whose size does not reach the end of
	// the file exactly, and one that overshoots it.
	at := len(data) - 400
	copy(data[at:], "LIST")
	binary.LittleEndian.PutUint32(data[at+4:], 100)
	copy(data[at+200:], "smpl")
	binary.LittleEndian.PutUint32(data[at+204:], 5000)
	if _, info, err := readBytes(data, options{probe: true}); err != nil || info.Frames != spec.frames {
		t.Errorf("%d frames, err %v; want %d", info.Frames, err, spec.frames)
	}
}

func TestExtended(t *testing.T) {
	tests := []struct {
		in   []byte
		want float64
	}{
		{[]byte{0x40, 0x0e, 0xac, 0x44, 0, 0, 0, 0, 0, 0}, 44100},
		{[]byte{0x40, 0x0e, 0xbb, 0x80, 0, 0, 0, 0, 0, 0}, 48000},
		{[]byte{0x40, 0x0f, 0xbb, 0x80, 0, 0, 0, 0, 0, 0}, 96000},
		{[]byte{0x40, 0x0b, 0xfa, 0, 0, 0, 0, 0, 0, 0}, 8000},
		{[]byte{0x40, 0x0d, 0xac, 0x44, 0, 0, 0, 0, 0, 0}, 22050},
		{make([]byte, 10), 0},
		{[]byte{0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, 0},
	}
	for _, tt := range tests {
		if got := extended(tt.in); got != tt.want {
			t.Errorf("extended(% x) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
