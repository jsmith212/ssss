package audio

import (
	"encoding/binary"
	"math"
)

type encoding uint8

const (
	encU8 encoding = iota
	encS8
	encIntLE // 2, 3 or 4 bytes
	encIntBE // 2, 3 or 4 bytes
	encF32LE
	encF32BE
	encF64LE
	encF64BE
)

// layout describes interleaved uncompressed samples as stored in a WAV data
// or AIFF SSND chunk.
type layout struct {
	enc      encoding
	bytes    int // per sample, including padding
	channels int
}

func (l layout) frameSize() int { return l.bytes * l.channels }

// floatLimit bounds float samples. Anything beyond it, and NaN, is treated as
// damage and silenced so it cannot reach the output device.
const floatLimit = 64

func cleanFloat(v float32) float32 {
	if v >= -floatLimit && v <= floatLimit {
		return v
	}
	return 0
}

// convert deinterleaves the whole frames in buf into dst[c][off:].
func (l layout) convert(dst [][]float32, off int, buf []byte) {
	stride := l.frameSize()
	frames := len(buf) / stride
	for c := range l.channels {
		out := dst[c][off : off+frames]
		src := buf[c*l.bytes:]
		switch l.enc {
		case encU8:
			for i := range out {
				out[i] = (float32(src[i*stride]) - 128) / 128
			}
		case encS8:
			for i := range out {
				out[i] = float32(int8(src[i*stride])) / 128
			}
		case encIntLE:
			switch l.bytes {
			case 2:
				for i := range out {
					out[i] = float32(int16(binary.LittleEndian.Uint16(src[i*stride:]))) / (1 << 15)
				}
			case 3:
				for i := range out {
					s := src[i*stride : i*stride+3]
					out[i] = float32(int32(uint32(s[0])<<8|uint32(s[1])<<16|uint32(s[2])<<24)>>8) / (1 << 23)
				}
			case 4:
				for i := range out {
					out[i] = float32(int32(binary.LittleEndian.Uint32(src[i*stride:]))) / (1 << 31)
				}
			}
		case encIntBE:
			switch l.bytes {
			case 2:
				for i := range out {
					out[i] = float32(int16(binary.BigEndian.Uint16(src[i*stride:]))) / (1 << 15)
				}
			case 3:
				for i := range out {
					s := src[i*stride : i*stride+3]
					out[i] = float32(int32(uint32(s[2])<<8|uint32(s[1])<<16|uint32(s[0])<<24)>>8) / (1 << 23)
				}
			case 4:
				for i := range out {
					out[i] = float32(int32(binary.BigEndian.Uint32(src[i*stride:]))) / (1 << 31)
				}
			}
		case encF32LE:
			for i := range out {
				out[i] = cleanFloat(math.Float32frombits(binary.LittleEndian.Uint32(src[i*stride:])))
			}
		case encF32BE:
			for i := range out {
				out[i] = cleanFloat(math.Float32frombits(binary.BigEndian.Uint32(src[i*stride:])))
			}
		case encF64LE:
			for i := range out {
				out[i] = cleanFloat(float32(math.Float64frombits(binary.LittleEndian.Uint64(src[i*stride:]))))
			}
		case encF64BE:
			for i := range out {
				out[i] = cleanFloat(float32(math.Float64frombits(binary.BigEndian.Uint64(src[i*stride:]))))
			}
		}
	}
}
