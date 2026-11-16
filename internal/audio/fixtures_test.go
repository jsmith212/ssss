package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

const (
	fixRate   = 44100
	fixFrames = 11025 // 0.25 s: a whole number of cycles for every test tone
)

// tone describes the sine in channel c of every generated fixture.
func tone(c int) (freq, amp float64) {
	return 440 * float64(c+1), 0.5 / float64(c+1)
}

func sines(channels, frames, rate int) [][]float64 {
	out := make([][]float64, channels)
	for c := range out {
		freq, amp := tone(c)
		out[c] = make([]float64, frames)
		for i := range out[c] {
			out[c][i] = amp * math.Sin(2*math.Pi*freq*float64(i)/float64(rate))
		}
	}
	return out
}

// toneAmp measures the amplitude of the freq component of x by correlation.
func toneAmp(x []float32, rate int, freq float64) float64 {
	var s, c float64
	for i, v := range x {
		ph := 2 * math.Pi * freq * float64(i) / float64(rate)
		s += float64(v) * math.Sin(ph)
		c += float64(v) * math.Cos(ph)
	}
	return 2 * math.Hypot(s, c) / float64(len(x))
}

// tonePhase returns the phase of the freq component of x relative to a sine
// starting at zero.
func tonePhase(x []float32, rate int, freq float64) float64 {
	var s, c float64
	for i, v := range x {
		ph := 2 * math.Pi * freq * float64(i) / float64(rate)
		s += float64(v) * math.Sin(ph)
		c += float64(v) * math.Cos(ph)
	}
	return math.Atan2(c, s)
}

func peakOf(x []float32) float64 {
	var p float64
	for _, v := range x {
		p = max(p, math.Abs(float64(v)))
	}
	return p
}

// checkTones asserts that every channel holds its fixture sine.
func checkTones(t *testing.T, p *PCM, tol float64) {
	t.Helper()
	for c, ch := range p.Data {
		if len(ch) != p.Frames {
			t.Fatalf("channel %d has %d samples, want %d", c, len(ch), p.Frames)
		}
		freq, amp := tone(c)
		if got := toneAmp(ch, p.Rate, freq); math.Abs(got-amp) > tol {
			t.Errorf("channel %d: %.0f Hz amplitude %.4f, want %.4f ± %.4f", c, freq, got, amp, tol)
		}
		if got := peakOf(ch); got > amp+tol || got < amp-tol {
			t.Errorf("channel %d: peak %.4f, want %.4f ± %.4f", c, got, amp, tol)
		}
	}
}

// encodeSamples interleaves signal in the given sample encoding. It is
// written independently of layout.convert so the two check each other.
func encodeSamples(enc encoding, size int, signal [][]float64) []byte {
	var b bytes.Buffer
	for i := range signal[0] {
		for c := range signal {
			v := signal[c][i]
			full := math.Round(v * float64(int64(1)<<(8*size-1)))
			full = min(full, float64(int64(1)<<(8*size-1)-1))
			n := int64(full)
			switch enc {
			case encU8:
				b.WriteByte(byte(n + 128))
			case encS8:
				b.WriteByte(byte(int8(n)))
			case encIntLE:
				for k := range size {
					b.WriteByte(byte(n >> (8 * k)))
				}
			case encIntBE:
				for k := size - 1; k >= 0; k-- {
					b.WriteByte(byte(n >> (8 * k)))
				}
			case encF32LE:
				binary.Write(&b, binary.LittleEndian, float32(v))
			case encF32BE:
				binary.Write(&b, binary.BigEndian, float32(v))
			case encF64LE:
				binary.Write(&b, binary.LittleEndian, v)
			case encF64BE:
				binary.Write(&b, binary.BigEndian, v)
			}
		}
	}
	return b.Bytes()
}

// riffChunk builds a little-endian chunk. pad controls whether an odd body
// gets its alignment byte, so tests can leave it out.
func riffChunk(id string, body []byte, pad bool) []byte {
	b := append([]byte(id), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(body)))
	b = append(b, body...)
	if pad && len(body)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

type wavSpec struct {
	enc        encoding
	size       int // bytes per sample
	channels   int
	frames     int
	rate       int
	extensible bool
	validBits  int
	before     [][]byte // chunks between fmt and data
	after      [][]byte // chunks after data
	dataSize   *uint32  // overrides the data chunk's size field
	noDataPad  bool
	rf64       bool
}

func (s wavSpec) withDefaults() wavSpec {
	if s.channels == 0 {
		s.channels = 2
	}
	if s.frames == 0 {
		s.frames = fixFrames
	}
	if s.rate == 0 {
		s.rate = fixRate
	}
	return s
}

func buildWAV(s wavSpec) []byte {
	s = s.withDefaults()
	tag := uint16(wavPCM)
	if s.enc == encF32LE || s.enc == encF64LE {
		tag = wavFloat
	}
	bits := s.size * 8
	valid := bits
	if s.validBits != 0 {
		valid = s.validBits
	}
	fmtBody := make([]byte, 16)
	binary.LittleEndian.PutUint16(fmtBody[0:], tag)
	binary.LittleEndian.PutUint16(fmtBody[2:], uint16(s.channels))
	binary.LittleEndian.PutUint32(fmtBody[4:], uint32(s.rate))
	binary.LittleEndian.PutUint32(fmtBody[8:], uint32(s.rate*s.channels*s.size))
	binary.LittleEndian.PutUint16(fmtBody[12:], uint16(s.channels*s.size))
	binary.LittleEndian.PutUint16(fmtBody[14:], uint16(bits))
	if s.extensible {
		binary.LittleEndian.PutUint16(fmtBody[0:], wavExtensible)
		ext := make([]byte, 24)
		binary.LittleEndian.PutUint16(ext[0:], 22)
		binary.LittleEndian.PutUint16(ext[2:], uint16(valid))
		binary.LittleEndian.PutUint32(ext[4:], 0) // channel mask
		binary.LittleEndian.PutUint16(ext[8:], tag)
		copy(ext[10:], []byte{0x00, 0x00, 0x00, 0x00, 0x10, 0x00, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71})
		fmtBody = append(fmtBody, ext...)
	}

	data := encodeSamples(s.enc, s.size, sines(s.channels, s.frames, s.rate))
	dataChunk := riffChunk("data", data, !s.noDataPad)
	if s.dataSize != nil {
		binary.LittleEndian.PutUint32(dataChunk[4:], *s.dataSize)
	}

	var body bytes.Buffer
	body.WriteString("WAVE")
	if s.rf64 {
		ds64 := make([]byte, 28)
		binary.LittleEndian.PutUint64(ds64[8:], uint64(len(data)))
		binary.LittleEndian.PutUint64(ds64[16:], uint64(s.frames))
		body.Write(riffChunk("ds64", ds64, true))
	}
	body.Write(riffChunk("fmt ", fmtBody, true))
	for _, c := range s.before {
		body.Write(c)
	}
	body.Write(dataChunk)
	for _, c := range s.after {
		body.Write(c)
	}

	out := riffChunk("RIFF", body.Bytes(), false)
	if s.rf64 {
		copy(out, "RF64")
		binary.LittleEndian.PutUint32(out[4:], math.MaxUint32)
	}
	return out
}

func acidChunk(flags uint32, root uint16, beats uint32, tempo float32) []byte {
	b := make([]byte, 24)
	binary.LittleEndian.PutUint32(b[0:], flags)
	binary.LittleEndian.PutUint16(b[4:], root)
	binary.LittleEndian.PutUint16(b[6:], 0x8000)
	binary.LittleEndian.PutUint32(b[12:], beats)
	binary.LittleEndian.PutUint16(b[16:], 4)
	binary.LittleEndian.PutUint16(b[18:], 4)
	binary.LittleEndian.PutUint32(b[20:], math.Float32bits(tempo))
	return riffChunk("acid", b, true)
}

// smplChunk stores loops with inclusive end points, as the format requires.
func smplChunk(unity uint32, loops ...[2]uint32) []byte {
	b := make([]byte, 36+24*len(loops))
	binary.LittleEndian.PutUint32(b[12:], unity)
	binary.LittleEndian.PutUint32(b[28:], uint32(len(loops)))
	for i, l := range loops {
		rec := b[36+24*i:]
		binary.LittleEndian.PutUint32(rec[8:], l[0])
		binary.LittleEndian.PutUint32(rec[12:], l[1])
	}
	return riffChunk("smpl", b, true)
}

func iffChunk(id string, body []byte) []byte {
	b := append([]byte(id), 0, 0, 0, 0)
	binary.BigEndian.PutUint32(b[4:], uint32(len(body)))
	b = append(b, body...)
	if len(body)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

type aiffSpec struct {
	enc       encoding
	size      int
	channels  int
	codec     string // "" = plain AIFF
	ssndFirst bool
	ssndSkip  int      // SSND offset field
	extra     [][]byte // chunks after COMM
}

func buildAIFF(s aiffSpec) []byte {
	if s.channels == 0 {
		s.channels = 2
	}
	comm := make([]byte, 18)
	binary.BigEndian.PutUint16(comm[0:], uint16(s.channels))
	binary.BigEndian.PutUint32(comm[2:], fixFrames)
	binary.BigEndian.PutUint16(comm[6:], uint16(s.size*8))
	// 44100 as an 80-bit extended float.
	copy(comm[8:], []byte{0x40, 0x0e, 0xac, 0x44, 0, 0, 0, 0, 0, 0})
	form := "AIFF"
	if s.codec != "" {
		form = "AIFC"
		comm = append(comm, s.codec...)
		comm = append(comm, 0, 0) // empty Pascal string, padded
	}
	ssnd := make([]byte, 8+s.ssndSkip)
	binary.BigEndian.PutUint32(ssnd[0:], uint32(s.ssndSkip))
	ssnd = append(ssnd, encodeSamples(s.enc, s.size, sines(s.channels, fixFrames, fixRate))...)

	var body bytes.Buffer
	body.WriteString(form)
	if s.ssndFirst {
		body.Write(iffChunk("SSND", ssnd))
	}
	body.Write(iffChunk("COMM", comm))
	for _, c := range s.extra {
		body.Write(c)
	}
	if !s.ssndFirst {
		body.Write(iffChunk("SSND", ssnd))
	}
	return iffChunk("FORM", body.Bytes())
}

func markChunk(markers map[uint16]uint32) []byte {
	b := binary.BigEndian.AppendUint16(nil, uint16(len(markers)))
	for id, pos := range markers {
		b = binary.BigEndian.AppendUint16(b, id)
		b = binary.BigEndian.AppendUint32(b, pos)
		b = append(b, 3, 'l', 'p', 'x') // odd-length name: no pad needed
	}
	return iffChunk("MARK", b)
}

func instChunk(baseNote byte, playMode, begin, end uint16) []byte {
	b := make([]byte, 20)
	b[0] = baseNote
	binary.BigEndian.PutUint16(b[8:], playMode)
	binary.BigEndian.PutUint16(b[10:], begin)
	binary.BigEndian.PutUint16(b[12:], end)
	return iffChunk("INST", b)
}

// buildFLAC encodes the fixture tones with the pure-Go encoder.
func buildFLAC(t testing.TB, bits, channels int) []byte {
	t.Helper()
	signal := sines(channels, fixFrames, fixRate)
	info := &meta.StreamInfo{
		BlockSizeMin:  4096,
		BlockSizeMax:  4096,
		SampleRate:    fixRate,
		NChannels:     uint8(channels),
		BitsPerSample: uint8(bits),
		NSamples:      fixFrames,
	}
	var buf bytes.Buffer
	enc, err := flac.NewEncoder(&buf, info)
	if err != nil {
		t.Fatal(err)
	}
	layout := frame.ChannelsMono
	if channels == 2 {
		layout = frame.ChannelsLR
	}
	for start := 0; start < fixFrames; start += 4096 {
		n := min(4096, fixFrames-start)
		f := &frame.Frame{Header: frame.Header{
			HasFixedBlockSize: true,
			BlockSize:         uint16(n),
			SampleRate:        fixRate,
			Channels:          layout,
			BitsPerSample:     uint8(bits),
		}}
		for c := range channels {
			samples := make([]int32, n)
			for i := range samples {
				samples[i] = int32(math.Round(signal[c][start+i] * float64(int64(1)<<(bits-1))))
			}
			f.Subframes = append(f.Subframes, &frame.Subframe{
				SubHeader: frame.SubHeader{Pred: frame.PredVerbatim},
				Samples:   samples,
				NSamples:  n,
			})
		}
		if err := enc.WriteFrame(f); err != nil {
			t.Fatal(err)
		}
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeTemp(t testing.TB, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readBytes(data []byte, opt options) (*PCM, Info, error) {
	return read(bytes.NewReader(data), int64(len(data)), opt)
}

func testdata(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func u32p(v uint32) *uint32 { return &v }
