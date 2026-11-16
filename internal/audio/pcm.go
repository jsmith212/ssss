package audio

import "math"

// PCM is decoded audio in planar form: Data[channel][frame], nominally in
// [-1, 1]. Float files may exceed that range.
type PCM struct {
	Rate     int
	Channels int
	Frames   int
	Data     [][]float32
}

// Loop is a sustain loop in frames. End is exclusive.
type Loop struct {
	Start int
	End   int
}

// Meta is what the WAV acid/smpl chunks or the AIFF INST/MARK chunks say
// about a file. Lossy and FLAC files carry none of it.
type Meta struct {
	// HasAcid reports whether an acid chunk was present; OneShot, Tempo,
	// Beats and RootNote come from it.
	HasAcid bool
	OneShot bool
	Tempo   float64 // BPM, 0 = not stated
	Beats   int     // 0 = not stated
	// RootNote is the acid root as a MIDI note, -1 unless the chunk marks
	// it valid.
	RootNote int
	// UnityNote is the smpl unity note or AIFF base note (MIDI), -1 when
	// absent. Many writers leave it at the default 60, so it is weak
	// evidence of pitch.
	UnityNote int
	Loops     []Loop
}

// Info describes a file without its samples.
type Info struct {
	Format   Format
	Rate     int
	Channels int
	// Frames is the length of the whole file, which can be more than a
	// length-limited decode returned.
	Frames int
	// BitDepth is the significant bits per sample; 0 for lossy formats.
	BitDepth int
	Float    bool
	Meta     Meta
}

// Duration returns the length of the file in seconds.
func (i Info) Duration() float64 {
	if i.Rate <= 0 {
		return 0
	}
	return float64(i.Frames) / float64(i.Rate)
}

func newMeta() Meta { return Meta{RootNote: -1, UnityNote: -1} }

func newPCM(rate, channels, frames int) *PCM {
	p := &PCM{Rate: rate, Channels: channels, Frames: frames, Data: make([][]float32, channels)}
	// One backing array keeps the channels adjacent and costs one allocation.
	backing := make([]float32, channels*frames)
	for c := range p.Data {
		p.Data[c] = backing[c*frames : (c+1)*frames : (c+1)*frames]
	}
	return p
}

// room returns s with space for n more samples. Decoders of compressed
// formats do not trust a stated length enough to allocate it up front, so
// their buffers grow; doubling up to limit keeps the copies left behind to
// about the size of the result, where append's finer steps leave several
// times that.
func room(s []float32, n, limit int) []float32 {
	need := len(s) + n
	if need <= cap(s) {
		return s
	}
	out := make([]float32, len(s), max(need, min(2*cap(s), limit)))
	copy(out, s)
	return out
}

// Mono returns the average of all channels. For a mono signal it returns the
// channel itself, not a copy.
func (p *PCM) Mono() []float32 { return p.MonoN(p.Frames) }

// MonoN is Mono for the first maxFrames frames only, for callers that look at
// just the start of long audio and should not pay for a downmix of all of it.
func (p *PCM) MonoN(maxFrames int) []float32 {
	frames := max(0, min(p.Frames, maxFrames))
	switch p.Channels {
	case 0:
		return nil
	case 1:
		return p.Data[0][:frames]
	}
	out := make([]float32, frames)
	for _, ch := range p.Data {
		for i, v := range ch[:frames] {
			out[i] += v
		}
	}
	scale := 1 / float32(p.Channels)
	for i := range out {
		out[i] *= scale
	}
	return out
}

const surroundGain = math.Sqrt2 / 2

// stereoGains gives each source channel's contribution to left and right,
// indexed by channel count, for the WAVE channel order (FL FR FC LFE BL BR
// SL SR). The LFE is dropped, as is conventional for a stereo fold-down.
var stereoGains = map[int][][2]float32{
	3: {{1, 0}, {0, 1}, {surroundGain, surroundGain}},
	4: {{1, 0}, {0, 1}, {surroundGain, 0}, {0, surroundGain}},
	5: {{1, 0}, {0, 1}, {surroundGain, surroundGain}, {surroundGain, 0}, {0, surroundGain}},
	6: {{1, 0}, {0, 1}, {surroundGain, surroundGain}, {0, 0}, {surroundGain, 0}, {0, surroundGain}},
	7: {{1, 0}, {0, 1}, {surroundGain, surroundGain}, {0, 0}, {0.5, 0.5}, {surroundGain, 0}, {0, surroundGain}},
	8: {{1, 0}, {0, 1}, {surroundGain, surroundGain}, {0, 0}, {surroundGain, 0}, {0, surroundGain}, {surroundGain, 0}, {0, surroundGain}},
}

// Stereo folds more than two channels down to two and returns p unchanged
// otherwise. Layouts up to 7.1 use the usual fold-down: front channels at
// unity, centre and surrounds at -3 dB, so material that sits in the front
// pair plays as loud as the same material in a stereo file. If the sum goes
// over full scale, the whole clip is turned down by one gain, the same for
// both sides, just far enough to bring its peak back to full scale (or to
// the source's own peak, for float files that were over it already).
// Anything wider than 7.1 has no known layout: its channels alternate
// between left and right and are averaged.
func (p *PCM) Stereo() *PCM {
	if p.Channels <= 2 {
		return p
	}
	gains, ok := stereoGains[p.Channels]
	if !ok {
		gains = make([][2]float32, p.Channels)
		perSide := [2]int{(p.Channels + 1) / 2, p.Channels / 2}
		for c := range gains {
			gains[c][c%2] = 1 / float32(perSide[c%2])
		}
	}
	out := newPCM(p.Rate, 2, p.Frames)
	var sourcePeak, peak float32
	for c, g := range gains {
		if g[0] == 0 && g[1] == 0 {
			continue
		}
		src := p.Data[c][:p.Frames]
		sourcePeak = max(sourcePeak, absPeak(src))
		for side, w := range g {
			if w == 0 {
				continue
			}
			dst := out.Data[side]
			for i, v := range src {
				dst[i] += v * w
			}
		}
	}
	for _, ch := range out.Data {
		peak = max(peak, absPeak(ch))
	}
	if ceiling := max(1, sourcePeak); peak > ceiling {
		scale := ceiling / peak
		for _, ch := range out.Data {
			for i := range ch {
				ch[i] *= scale
			}
		}
	}
	return out
}

// absPeak returns the largest magnitude in x, ignoring NaNs.
func absPeak(x []float32) float32 {
	var peak float32
	for _, v := range x {
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	return peak
}

// truncate limits p to at most frames frames without copying.
func (p *PCM) truncate(frames int) {
	if frames >= p.Frames {
		return
	}
	p.Frames = frames
	for c := range p.Data {
		p.Data[c] = p.Data[c][:frames]
	}
}
