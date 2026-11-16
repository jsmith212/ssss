package audio

import (
	"encoding/binary"
	"io"
	"math"
)

// readAIFF walks an AIFF or AIFF-C file. Chunk order is free, so the walk
// collects COMM, SSND, MARK and INST before anything is decoded.
func readAIFF(r io.ReaderAt, size int64, opt options) (*PCM, Info, error) {
	var head [12]byte
	if _, err := r.ReadAt(head[:], 0); err != nil {
		return nil, Info{}, corrupt(AIFF, "short header")
	}
	compressed := string(head[8:12]) == "AIFC"

	var (
		comm       []byte
		dataOff    = int64(-1)
		dataLen    int64
		markers    map[int]int
		baseNote   = -1
		loopMode   int
		loopBegin  int
		loopEnd    int
		hdr        [8]byte
		commFrames int
	)
	pos := int64(12)
	for range maxChunks {
		if pos+8 > size {
			break
		}
		if _, err := r.ReadAt(hdr[:], pos); err != nil || !plausibleID(hdr[:]) {
			break
		}
		id := string(hdr[:4])
		length := u32be(hdr[4:])
		body := pos + 8
		avail := size - body
		length = min(length, avail)

		switch id {
		case "COMM":
			if comm = readChunk(r, body, length, 18); comm == nil {
				return nil, Info{}, corrupt(AIFF, "short COMM chunk")
			}
		case "SSND":
			var b [8]byte
			if dataOff >= 0 || length < 8 {
				break
			}
			if _, err := r.ReadAt(b[:], body); err != nil {
				break
			}
			skip := 8 + u32be(b[:])
			if skip > length {
				return nil, Info{}, corrupt(AIFF, "SSND offset past end of chunk")
			}
			dataOff, dataLen = body+skip, length-skip
		case "MARK":
			if b := readChunk(r, body, length, 2); b != nil {
				markers = parseMarkers(b)
			}
		case "INST":
			if b := readChunk(r, body, length, 20); b != nil {
				baseNote = int(b[0])
				loopMode, loopBegin, loopEnd = u16be(b[8:]), u16be(b[10:]), u16be(b[12:])
			}
		}
		pos = body + length + length&1
	}

	if comm == nil {
		return nil, Info{}, corrupt(AIFF, "no COMM chunk")
	}
	channels := u16be(comm)
	commFrames = int(u32be(comm[2:]))
	bits := u16be(comm[6:])
	rate := int(math.Round(extended(comm[8:18])))
	if channels < 1 || channels > maxChannels {
		return nil, Info{}, corrupt(AIFF, "%d channels", channels)
	}
	if rate < 1 {
		return nil, Info{}, corrupt(AIFF, "sample rate %d", rate)
	}

	l := layout{channels: channels, bytes: (bits + 7) / 8, enc: encIntBE}
	isFloat := false
	if compressed {
		if len(comm) < 22 {
			return nil, Info{}, corrupt(AIFF, "short AIFF-C COMM chunk")
		}
		switch codec := string(comm[18:22]); codec {
		case "NONE", "twos", "in24", "in32":
		case "sowt":
			l.enc = encIntLE
		case "fl32", "FL32":
			l.enc, l.bytes, isFloat = encF32BE, 4, true
		case "fl64", "FL64":
			l.enc, l.bytes, isFloat = encF64BE, 8, true
		default:
			return nil, Info{}, unsupported(AIFF, "codec %q", codec)
		}
	}
	if !isFloat {
		switch l.bytes {
		case 1:
			l.enc = encS8
		case 2, 3, 4:
		default:
			return nil, Info{}, unsupported(AIFF, "%d-bit samples", bits)
		}
	}

	// A file with no SSND chunk is legal when it has no frames.
	frames := 0
	if dataOff >= 0 {
		frames = int(dataLen / int64(l.frameSize()))
		if commFrames > 0 {
			frames = min(frames, commFrames)
		}
	}

	meta := newMeta()
	if baseNote >= 0 && baseNote <= 127 {
		meta.UnityNote = baseNote
	}
	if loopMode != 0 {
		start, okStart := markers[loopBegin]
		end, okEnd := markers[loopEnd]
		if okStart && okEnd {
			// Markers sit between frames, so the end is already exclusive.
			meta.Loops = []Loop{{Start: start, End: end}}
		}
	}

	info := Info{Rate: rate, Channels: channels, Frames: frames, BitDepth: bits, Float: isFloat, Meta: meta}
	if opt.probe {
		return nil, info, nil
	}
	pcm, err := readRaw(r, dataOff, opt.limit(frames, channels, rate), l, rate, opt.hold)
	if err != nil {
		return nil, Info{}, corrupt(AIFF, "%v", err)
	}
	return pcm, info, nil
}

// parseMarkers reads a MARK chunk into marker id -> frame position.
func parseMarkers(b []byte) map[int]int {
	count := u16be(b)
	markers := make(map[int]int, min(count, 64))
	off := 2
	for range count {
		if off+7 > len(b) {
			break
		}
		markers[u16be(b[off:])] = int(u32be(b[off+2:]))
		// The name is a Pascal string padded to an even total length.
		nameLen := 1 + int(b[off+6])
		off += 6 + nameLen + nameLen&1
	}
	return markers
}

// extended converts an 80-bit IEEE 754 extended float, as used for the AIFF
// sample rate.
func extended(b []byte) float64 {
	exp := int(binary.BigEndian.Uint16(b)) & 0x7fff
	mant := binary.BigEndian.Uint64(b[2:])
	if exp == 0 && mant == 0 {
		return 0
	}
	v := math.Ldexp(float64(mant), exp-16383-63)
	if math.IsInf(v, 0) || math.IsNaN(v) || v > math.MaxInt32 {
		return 0
	}
	return v
}
