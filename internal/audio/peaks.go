package audio

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

const (
	// MiniBuckets is the length of the row waveform stored with each sample.
	MiniBuckets = 96
	// FullBuckets is the resolution of the player waveform.
	FullBuckets = 2000
	// FullSize is the byte length of full peaks: an int8 minimum and
	// maximum per bucket.
	FullSize = 2 * FullBuckets
)

// Mini returns 96 buckets of peak absolute amplitude across all channels,
// scaled so that full scale is 255. Levels are absolute, not normalised: a
// quiet sample draws as a quiet waveform.
func Mini(p *PCM) []byte {
	out := make([]byte, MiniBuckets)
	eachBucket(p, MiniBuckets, func(i int, lo, hi float32) {
		out[i] = byte(math.Round(float64(min(max(-lo, hi), 1)) * 255))
	})
	return out
}

// Full returns 2000 (min, max) pairs of int8 across all channels, scaled so
// that full scale is ±127: the body of GET /peaks/{id}.
func Full(p *PCM) []byte {
	out := make([]byte, FullSize)
	eachBucket(p, FullBuckets, func(i int, lo, hi float32) {
		out[2*i] = byte(int8(math.Round(float64(max(lo, -1)) * 127)))
		out[2*i+1] = byte(int8(math.Round(float64(min(hi, 1)) * 127)))
	})
	return out
}

// eachBucket splits p into n equal spans and reports each span's extremes.
// A file shorter than n frames repeats frames so every bucket has a value;
// an empty file reports nothing.
func eachBucket(p *PCM, n int, emit func(i int, lo, hi float32)) {
	if p == nil || p.Frames == 0 || p.Channels == 0 {
		return
	}
	for i := range n {
		start := int(int64(i) * int64(p.Frames) / int64(n))
		end := int(int64(i+1) * int64(p.Frames) / int64(n))
		if end <= start {
			end = start + 1
		}
		lo, hi := float32(math.MaxFloat32), float32(-math.MaxFloat32)
		for _, ch := range p.Data {
			for _, v := range ch[start:end] {
				lo = min(lo, v)
				hi = max(hi, v)
			}
		}
		emit(i, lo, hi)
	}
}

// PeaksPath is where the full peaks of sample id live under the cache
// directory dir: <dir>/<id[:2]>/<id>.pk.
func PeaksPath(dir, id string) string {
	shard := id
	if len(id) > 2 {
		shard = id[:2]
	}
	return filepath.Join(dir, shard, id+".pk")
}

// LoadPeaks reads cached full peaks. A missing or wrong-sized file is
// reported as fs.ErrNotExist so callers simply recompute.
func LoadPeaks(dir, id string) ([]byte, error) {
	data, err := os.ReadFile(PeaksPath(dir, id))
	if err != nil {
		return nil, err
	}
	if len(data) != FullSize {
		return nil, fmt.Errorf("peaks for %s are %d bytes: %w", id, len(data), os.ErrNotExist)
	}
	return data, nil
}

// SavePeaks writes full peaks to the cache, replacing any existing file
// atomically so a concurrent reader never sees a partial one.
func SavePeaks(dir, id string, peaks []byte) error {
	if len(peaks) != FullSize {
		return fmt.Errorf("peaks for %s are %d bytes, want %d", id, len(peaks), FullSize)
	}
	path := PeaksPath(dir, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), id+".*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(peaks)
	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
