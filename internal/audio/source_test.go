package audio

import (
	"bytes"
	"io"
	"math/rand/v2"
	"slices"
	"testing"
)

// zeros is an endless file of zero bytes that counts what is read from it.
type zeros struct{ read int64 }

func (z *zeros) ReadAt(p []byte, _ int64) (int, error) {
	clear(p)
	z.read += int64(len(p))
	return len(p), nil
}

// A large compressed file must not be pulled into memory to be decoded.
func TestCompressedSourceDoesNotLoadLargeFiles(t *testing.T) {
	var z zeros
	if _, ok := compressedSource(&z, maxInMemory+1, false).(*windowReader); !ok || z.read != 0 {
		t.Errorf("a file over maxInMemory was read up front (%d bytes)", z.read)
	}
	if _, ok := compressedSource(&z, 1<<20, true).(*windowReader); !ok || z.read != 0 {
		t.Errorf("a probe read the file up front (%d bytes)", z.read)
	}
	if _, ok := compressedSource(&z, 1<<20, false).(*bytes.Reader); !ok {
		t.Error("a small file is not decoded from memory")
	}
}

// The decoders must produce the same audio through the window as from memory.
func TestWindowedDecodeMatchesInMemory(t *testing.T) {
	files := map[string]struct {
		data []byte
		read func(src io.ReadSeeker, size int64, opt options) (*PCM, Info, error)
	}{
		"flac": {buildFLAC(t, 16, 2), func(src io.ReadSeeker, _ int64, opt options) (*PCM, Info, error) { return readFLAC(src, opt) }},
		"ogg":  {testdata(t, "stereo.ogg"), func(src io.ReadSeeker, _ int64, opt options) (*PCM, Info, error) { return readOGG(src, opt) }},
		"mp3":  {testdata(t, "stereo_id3.mp3"), readMP3},
	}
	for name, f := range files {
		t.Run(name, func(t *testing.T) {
			size := int64(len(f.data))
			for _, opt := range []options{{}, {maxFrames: 1000}, {probe: true}} {
				want, wantInfo, err := f.read(bytes.NewReader(f.data), size, opt)
				if err != nil {
					t.Fatal(err)
				}
				got, gotInfo, err := f.read(&windowReader{r: bytes.NewReader(f.data), size: size}, size, opt)
				if err != nil {
					t.Fatal(err)
				}
				if gotInfo.Frames != wantInfo.Frames || gotInfo.Rate != wantInfo.Rate || gotInfo.Channels != wantInfo.Channels {
					t.Errorf("%+v: info %+v, want %+v", opt, gotInfo, wantInfo)
				}
				if opt.probe {
					continue
				}
				if got.Frames != want.Frames || got.Frames == 0 {
					t.Fatalf("%+v: %d frames, want %d", opt, got.Frames, want.Frames)
				}
				for c := range want.Data {
					if !slices.Equal(got.Data[c], want.Data[c]) {
						t.Errorf("%+v: channel %d differs", opt, c)
					}
				}
			}
		})
	}
}

// windowReader must behave like bytes.Reader under any mix of reads and seeks.
func TestWindowReaderMatchesBytesReader(t *testing.T) {
	data := make([]byte, 3*windowSize+12345)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range data {
		data[i] = byte(rng.Uint32())
	}
	want := bytes.NewReader(data)
	got := &windowReader{r: bytes.NewReader(data), size: int64(len(data))}
	a, b := make([]byte, 2*windowSize), make([]byte, 2*windowSize)
	for step := range 5000 {
		switch rng.IntN(4) {
		case 0:
			whence := rng.IntN(3)
			off := rng.Int64N(int64(len(data)))
			if whence != io.SeekStart {
				off = rng.Int64N(4*windowSize) - 2*windowSize
			}
			wp, werr := want.Seek(off, whence)
			gp, gerr := got.Seek(off, whence)
			if (werr == nil) != (gerr == nil) || (werr == nil && wp != gp) {
				t.Fatalf("step %d: Seek(%d, %d) = %d, %v; want %d, %v", step, off, whence, gp, gerr, wp, werr)
			}
		case 1:
			n := rng.IntN(len(a))
			off := rng.Int64N(int64(len(data)) + 100)
			wn, werr := want.ReadAt(a[:n], off)
			gn, gerr := got.ReadAt(b[:n], off)
			if wn != gn || (werr == nil) != (gerr == nil) || !bytes.Equal(a[:wn], b[:gn]) {
				t.Fatalf("step %d: ReadAt(%d, %d) = %d, %v; want %d, %v", step, n, off, gn, gerr, wn, werr)
			}
		default:
			n := rng.IntN(300)
			if rng.IntN(20) == 0 {
				n = rng.IntN(len(a))
			}
			wn, werr := io.ReadFull(want, a[:n])
			gn, gerr := io.ReadFull(got, b[:n])
			if wn != gn || werr != gerr || !bytes.Equal(a[:wn], b[:gn]) {
				t.Fatalf("step %d: read %d = %d, %v; want %d, %v", step, n, gn, gerr, wn, werr)
			}
		}
	}
}

// A file that shrinks under the reader is an error, not an endless loop.
func TestWindowReaderShortFile(t *testing.T) {
	w := &windowReader{r: bytes.NewReader(make([]byte, 100)), size: 1000}
	n, err := io.Copy(io.Discard, w)
	if n != 100 || err != io.ErrUnexpectedEOF {
		t.Errorf("copied %d bytes, err %v; want 100, unexpected EOF", n, err)
	}
}
