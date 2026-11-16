package audio

import (
	"encoding/binary"
	"io"

	"github.com/hajimehoshi/go-mp3"
)

// mp3DecoderDelay is the latency of the MPEG synthesis filterbank in
// samples, which every decoder adds on top of the encoder's own delay.
const mp3DecoderDelay = 529

func readMP3(src io.ReadSeeker, size int64, opt options) (*PCM, Info, error) {
	// The decoder always produces stereo and knows nothing of gapless
	// playback, so the first frame is inspected here for both.
	var (
		first   mp3Header
		skip    int // decoded frames to drop from the start
		trim    int // and from the end
		located bool
	)
	if ra, ok := src.(io.ReaderAt); ok {
		start := int64(0)
		var head [10]byte
		if _, err := ra.ReadAt(head[:], 0); err == nil && string(head[:3]) == "ID3" {
			start = id3v2Size(head[:])
		}
		var off int64
		if first, off, located = findMP3Frame(ra, start, size); located {
			skip, trim = mp3Gapless(ra, off, first)
		}
	}

	dec, err := mp3.NewDecoder(src)
	if err != nil {
		return nil, Info{}, corrupt(MP3, "%v", err)
	}
	rate := dec.SampleRate()
	if rate < 1 {
		return nil, Info{}, corrupt(MP3, "sample rate %d", rate)
	}
	mono := located && first.mono
	channels := 2
	if mono {
		channels = 1
	}
	// Length is in bytes of 16-bit stereo output.
	total := int(max(dec.Length(), 0) / 4)
	if total > 0 {
		if total <= skip+trim {
			skip, trim = 0, 0 // the tag does not describe this stream
		}
		total -= skip + trim
	}
	info := Info{Rate: rate, Channels: channels, Frames: total, Meta: newMeta()}
	if opt.probe {
		return nil, info, nil
	}

	limit := opt.limit(maxDecodeSamples, channels, rate)
	if total > 0 {
		limit = min(limit, total)
	}
	opt.hold.reserve(limit, channels)
	data := make([][]float32, channels)
	for c := range data {
		if total > 0 {
			// Counted from the frames in the file, so it can be trusted.
			data[c] = make([]float32, 0, limit)
		} else {
			data[c] = make([]float32, 0, min(limit, 1<<22))
		}
	}
	buf := make([]byte, 64<<10)
	frames := 0
	for frames < limit {
		n, err := io.ReadFull(dec, buf)
		got := buf[:n-n%4]
		if drop := min(skip, len(got)/4); drop > 0 {
			got = got[drop*4:]
			skip -= drop
		}
		take := min(len(got)/4, limit-frames)
		for c := range data {
			data[c] = room(data[c], take, limit)
		}
		for i := range take {
			s := got[i*4:]
			data[0] = append(data[0], float32(int16(binary.LittleEndian.Uint16(s)))/(1<<15))
			if !mono {
				data[1] = append(data[1], float32(int16(binary.LittleEndian.Uint16(s[2:])))/(1<<15))
			}
		}
		frames += take
		if err != nil {
			// MP3s routinely end in tags or a cut frame; keep what decoded.
			if frames == 0 && err != io.EOF && err != io.ErrUnexpectedEOF {
				return nil, Info{}, corrupt(MP3, "%v", err)
			}
			break
		}
	}
	if total == 0 || frames < limit {
		info.Frames = frames
	}
	return &PCM{Rate: rate, Channels: channels, Frames: frames, Data: data}, info, nil
}

// mp3Gapless reads the Xing/Info tag frame, if the stream starts with one,
// and returns how many decoded frames to drop at each end so the audio lines
// up with what was encoded. That matters for loops: untrimmed, an MP3 is
// tens of milliseconds longer than its source and starts with silence.
//
// The tag frame itself decodes to one frame of silence. The LAME extension
// that follows it gives the encoder delay and end padding.
func mp3Gapless(r io.ReaderAt, off int64, h mp3Header) (skip, trim int) {
	frame := make([]byte, min(h.frameLen, 4+h.sideInfo+160))
	if _, err := r.ReadAt(frame, off); err != nil {
		return 0, 0
	}
	tag := frame[4+h.sideInfo:]
	if len(tag) < 8 || (string(tag[:4]) != "Xing" && string(tag[:4]) != "Info") {
		return 0, 0
	}
	skip = h.samples

	flags := binary.BigEndian.Uint32(tag[4:])
	ext := 8
	for _, field := range []struct {
		bit  uint32
		size int
	}{{1, 4}, {2, 4}, {4, 100}, {8, 4}} {
		if flags&field.bit != 0 {
			ext += field.size
		}
	}
	// The extension is a 9-byte encoder name, 12 bytes of settings, then
	// delay and padding packed into three bytes.
	if len(tag) < ext+24 || tag[ext] < 0x20 || tag[ext] > 0x7e {
		return skip, 0
	}
	b := tag[ext+21:]
	delay := int(b[0])<<4 | int(b[1])>>4
	padding := int(b[1]&0x0f)<<8 | int(b[2])
	if delay == 0 && padding == 0 {
		return skip, 0
	}
	return skip + delay + mp3DecoderDelay, max(0, padding-mp3DecoderDelay)
}
