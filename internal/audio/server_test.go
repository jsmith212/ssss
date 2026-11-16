package audio

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	idStereo  = "00000000000000000000000000000001"
	idLoop    = "00000000000000000000000000000002"
	idSix     = "00000000000000000000000000000003"
	idCorrupt = "00000000000000000000000000000004"
	idGone    = "00000000000000000000000000000005"
	idMP3     = "00000000000000000000000000000006"
	idUnknown = "ffffffffffffffffffffffffffffffff"
)

type fixtureServer struct {
	*Server
	mux      *http.ServeMux
	paths    map[string]string
	peaksDir string
}

func newFixtureServer(t *testing.T) *fixtureServer {
	t.Helper()
	dir := t.TempDir()
	files := map[string][]byte{
		idStereo:  buildWAV(wavSpec{enc: encIntLE, size: 2}),
		idLoop:    buildWAV(wavSpec{enc: encIntLE, size: 3, channels: 1, after: [][]byte{smplChunk(60, [2]uint32{1000, 7999})}}),
		idSix:     buildWAV(wavSpec{enc: encIntLE, size: 2, channels: 6, extensible: true}),
		idCorrupt: []byte("RIFF\x04\x00\x00\x00WAVE"),
		idMP3:     testdata(t, "stereo_id3.mp3"),
	}
	fs := &fixtureServer{paths: map[string]string{idGone: filepath.Join(dir, "gone.wav")}, peaksDir: filepath.Join(dir, "peaks")}
	for id, data := range files {
		fs.paths[id] = filepath.Join(dir, id+".bin")
		if err := os.WriteFile(fs.paths[id], data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fs.Server = NewServer(func(id string) (string, bool) {
		p, ok := fs.paths[id]
		return p, ok
	}, fs.peaksDir)
	// The same patterns the application's asset handler uses.
	fs.mux = http.NewServeMux()
	fs.mux.Handle("GET /audio/{file}", fs.AudioHandler())
	fs.mux.Handle("GET /peaks/{id}", fs.PeaksHandler())
	return fs
}

func (fs *fixtureServer) do(method, url string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	fs.mux.ServeHTTP(rec, httptest.NewRequest(method, url, nil))
	return rec
}

// planar splits a response body into channels of float32.
func planar(t *testing.T, body []byte, channels, frames int) [][]float32 {
	t.Helper()
	if len(body) != channels*frames*4 {
		t.Fatalf("body is %d bytes, want %d", len(body), channels*frames*4)
	}
	out := make([][]float32, channels)
	for c := range out {
		out[c] = make([]float32, frames)
		for i := range out[c] {
			out[c][i] = math.Float32frombits(binary.LittleEndian.Uint32(body[(c*frames+i)*4:]))
		}
	}
	return out
}

func TestAudioHandler(t *testing.T) {
	fs := newFixtureServer(t)
	tests := []struct {
		name      string
		id        string
		channels  int
		frames    int
		rate      int
		loopStart string
		loopEnd   string
	}{
		{name: "stereo", id: idStereo, channels: 2, frames: fixFrames, rate: fixRate},
		{name: "mono with loop", id: idLoop, channels: 1, frames: fixFrames, rate: fixRate, loopStart: "1000", loopEnd: "8000"},
		{name: "six channels folded to two", id: idSix, channels: 2, frames: fixFrames, rate: fixRate},
		{name: "mp3", id: idMP3, channels: 2, frames: 22050, rate: 44100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := fs.do(http.MethodGet, "/audio/"+tt.id+".f32")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body)
			}
			h := rec.Header()
			for key, want := range map[string]string{
				"X-Frames":       strconv.Itoa(tt.frames),
				"X-Channels":     strconv.Itoa(tt.channels),
				"X-Rate":         strconv.Itoa(tt.rate),
				"X-Loop-Start":   tt.loopStart,
				"X-Loop-End":     tt.loopEnd,
				"Cache-Control":  "no-store",
				"Content-Type":   "application/octet-stream",
				"Content-Length": strconv.Itoa(tt.channels * tt.frames * 4),
			} {
				if got := h.Get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			got := planar(t, rec.Body.Bytes(), tt.channels, tt.frames)

			// The body must be exactly what Decode gives, folded to stereo.
			pcm, _, err := Decode(fs.paths[tt.id])
			if err != nil {
				t.Fatal(err)
			}
			want := pcm.Stereo()
			for c := range got {
				for i := range got[c] {
					if got[c][i] != want.Data[c][i] {
						t.Fatalf("channel %d frame %d = %v, want %v", c, i, got[c][i], want.Data[c][i])
					}
				}
			}

			head := fs.do(http.MethodHead, "/audio/"+tt.id+".f32")
			if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("X-Frames") != strconv.Itoa(tt.frames) {
				t.Errorf("HEAD: status %d, %d body bytes, X-Frames %q", head.Code, head.Body.Len(), head.Header().Get("X-Frames"))
			}
		})
	}

	t.Run("stereo carries the fixture tones in order", func(t *testing.T) {
		rec := fs.do(http.MethodGet, "/audio/"+idStereo+".f32")
		ch := planar(t, rec.Body.Bytes(), 2, fixFrames)
		checkTones(t, &PCM{Rate: fixRate, Channels: 2, Frames: fixFrames, Data: ch}, 1e-3)
	})
}

func TestHandlerErrors(t *testing.T) {
	fs := newFixtureServer(t)
	tests := []struct {
		name   string
		method string
		url    string
		status int
	}{
		{"unknown audio", http.MethodGet, "/audio/" + idUnknown + ".f32", http.StatusNotFound},
		{"unknown peaks", http.MethodGet, "/peaks/" + idUnknown, http.StatusNotFound},
		{"corrupt audio", http.MethodGet, "/audio/" + idCorrupt + ".f32", http.StatusNotFound},
		{"corrupt peaks", http.MethodGet, "/peaks/" + idCorrupt, http.StatusNotFound},
		{"file gone audio", http.MethodGet, "/audio/" + idGone + ".f32", http.StatusNotFound},
		{"file gone peaks", http.MethodGet, "/peaks/" + idGone, http.StatusNotFound},
		{"no extension", http.MethodGet, "/audio/" + idStereo, http.StatusNotFound},
		{"wrong extension", http.MethodGet, "/audio/" + idStereo + ".wav", http.StatusNotFound},
		{"short id", http.MethodGet, "/audio/abc.f32", http.StatusNotFound},
		{"upper-case id", http.MethodGet, "/peaks/FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF", http.StatusNotFound},
		{"traversal", http.MethodGet, "/peaks/..%2F..%2F..%2F..%2Fetc%2Fpasswd0000000", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := fs.do(tt.method, tt.url)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
			var body struct{ Error string }
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error == "" {
				t.Errorf("body = %q, want JSON with an error", rec.Body)
			}
		})
	}

	// Called directly, without a mux filtering methods.
	for _, h := range []http.Handler{fs.AudioHandler(), fs.PeaksHandler()} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/audio/"+idStereo+".f32", nil))
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("POST: status %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
		}
	}

	if entries, _ := os.ReadDir(fs.peaksDir); len(entries) != 0 {
		t.Errorf("failed requests left %d entries in the peaks cache", len(entries))
	}
}

func TestPeaksHandler(t *testing.T) {
	fs := newFixtureServer(t)
	rec := fs.do(http.MethodGet, "/peaks/"+idStereo)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != FullSize || rec.Header().Get("Content-Length") != "4000" {
		t.Fatalf("%d bytes, Content-Length %q", rec.Body.Len(), rec.Header().Get("Content-Length"))
	}
	pcm, _, err := Decode(fs.paths[idStereo])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rec.Body.Bytes(), Full(pcm)) {
		t.Error("body differs from Full(Decode(file))")
	}
	// The 0.5 amplitude tone must reach ±64 in the waveform.
	lo, hi := int8(0), int8(0)
	for i := 0; i < FullSize; i += 2 {
		lo = min(lo, int8(rec.Body.Bytes()[i]))
		hi = max(hi, int8(rec.Body.Bytes()[i+1]))
	}
	if lo != -64 || hi != 64 {
		t.Errorf("extremes = %d, %d; want -64, 64", lo, hi)
	}

	// Written to the cache, and served from it from then on.
	cached, err := LoadPeaks(fs.peaksDir, idStereo)
	if err != nil || !bytes.Equal(cached, rec.Body.Bytes()) {
		t.Fatalf("cache file: %v", err)
	}
	marker := make([]byte, FullSize)
	marker[0] = 42
	if err := SavePeaks(fs.peaksDir, idStereo, marker); err != nil {
		t.Fatal(err)
	}
	if rec := fs.do(http.MethodGet, "/peaks/"+idStereo); !bytes.Equal(rec.Body.Bytes(), marker) {
		t.Error("second request did not come from the disk cache")
	}

	// Cached peaks of a sample that left the library are not served.
	if err := SavePeaks(fs.peaksDir, idUnknown, marker); err != nil {
		t.Fatal(err)
	}
	if rec := fs.do(http.MethodGet, "/peaks/"+idUnknown); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id with cached peaks: status %d", rec.Code)
	}

	// Without a directory, peaks are computed and nothing is written.
	fs.SetPeaksDir("")
	if rec := fs.do(http.MethodGet, "/peaks/"+idLoop); rec.Code != http.StatusOK || rec.Body.Len() != FullSize {
		t.Errorf("no cache dir: status %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if _, err := LoadPeaks(fs.peaksDir, idLoop); err == nil {
		t.Error("peaks were written with the cache disabled")
	}
}

func TestPlaybackLimit(t *testing.T) {
	fs := newFixtureServer(t)
	// 3 s of 8 kHz mono with a loop that ends beyond the limit.
	data := buildWAV(wavSpec{enc: encIntLE, size: 2, channels: 1, rate: 8000, frames: 24000,
		after: [][]byte{smplChunk(60, [2]uint32{0, 23999})}})
	const id = "00000000000000000000000000000099"
	fs.paths[id] = writeTemp(t, "long.wav", data)
	fs.maxSeconds = 2

	rec := fs.do(http.MethodGet, "/audio/"+id+".f32")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-Frames"); got != "16000" || rec.Body.Len() != 64000 {
		t.Errorf("X-Frames = %q, %d body bytes; want 16000, 64000", got, rec.Body.Len())
	}
	if got := rec.Header().Get("X-Loop-End"); got != "" {
		t.Errorf("X-Loop-End = %q for a loop outside the served range", got)
	}
}

func TestClipCache(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, 5)
	for i := range paths {
		paths[i] = filepath.Join(dir, strconv.Itoa(i))
		if err := os.WriteFile(paths[i], []byte{byte(i)}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var loads atomic.Int32
	load := func(string) (*clip, error) {
		loads.Add(1)
		return &clip{pcm: newPCM(8000, 1, 250)}, nil // 1000 bytes
	}
	get := func(c *clipCache, i int) *clip {
		t.Helper()
		cl, err := c.get(strconv.Itoa(i), paths[i])
		if err != nil {
			t.Fatal(err)
		}
		return cl
	}

	t.Run("hit, entry limit and LRU order", func(t *testing.T) {
		loads.Store(0)
		c := newClipCache(2, 1<<20, load)
		first := get(c, 0)
		if get(c, 0) != first || loads.Load() != 1 {
			t.Fatalf("second get reloaded (%d loads)", loads.Load())
		}
		get(c, 1)
		get(c, 0) // 0 is now the most recent
		get(c, 2) // evicts 1
		if get(c, 0) != first {
			t.Error("most recently used entry was evicted")
		}
		before := loads.Load()
		get(c, 1)
		if loads.Load() != before+1 {
			t.Error("least recently used entry was not evicted")
		}
		if len(c.entries) != 2 || c.order.Len() != 2 || c.used != 2000 {
			t.Errorf("%d entries, %d bytes", len(c.entries), c.used)
		}
	})

	t.Run("byte limit keeps the newest", func(t *testing.T) {
		c := newClipCache(8, 1500, load)
		get(c, 0)
		kept := get(c, 1)
		if len(c.entries) != 1 || c.used != 1000 || get(c, 1) != kept {
			t.Errorf("%d entries, %d bytes", len(c.entries), c.used)
		}
	})

	t.Run("changed file reloads", func(t *testing.T) {
		loads.Store(0)
		c := newClipCache(8, 1<<20, load)
		get(c, 3)
		if err := os.Chtimes(paths[3], time.Time{}, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		get(c, 3)
		if loads.Load() != 2 || c.used != 1000 {
			t.Errorf("%d loads, %d bytes", loads.Load(), c.used)
		}
		c.forget("3")
		if len(c.entries) != 0 || c.used != 0 {
			t.Errorf("forget left %d entries, %d bytes", len(c.entries), c.used)
		}
	})

	t.Run("errors are not cached", func(t *testing.T) {
		calls := 0
		c := newClipCache(8, 1<<20, func(string) (*clip, error) {
			calls++
			return nil, ErrCorrupt
		})
		for range 2 {
			if _, err := c.get("4", paths[4]); err != ErrCorrupt {
				t.Fatalf("err = %v", err)
			}
		}
		if calls != 2 || len(c.entries) != 0 || c.order.Len() != 0 {
			t.Errorf("%d calls, %d entries", calls, len(c.entries))
		}
		if _, err := c.get("x", filepath.Join(dir, "missing")); !os.IsNotExist(err) {
			t.Errorf("missing file: err = %v", err)
		}
	})

	t.Run("concurrent gets share one decode", func(t *testing.T) {
		loads.Store(0)
		release := make(chan struct{})
		c := newClipCache(8, 1<<20, func(p string) (*clip, error) {
			<-release
			return load(p)
		})
		var wg sync.WaitGroup
		got := make([]*clip, 16)
		for i := range got {
			wg.Go(func() {
				got[i], _ = c.get("0", paths[0])
			})
		}
		close(release)
		wg.Wait()
		for _, cl := range got {
			if cl == nil || cl != got[0] {
				t.Fatal("callers received different clips")
			}
		}
		if loads.Load() != 1 {
			t.Errorf("%d loads, want 1", loads.Load())
		}
	})
}

func TestServerConcurrent(t *testing.T) {
	fs := newFixtureServer(t)
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Go(func() {
			id := []string{idStereo, idLoop, idSix, idMP3}[i%4]
			url := "/audio/" + id + ".f32"
			if i%2 == 1 {
				url = "/peaks/" + id
			}
			if rec := fs.do(http.MethodGet, url); rec.Code != http.StatusOK {
				t.Errorf("%s: status %d", url, rec.Code)
			}
		})
	}
	wg.Wait()
}

func BenchmarkWritePlanar(b *testing.B) {
	p := newPCM(44100, 2, 44100*10)
	b.SetBytes(int64(p.Frames * 2 * 4))
	b.ReportAllocs()
	for b.Loop() {
		writePlanar(httptest.NewRecorder(), p)
	}
}
