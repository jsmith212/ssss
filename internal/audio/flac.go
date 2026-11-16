package audio

import (
	"errors"
	"io"

	"github.com/mewkiz/flac"
)

func readFLAC(src io.ReadSeeker, opt options) (*PCM, Info, error) {
	stream, err := flac.New(src)
	if err != nil {
		return nil, Info{}, corrupt(FLAC, "%v", err)
	}
	si := stream.Info
	channels, rate, bits := int(si.NChannels), int(si.SampleRate), int(si.BitsPerSample)
	if channels < 1 || rate < 1 || bits < 1 || bits > 32 {
		return nil, Info{}, corrupt(FLAC, "invalid stream info")
	}
	declared := int(si.NSamples) // 36 bits in the stream, so it fits
	info := Info{Rate: rate, Channels: channels, Frames: declared, BitDepth: bits, Meta: newMeta()}
	if opt.probe && declared > 0 {
		return nil, info, nil
	}

	limit := opt.limit(maxDecodeSamples, channels, rate)
	if declared > 0 {
		limit = min(limit, declared)
	}
	// The declared length is not trusted for sizing beyond a modest amount;
	// append grows the rest.
	data := make([][]float32, channels)
	if !opt.probe {
		opt.hold.reserve(limit, channels)
		for c := range data {
			data[c] = make([]float32, 0, min(limit, 1<<22))
		}
	}

	frames := 0
	for frames < limit {
		f, err := stream.ParseNext()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, Info{}, corrupt(FLAC, "frame at sample %d: %v", frames, err)
		}
		if len(f.Subframes) != channels {
			return nil, Info{}, corrupt(FLAC, "frame has %d channels, stream has %d", len(f.Subframes), channels)
		}
		n := len(f.Subframes[0].Samples)
		frameBits := bits
		if f.BitsPerSample != 0 {
			frameBits = int(f.BitsPerSample)
		}
		if !opt.probe {
			take := min(n, limit-frames)
			scale := 1 / float32(uint64(1)<<(frameBits-1))
			for c, sub := range f.Subframes {
				if len(sub.Samples) != n {
					return nil, Info{}, corrupt(FLAC, "uneven subframes")
				}
				data[c] = room(data[c], take, limit)
				for _, s := range sub.Samples[:take] {
					data[c] = append(data[c], float32(s)*scale)
				}
			}
		}
		frames += n
	}

	if declared == 0 {
		info.Frames = frames
	} else if frames < limit {
		return nil, Info{}, corrupt(FLAC, "stream ends at sample %d of %d", frames, declared)
	}
	if opt.probe {
		return nil, info, nil
	}
	frames = min(frames, limit)
	return &PCM{Rate: rate, Channels: channels, Frames: frames, Data: data}, info, nil
}
