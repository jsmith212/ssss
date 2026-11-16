package audio

import (
	"fmt"
	"io"
	"math"
	"os"
)

// maxDecodeSamples caps how much one decode may hold in memory: 2^28 samples
// is 1 GiB of float32, about 50 minutes of 44.1 kHz stereo. Longer files are
// cut at the cap; Info.Frames still reports their full length.
const maxDecodeSamples = 1 << 28

type options struct {
	maxFrames  int   // 0 = no limit beyond maxDecodeSamples
	maxSeconds int   // as maxFrames, for callers that do not know the rate yet
	probe      bool  // headers only; return no PCM
	hold       *hold // claims the decode's memory from a budget; nil = unbounded
}

// limit returns how many of frames to decode.
func (o options) limit(frames, channels, rate int) int {
	if channels > 0 {
		frames = min(frames, maxDecodeSamples/channels)
	}
	if o.maxFrames > 0 {
		frames = min(frames, o.maxFrames)
	}
	if o.maxSeconds > 0 && rate > 0 && rate <= math.MaxInt32 {
		frames = min(frames, o.maxSeconds*rate)
	}
	return frames
}

// Decode reads the whole file at path. See DecodeN.
func Decode(path string) (*PCM, Info, error) {
	return DecodeN(path, 0)
}

// DecodeN decodes at most maxFrames frames from the start of the file at
// path; 0 means everything, up to a 2^28-sample memory cap. Info describes
// the whole file, so pcm.Frames < info.Frames means the audio was cut.
//
// Leniency differs by format, following what is seen in the wild: a WAV or
// AIFF whose data chunk overstates its size, and an MP3 with a damaged tail,
// decode as far as they go; a FLAC or Vorbis stream that ends early is an
// error.
//
// Decodes across the process share a memory budget, so a call may wait for
// others to finish. The budget covers only the decode itself; a caller that
// goes on to work on long audio should use DecodeHeld.
func DecodeN(path string, maxFrames int) (*PCM, Info, error) {
	pcm, info, release, err := DecodeHeld(path, maxFrames)
	release()
	return pcm, info, err
}

// DecodeHeld is DecodeN for callers that keep working on the audio, as
// analysis does. The decode's share of the process-wide memory budget stays
// claimed until release is called, which keeps a pool of workers from holding
// many long recordings in memory at once. release is never nil and must be
// called once the PCM, and anything of similar size derived from it, is no
// longer needed. Do not start another decode before releasing: two callers
// each holding one claim and waiting for a second would wait for ever.
func DecodeHeld(path string, maxFrames int) (pcm *PCM, info Info, release func(), err error) {
	h := &hold{b: decodeBudget}
	pcm, info, err = openAndRead(path, options{maxFrames: maxFrames, hold: h})
	return pcm, info, h.release, err
}

// Probe reports a file's format, length and chunk metadata without decoding
// samples. WAV, AIFF, Vorbis and FLAC need only their headers (a FLAC that
// does not state its length is decoded to count it); an MP3 is read through
// so its frames can be counted, but not decoded.
func Probe(path string) (Info, error) {
	_, info, err := openAndRead(path, options{probe: true})
	return info, err
}

// Sniff identifies the container of the file at path from its content.
func Sniff(path string) (Format, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	return detect(f, st.Size())
}

func openAndRead(path string, opt options) (*PCM, Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, Info{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, Info{}, err
	}
	if st.IsDir() {
		return nil, Info{}, fmt.Errorf("%s is a directory: %w", path, ErrUnknownFormat)
	}
	pcm, info, err := read(f, st.Size(), opt)
	if err != nil {
		opt.hold.release()
	}
	return pcm, info, err
}

// read decodes from any random-access source. It never panics: the
// third-party decoders are not hardened against hostile input, so a panic
// inside them is reported as a corrupt file.
func read(r io.ReaderAt, size int64, opt options) (pcm *PCM, info Info, err error) {
	format, err := detect(r, size)
	if err != nil {
		return nil, Info{}, err
	}
	defer func() {
		if p := recover(); p != nil {
			pcm, info = nil, Info{}
			err = corrupt(format, "decoder panic: %v", p)
		}
	}()

	switch format {
	case WAV:
		pcm, info, err = readWAV(r, size, opt)
	case AIFF:
		pcm, info, err = readAIFF(r, size, opt)
	case FLAC:
		pcm, info, err = readFLAC(compressedSource(r, size, opt.probe), opt)
	case OGG:
		pcm, info, err = readOGG(compressedSource(r, size, opt.probe), opt)
	case MP3:
		pcm, info, err = readMP3(compressedSource(r, size, false), size, opt)
	}
	if err != nil {
		return nil, Info{}, err
	}
	info.Format = format
	info.Meta.Loops = validLoops(info.Meta.Loops, info.Frames)
	return pcm, info, nil
}

// readRaw decodes frames frames of uncompressed audio starting at off.
func readRaw(r io.ReaderAt, off int64, frames int, l layout, rate int, h *hold) (*PCM, error) {
	h.reserve(frames, l.channels)
	pcm := newPCM(rate, l.channels, frames)
	stride := l.frameSize()
	block := max(1, (1<<20)/stride)
	buf := make([]byte, min(block, max(frames, 1))*stride)
	for done := 0; done < frames; {
		n := min(block, frames-done)
		chunk := buf[:n*stride]
		if _, err := r.ReadAt(chunk, off+int64(done)*int64(stride)); err != nil {
			return nil, fmt.Errorf("reading samples: %w", err)
		}
		l.convert(pcm.Data, done, chunk)
		done += n
	}
	return pcm, nil
}

// validLoops drops loops that do not fit the file. Some writers store an
// exclusive end where the smpl chunk wants an inclusive one, which lands one
// frame past the end once converted; that is clamped, not dropped.
func validLoops(loops []Loop, frames int) []Loop {
	var out []Loop
	for _, l := range loops {
		if l.End == frames+1 {
			l.End = frames
		}
		if l.Start >= 0 && l.Start < l.End && l.End <= frames {
			out = append(out, l)
		}
	}
	return out
}
