package audio

import (
	"bytes"
	"errors"
	"io"

	"github.com/jfreymuth/oggvorbis"
)

// vorbisOrder maps each WAVE-order channel to its index in the Vorbis channel
// order, per channel count, so Stereo sees every format in one layout.
var vorbisOrder = map[int][]int{
	3: {0, 2, 1},
	5: {0, 2, 1, 3, 4},
	6: {0, 2, 1, 5, 3, 4},
	7: {0, 2, 1, 6, 5, 3, 4},
	8: {0, 2, 1, 7, 5, 6, 3, 4},
}

func readOGG(src io.ReadSeeker, opt options) (*PCM, Info, error) {
	// The first page holds the codec's identification header.
	var first [64]byte
	n, _ := io.ReadFull(src, first[:])
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return nil, Info{}, corrupt(OGG, "%v", err)
	}
	if !bytes.Contains(first[:n], []byte("\x01vorbis")) {
		if bytes.Contains(first[:n], []byte("OpusHead")) {
			return nil, Info{}, unsupported(OGG, "Opus stream")
		}
		return nil, Info{}, unsupported(OGG, "not a Vorbis stream")
	}

	if opt.probe {
		length, format, err := oggvorbis.GetLength(src)
		if err != nil {
			return nil, Info{}, corrupt(OGG, "%v", err)
		}
		if format.Channels < 1 || format.SampleRate < 1 || length < 0 {
			return nil, Info{}, corrupt(OGG, "invalid stream header")
		}
		return nil, Info{Rate: format.SampleRate, Channels: format.Channels, Frames: int(length), Meta: newMeta()}, nil
	}

	r, err := oggvorbis.NewReader(src)
	if err != nil {
		return nil, Info{}, corrupt(OGG, "%v", err)
	}
	channels, rate := r.Channels(), r.SampleRate()
	if channels < 1 || rate < 1 {
		return nil, Info{}, corrupt(OGG, "invalid stream header")
	}
	total := int(max(r.Length(), 0))
	limit := opt.limit(maxDecodeSamples, channels, rate)
	if total > 0 {
		limit = min(limit, total)
	}
	order := vorbisOrder[channels]

	opt.hold.reserve(limit, channels)
	data := make([][]float32, channels)
	for c := range data {
		data[c] = make([]float32, 0, min(limit, 1<<22))
	}
	buf := make([]float32, 8192*channels)
	frames := 0
	for frames < limit {
		n, err := r.Read(buf)
		take := min(n/channels, limit-frames)
		for c := range channels {
			src := c
			if order != nil {
				src = order[c]
			}
			data[c] = room(data[c], take, limit)
			for i := range take {
				data[c] = append(data[c], buf[i*channels+src])
			}
		}
		frames += take
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, Info{}, corrupt(OGG, "at sample %d: %v", frames, err)
		}
		if n == 0 {
			return nil, Info{}, corrupt(OGG, "decoder made no progress")
		}
	}
	if total == 0 {
		total = frames
	} else if frames < limit {
		return nil, Info{}, corrupt(OGG, "stream ends at sample %d of %d", frames, total)
	}
	info := Info{Rate: rate, Channels: channels, Frames: total, Meta: newMeta()}
	return &PCM{Rate: rate, Channels: channels, Frames: frames, Data: data}, info, nil
}
