package audio

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

const (
	// MaxPlaybackSeconds is how much of a file GET /audio serves. A sample
	// manager's files are seconds long; the cap keeps a stray hour-long
	// recording from becoming a gigabyte response the webview must hold
	// in memory. Longer files play, and draw their waveform, up to here.
	MaxPlaybackSeconds = 300

	cacheEntries = 8
	cacheBytes   = 128 << 20

	// writeChunk is the number of samples serialised per Write.
	writeChunk = 16 << 10
)

// Resolver maps a sample ID to the absolute path of its file. It reports
// false for unknown samples and must be safe for concurrent use.
type Resolver func(id string) (absPath string, ok bool)

// Server serves decoded audio and waveform peaks for the asset handler.
type Server struct {
	resolve    Resolver
	cache      *clipCache
	maxSeconds int

	mu       sync.RWMutex
	peaksDir string
}

// NewServer returns a Server that finds files through resolve and caches
// full peaks under peaksDir (see PeaksPath). An empty peaksDir disables the
// disk cache.
func NewServer(resolve Resolver, peaksDir string) *Server {
	s := &Server{resolve: resolve, peaksDir: peaksDir, maxSeconds: MaxPlaybackSeconds}
	s.cache = newClipCache(cacheEntries, cacheBytes, func(file string) (*clip, error) {
		return loadClip(file, s.maxSeconds)
	})
	return s
}

// SetPeaksDir changes the peaks cache directory, for when another library is
// opened. An empty dir disables the disk cache.
func (s *Server) SetPeaksDir(dir string) {
	s.mu.Lock()
	s.peaksDir = dir
	s.mu.Unlock()
}

// Forget drops any decoded audio held for id, for when its file is removed.
func (s *Server) Forget(id string) {
	s.cache.forget(id)
}

// AudioHandler serves GET /audio/{id}.f32: planar little-endian float32,
// channel 0 then channel 1, at most two channels (wider files are folded
// down) and at most MaxPlaybackSeconds. X-Frames, X-Channels and X-Rate
// describe the body; X-Loop-Start and X-Loop-End (frames, end exclusive) are
// present when the file carries a loop inside the served range. Unknown or
// undecodable samples get a 404 with a JSON {"error"} body.
//
// The handler takes the ID from the last path segment, so it works behind
// any mux pattern.
func (s *Server) AudioHandler() http.Handler {
	return http.HandlerFunc(s.serveAudio)
}

// PeaksHandler serves GET /peaks/{id}: FullSize bytes of (int8 min, int8
// max) pairs covering the same audio AudioHandler serves. Peaks are computed
// on first request and stored in the peaks directory.
func (s *Server) PeaksHandler() http.Handler {
	return http.HandlerFunc(s.servePeaks)
}

func (s *Server) serveAudio(w http.ResponseWriter, r *http.Request) {
	if !allowRead(w, r) {
		return
	}
	id, ok := strings.CutSuffix(path.Base(r.URL.Path), ".f32")
	if !ok {
		writeError(w, "not found")
		return
	}
	c, err := s.clip(id)
	if err != nil {
		writeError(w, err.Error())
		return
	}

	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.FormatInt(c.bytes(), 10))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frames", strconv.Itoa(c.pcm.Frames))
	h.Set("X-Channels", strconv.Itoa(c.pcm.Channels))
	h.Set("X-Rate", strconv.Itoa(c.pcm.Rate))
	if c.loop != nil {
		h.Set("X-Loop-Start", strconv.Itoa(c.loop.Start))
		h.Set("X-Loop-End", strconv.Itoa(c.loop.End))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	writePlanar(w, c.pcm)
}

func (s *Server) servePeaks(w http.ResponseWriter, r *http.Request) {
	if !allowRead(w, r) {
		return
	}
	id := path.Base(r.URL.Path)
	peaks, err := s.peaks(id)
	if err != nil {
		writeError(w, err.Error())
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.Itoa(len(peaks)))
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(peaks)
	}
}

var errNotFound = errors.New("sample not found")

func (s *Server) clip(id string) (*clip, error) {
	if !validID(id) {
		return nil, errNotFound
	}
	file, ok := s.resolve(id)
	if !ok {
		return nil, errNotFound
	}
	c, err := s.cache.get(id, file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errNotFound
	}
	return c, err
}

func (s *Server) peaks(id string) ([]byte, error) {
	if !validID(id) {
		return nil, errNotFound
	}
	s.mu.RLock()
	dir := s.peaksDir
	s.mu.RUnlock()

	if dir != "" {
		// Resolve first so a sample that has left the library is a 404
		// even while its peaks linger in the cache.
		if _, ok := s.resolve(id); !ok {
			return nil, errNotFound
		}
		if peaks, err := LoadPeaks(dir, id); err == nil {
			return peaks, nil
		}
	}
	c, err := s.clip(id)
	if err != nil {
		return nil, err
	}
	peaks := Full(c.pcm)
	if dir != "" {
		// The cache is an optimisation; a read-only or full disk only
		// costs a recompute next time.
		_ = SavePeaks(dir, id, peaks)
	}
	return peaks, nil
}

// loadClip decodes a file the way the player wants it.
func loadClip(file string, maxSeconds int) (*clip, error) {
	// Someone is waiting to hear this, so it goes ahead of analysis in the
	// queue for memory. The claim ends with the decode: from then on the
	// clip cache accounts for the audio.
	h := &hold{b: decodeBudget, urgent: true}
	defer h.release()
	pcm, info, err := openAndRead(file, options{maxSeconds: maxSeconds, hold: h})
	if err != nil {
		return nil, err
	}
	c := &clip{pcm: pcm.Stereo()}
	if loops := validLoops(info.Meta.Loops, pcm.Frames); len(loops) > 0 {
		c.loop = &loops[0]
	}
	return c, nil
}

// validID accepts only sample IDs (32 lower-case hex digits). IDs become
// file names in the peaks cache, so nothing else may get through.
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range []byte(id) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func allowRead(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func writeError(w http.ResponseWriter, message string) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

var writeBuffers = sync.Pool{New: func() any {
	b := make([]byte, writeChunk*4)
	return &b
}}

// writePlanar streams p as little-endian float32, one channel after another,
// through a pooled buffer. A write error means the client went away, so the
// rest is abandoned.
func writePlanar(w http.ResponseWriter, p *PCM) {
	bp := writeBuffers.Get().(*[]byte)
	defer writeBuffers.Put(bp)
	buf := *bp
	for _, ch := range p.Data {
		for len(ch) > 0 {
			n := min(len(ch), writeChunk)
			for i, v := range ch[:n] {
				binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
			}
			if _, err := w.Write(buf[:n*4]); err != nil {
				return
			}
			ch = ch[n:]
		}
	}
}
