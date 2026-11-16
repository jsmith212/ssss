package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Format identifies a container. Its string form is the value stored in the
// library's "fmt" field.
type Format string

const (
	WAV  Format = "wav"
	AIFF Format = "aiff" // AIFF and AIFF-C
	FLAC Format = "flac"
	MP3  Format = "mp3"
	OGG  Format = "ogg" // Ogg Vorbis
)

var (
	// ErrUnknownFormat means the content is not one of the supported containers.
	ErrUnknownFormat = errors.New("audio: unrecognised file format")
	// ErrUnsupported means the container was recognised but the encoding
	// inside it (ADPCM, Opus, µ-law, ...) cannot be decoded.
	ErrUnsupported = errors.New("audio: unsupported encoding")
	// ErrCorrupt means the file is damaged or cut short.
	ErrCorrupt = errors.New("audio: corrupt or truncated file")
)

// mp3ScanWindow is how far into a file an MPEG frame sync is searched for.
// Encoders occasionally leave junk before the first frame.
const mp3ScanWindow = 64 << 10

// detect identifies the container from its leading bytes.
func detect(r io.ReaderAt, size int64) (Format, error) {
	var head [12]byte
	n, err := r.ReadAt(head[:], 0)
	if n < 4 || (err != nil && err != io.EOF && n < len(head)) {
		return "", ErrUnknownFormat
	}
	h := head[:n]
	tag := string(h[:4])
	form := ""
	if n >= 12 {
		form = string(h[8:12])
	}
	switch {
	case (tag == "RIFF" || tag == "RF64" || tag == "BW64") && form == "WAVE":
		return WAV, nil
	case tag == "FORM" && (form == "AIFF" || form == "AIFC"):
		return AIFF, nil
	case tag == "fLaC":
		return FLAC, nil
	case tag == "OggS":
		return OGG, nil
	}

	start := int64(0)
	if n >= 10 && bytes.HasPrefix(h, []byte("ID3")) {
		start = id3v2Size(h[:10])
		var next [4]byte
		if _, err := r.ReadAt(next[:], start); err == nil && string(next[:]) == "fLaC" {
			return FLAC, nil
		}
	}
	if _, _, ok := findMP3Frame(r, start, size); ok {
		return MP3, nil
	}
	return "", ErrUnknownFormat
}

// id3v2Size returns the total length of an ID3v2 tag given its 10-byte header.
func id3v2Size(h []byte) int64 {
	size := int64(h[6]&0x7f)<<21 | int64(h[7]&0x7f)<<14 | int64(h[8]&0x7f)<<7 | int64(h[9]&0x7f)
	size += 10
	if h[5]&0x10 != 0 { // footer present
		size += 10
	}
	return size
}

// mp3Header is the subset of an MPEG audio frame header needed for sniffing.
type mp3Header struct {
	rate     int
	mono     bool
	frameLen int // bytes
	samples  int // per frame
	sideInfo int // bytes between the header and the main data
}

var (
	mp3BitratesV1 = [...]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	mp3BitratesV2 = [...]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
	mp3Rates      = [...]int{44100, 48000, 32000}
)

// parseMP3Header decodes a Layer III frame header. Other layers are rejected
// because the decoder cannot play them.
func parseMP3Header(b []byte) (mp3Header, bool) {
	if len(b) < 4 || b[0] != 0xff || b[1]&0xe0 != 0xe0 {
		return mp3Header{}, false
	}
	version := (b[1] >> 3) & 3 // 0 = MPEG 2.5, 2 = MPEG 2, 3 = MPEG 1
	layer := (b[1] >> 1) & 3   // 1 = Layer III
	bitrateIdx := b[2] >> 4
	rateIdx := (b[2] >> 2) & 3
	if version == 1 || layer != 1 || bitrateIdx == 0 || bitrateIdx == 15 || rateIdx == 3 {
		return mp3Header{}, false
	}
	padding := int((b[2] >> 1) & 1)
	h := mp3Header{mono: b[3]>>6 == 3}
	if version == 3 {
		h.rate = mp3Rates[rateIdx]
		h.samples = 1152
		h.sideInfo = 32
		if h.mono {
			h.sideInfo = 17
		}
		h.frameLen = 144000*mp3BitratesV1[bitrateIdx]/h.rate + padding
	} else {
		h.rate = mp3Rates[rateIdx] / 2
		if version == 0 {
			h.rate = mp3Rates[rateIdx] / 4
		}
		h.samples = 576
		h.sideInfo = 17
		if h.mono {
			h.sideInfo = 9
		}
		h.frameLen = 72000*mp3BitratesV2[bitrateIdx]/h.rate + padding
	}
	return h, h.frameLen > 4
}

// findMP3Frame looks for the first MPEG frame at or after start. A sync is
// only trusted when a second frame header follows it, or it is the last frame
// in the file; a bare 0xFFE pattern is too common in arbitrary data.
func findMP3Frame(r io.ReaderAt, start, size int64) (mp3Header, int64, bool) {
	if start < 0 || start >= size {
		return mp3Header{}, 0, false
	}
	buf := make([]byte, min(size-start, mp3ScanWindow))
	n, _ := r.ReadAt(buf, start)
	buf = buf[:n]
	for i := 0; i+4 <= len(buf); i++ {
		if buf[i] != 0xff {
			continue
		}
		h, ok := parseMP3Header(buf[i:])
		if !ok {
			continue
		}
		next := start + int64(i) + int64(h.frameLen)
		if next == size {
			return h, start + int64(i), true
		}
		var nb [4]byte
		if _, err := r.ReadAt(nb[:], next); err != nil {
			continue
		}
		if h2, ok := parseMP3Header(nb[:]); ok && h2.rate == h.rate {
			return h, start + int64(i), true
		}
	}
	return mp3Header{}, 0, false
}

func u16le(b []byte) int   { return int(binary.LittleEndian.Uint16(b)) }
func u32le(b []byte) int64 { return int64(binary.LittleEndian.Uint32(b)) }
func u16be(b []byte) int   { return int(binary.BigEndian.Uint16(b)) }
func u32be(b []byte) int64 { return int64(binary.BigEndian.Uint32(b)) }

// plausibleID reports whether b looks like a RIFF/IFF chunk identifier.
func plausibleID(b []byte) bool {
	for _, c := range b[:4] {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func corrupt(format Format, msg string, args ...any) error {
	return fmt.Errorf("%s: %s: %w", format, fmt.Sprintf(msg, args...), ErrCorrupt)
}

func unsupported(format Format, msg string, args ...any) error {
	return fmt.Errorf("%s: %s: %w", format, fmt.Sprintf(msg, args...), ErrUnsupported)
}
