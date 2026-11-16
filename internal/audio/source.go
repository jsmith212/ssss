package audio

import (
	"bytes"
	"errors"
	"io"
)

// maxInMemory is the largest compressed file read into memory before
// decoding. The MP3 and Vorbis decoders make many small reads, which are
// cheapest against a byte slice; larger files are read through a window so
// that a decode never costs more memory than the audio it returns.
const maxInMemory = 16 << 20

// windowSize is the read-ahead of a windowReader.
const windowSize = 128 << 10

// compressedSource returns a seekable view of the file for a decoder. A file
// that is to be decoded and is small is held in memory; anything else is read
// through a window.
func compressedSource(r io.ReaderAt, size int64, headersOnly bool) io.ReadSeeker {
	if !headersOnly && size <= maxInMemory {
		data := make([]byte, size)
		if n, _ := r.ReadAt(data, 0); n == len(data) {
			return bytes.NewReader(data)
		}
		// Let the decoder meet the same read error and report it.
	}
	return &windowReader{r: r, size: size}
}

// windowReader is a buffered io.ReadSeeker over part of an io.ReaderAt. Seeks
// are free and keep the buffer when they land inside it, which suits decoders
// that read a few bytes, skip, and look back.
type windowReader struct {
	r    io.ReaderAt
	size int64
	pos  int64

	buf   []byte
	start int64 // file offset of buf[0]
}

func (w *windowReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if w.pos >= w.size {
		return 0, io.EOF
	}
	if w.pos < w.start || w.pos >= w.start+int64(len(w.buf)) {
		if err := w.fill(); err != nil {
			return 0, err
		}
	}
	n := copy(p, w.buf[w.pos-w.start:])
	w.pos += int64(n)
	return n, nil
}

// fill loads the window starting at the current position.
func (w *windowReader) fill() error {
	want := int(min(windowSize, w.size-w.pos))
	if cap(w.buf) < want {
		w.buf = make([]byte, want)
	}
	n, err := w.r.ReadAt(w.buf[:want], w.pos)
	w.buf, w.start = w.buf[:n], w.pos
	if n > 0 {
		return nil // a short read is served; the error recurs on the next fill
	}
	if err == nil || err == io.EOF {
		// The file is shorter than it was when it was opened.
		return io.ErrUnexpectedEOF
	}
	return err
}

func (w *windowReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += w.pos
	case io.SeekEnd:
		offset += w.size
	default:
		return 0, errors.New("audio: invalid seek whence")
	}
	if offset < 0 {
		return 0, errors.New("audio: seek before start of file")
	}
	w.pos = offset
	return offset, nil
}

// ReadAt bypasses the window; it is for the occasional header lookup.
func (w *windowReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= w.size {
		return 0, io.EOF
	}
	if rest := w.size - off; int64(len(p)) > rest {
		n, err := w.r.ReadAt(p[:rest], off)
		if err == nil {
			err = io.EOF
		}
		return n, err
	}
	return w.r.ReadAt(p, off)
}
