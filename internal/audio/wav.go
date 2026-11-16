package audio

import (
	"encoding/binary"
	"io"
	"math"
)

const (
	wavPCM        = 0x0001
	wavFloat      = 0x0003
	wavExtensible = 0xfffe

	// maxChunks stops a walk over a file made of thousands of empty chunks.
	maxChunks = 1 << 16
	// maxMetaChunk bounds how much of an acid/smpl/MARK chunk is read.
	maxMetaChunk = 1 << 16
	// maxChannels rejects absurd headers before anything is sized by them.
	maxChannels = 64
)

type wavFormat struct {
	layout   layout
	rate     int
	bitDepth int
	float    bool
}

// readWAV walks a RIFF/WAVE (or RF64/BW64) file. The walk continues past the
// data chunk because acid and smpl usually follow it.
func readWAV(r io.ReaderAt, size int64, opt options) (*PCM, Info, error) {
	var head [12]byte
	if _, err := r.ReadAt(head[:], 0); err != nil {
		return nil, Info{}, corrupt(WAV, "short header")
	}
	wide := string(head[:4]) != "RIFF"

	var (
		format   *wavFormat
		dataOff  = int64(-1)
		dataLen  int64
		wideData int64
		meta     = newMeta()
		hdr      [8]byte
	)
	pos := int64(12)
	for range maxChunks {
		if pos+8 > size {
			break
		}
		if _, err := r.ReadAt(hdr[:], pos); err != nil {
			break
		}
		if !plausibleID(hdr[:]) {
			break
		}
		id := string(hdr[:4])
		length := u32le(hdr[4:])
		body := pos + 8
		avail := size - body

		switch id {
		case "ds64":
			if b := readChunk(r, body, min(length, avail), 16); b != nil {
				wideData = int64(binary.LittleEndian.Uint64(b[8:]) & math.MaxInt64)
			}
		case "fmt ":
			b := readChunk(r, body, min(length, avail), 16)
			if b == nil {
				return nil, Info{}, corrupt(WAV, "short fmt chunk")
			}
			f, err := parseWAVFormat(b)
			if err != nil {
				return nil, Info{}, err
			}
			format = f
		case "data":
			if dataOff >= 0 && dataLen > 0 {
				break
			}
			if wide && length == math.MaxUint32 && wideData > 0 {
				length = wideData
			}
			if length > avail || (length == 0 && avail > 0 && !chunkFollows(r, body, size)) {
				// The 0xFFFFFFFF or zero placeholder of a streaming writer
				// that never patched the header, or a file cut short. The
				// audio runs to the end of the file, less any chunks the
				// writer appended after it.
				stride := int64(1)
				if format != nil {
					stride = int64(format.layout.frameSize())
				}
				length = unsizedData(r, body, size, stride)
			}
			dataOff, dataLen = body, length
		case "acid":
			if b := readChunk(r, body, min(length, avail), 24); b != nil {
				parseAcid(b, &meta)
			}
		case "smpl":
			if b := readChunk(r, body, min(length, avail), 36); b != nil {
				parseSmpl(b, &meta)
			}
		}

		next := body + min(length, avail)
		if length&1 == 1 {
			// Chunks are word-aligned, but some writers omit the pad byte.
			if chunkFollows(r, next+1, size) || !chunkFollows(r, next, size) {
				next++
			}
		}
		pos = next
	}

	if format == nil {
		return nil, Info{}, corrupt(WAV, "no fmt chunk")
	}
	if dataOff < 0 {
		return nil, Info{}, corrupt(WAV, "no data chunk")
	}
	l := format.layout
	frames := int(dataLen / int64(l.frameSize()))
	info := Info{
		Rate:     format.rate,
		Channels: l.channels,
		Frames:   frames,
		BitDepth: format.bitDepth,
		Float:    format.float,
		Meta:     meta,
	}
	if opt.probe {
		return nil, info, nil
	}
	pcm, err := readRaw(r, dataOff, opt.limit(frames, l.channels, format.rate), l, format.rate, opt.hold)
	if err != nil {
		return nil, Info{}, corrupt(WAV, "%v", err)
	}
	return pcm, info, nil
}

// maxTrailer is how far back from the end of a file unsizedData looks for
// chunks following the audio. Metadata chunks are small; this is generous.
const maxTrailer = 1 << 20

// unsizedData returns the length of a data chunk at body whose stated size
// is useless. The audio ends where a chain of well-formed chunks begins that
// runs exactly to the end of the file, or at the end of the file if there is
// none. Sample data can look like one chunk header, but not like a chain of
// them whose sizes add up to the byte, and only whole frames are considered.
func unsizedData(r io.ReaderAt, body, size, stride int64) int64 {
	avail := size - body
	from := max(body, size-maxTrailer)
	tail := make([]byte, size-from)
	if _, err := r.ReadAt(tail, from); err != nil {
		return avail
	}
	// chain reports whether chunks starting at off (an offset into tail)
	// run exactly to the end, give or take a final pad byte.
	chain := func(off int64) bool {
		end := int64(len(tail))
		for off+8 <= end {
			if !plausibleID(tail[off:]) {
				return false
			}
			length := u32le(tail[off+4:])
			off += 8 + length
			if off == end || (length&1 == 1 && off+1 == end) {
				return true
			}
			off += length & 1
		}
		return false
	}
	first := from - body
	if rem := first % stride; rem != 0 {
		first += stride - rem
	}
	for length := first; length+8 <= avail; length += stride {
		// An odd-length body is followed by a pad byte, unless the writer
		// forgot it.
		if chain(body+length-from) || (length&1 == 1 && chain(body+length+1-from)) {
			return length
		}
	}
	return avail
}

// readChunk reads up to maxMetaChunk bytes of a chunk body, or nil if fewer
// than need bytes are there.
func readChunk(r io.ReaderAt, off, length int64, need int) []byte {
	if length < int64(need) {
		return nil
	}
	b := make([]byte, min(length, maxMetaChunk))
	if _, err := r.ReadAt(b, off); err != nil {
		return nil
	}
	return b
}

// chunkFollows reports whether a well-formed chunk sits at off: a plausible
// header whose body ends at the end of the file or at another plausible
// header. Looking one chunk further is what separates a real header from
// sample data, or from a header read one byte out of step.
func chunkFollows(r io.ReaderAt, off, size int64) bool {
	var hdr [8]byte
	if _, err := r.ReadAt(hdr[:], off); err != nil || !plausibleID(hdr[:]) {
		return false
	}
	length := u32le(hdr[4:])
	end := off + 8 + length
	if end >= size-1 {
		// A final chunk, possibly overstating its size as data chunks do.
		return string(hdr[:4]) == "data" || end <= size+1
	}
	for _, next := range [2]int64{end + length&1, end} {
		if _, err := r.ReadAt(hdr[:], next); err == nil && plausibleID(hdr[:]) {
			return true
		}
	}
	return false
}

func parseWAVFormat(b []byte) (*wavFormat, error) {
	tag := u16le(b)
	channels := u16le(b[2:])
	rate := int(u32le(b[4:]))
	blockAlign := u16le(b[12:])
	bits := u16le(b[14:])
	validBits := bits

	if tag == wavExtensible {
		if len(b) < 40 {
			return nil, corrupt(WAV, "extensible fmt chunk is %d bytes", len(b))
		}
		if v := u16le(b[18:]); v > 0 && v <= bits {
			validBits = v
		}
		// The sub-format GUID starts with the plain format tag.
		tag = u16le(b[24:])
	}
	if channels < 1 || channels > maxChannels {
		return nil, corrupt(WAV, "%d channels", channels)
	}
	if rate < 1 {
		return nil, corrupt(WAV, "sample rate %d", rate)
	}

	// Trust the block alignment over the bit depth for the container size:
	// 24-bit audio in 4-byte slots states it only there.
	size := (bits + 7) / 8
	if blockAlign > 0 && blockAlign%channels == 0 && blockAlign/channels >= size {
		size = blockAlign / channels
	}

	f := &wavFormat{rate: rate, bitDepth: validBits, layout: layout{bytes: size, channels: channels}}
	switch tag {
	case wavPCM:
		switch size {
		case 1:
			f.layout.enc = encU8
		case 2, 3, 4:
			f.layout.enc = encIntLE
		default:
			return nil, unsupported(WAV, "%d-bit integer samples", bits)
		}
	case wavFloat:
		f.float = true
		switch size {
		case 4:
			f.layout.enc = encF32LE
		case 8:
			f.layout.enc = encF64LE
		default:
			return nil, unsupported(WAV, "%d-bit float samples", bits)
		}
	default:
		return nil, unsupported(WAV, "codec 0x%04x", tag)
	}
	return f, nil
}

// parseAcid reads the 24-byte ACID loop chunk: flags, root note, two unknown
// fields, beat count, meter and tempo.
func parseAcid(b []byte, m *Meta) {
	flags := u32le(b)
	m.HasAcid = true
	m.OneShot = flags&1 != 0
	if root := u16le(b[4:]); flags&2 != 0 && root <= 127 {
		m.RootNote = root
	}
	if beats := u32le(b[12:]); beats > 0 && beats < 1<<16 {
		m.Beats = int(beats)
	}
	tempo := float64(math.Float32frombits(binary.LittleEndian.Uint32(b[20:])))
	if tempo > 0 && tempo < 1000 {
		m.Tempo = tempo
	}
}

// parseSmpl reads the sampler chunk: unity note at offset 12, loop count at
// 28, then 24-byte loop records whose end points are inclusive.
func parseSmpl(b []byte, m *Meta) {
	if note := u32le(b[12:]); note <= 127 {
		m.UnityNote = int(note)
	}
	count := u32le(b[28:])
	for i := int64(0); i < count; i++ {
		off := 36 + i*24
		if off+24 > int64(len(b)) {
			break
		}
		rec := b[off:]
		m.Loops = append(m.Loops, Loop{Start: int(u32le(rec[8:])), End: int(u32le(rec[12:])) + 1})
	}
}
