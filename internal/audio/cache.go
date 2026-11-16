package audio

import (
	"container/list"
	"os"
	"sync"
	"time"
)

// clip is a file decoded for playback: at most two channels and at most the
// server's length limit.
type clip struct {
	pcm  *PCM
	loop *Loop // nil when the file has none inside the served range
}

func (c *clip) bytes() int64 {
	return int64(c.pcm.Channels) * int64(c.pcm.Frames) * 4
}

// clipCache is a small LRU of recently decoded clips. Its main job is to let
// the /audio and /peaks requests for a newly selected sample share one
// decode; it also absorbs re-requests when the frontend's own buffer cache
// has evicted a sample.
type clipCache struct {
	maxEntries int
	maxBytes   int64
	load       func(path string) (*clip, error)

	mu      sync.Mutex
	entries map[string]*cacheEntry
	order   *list.List // front = most recent; values are *cacheEntry
	used    int64
}

type cacheEntry struct {
	id    string
	path  string
	size  int64
	mtime time.Time
	elem  *list.Element

	ready chan struct{} // closed once clip and err are set
	clip  *clip
	err   error
	bytes int64
}

func newClipCache(maxEntries int, maxBytes int64, load func(string) (*clip, error)) *clipCache {
	return &clipCache{
		maxEntries: maxEntries,
		maxBytes:   maxBytes,
		load:       load,
		entries:    make(map[string]*cacheEntry),
		order:      list.New(),
	}
}

// get returns the clip for id, decoding path if it is not cached or the file
// has changed. Concurrent calls for one id wait on a single decode. Failures
// are not cached.
func (c *clipCache) get(id, path string) (*clip, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	e, ok := c.entries[id]
	if ok && (e.path != path || e.size != st.Size() || !e.mtime.Equal(st.ModTime())) {
		c.remove(e)
		ok = false
	}
	if ok {
		c.order.MoveToFront(e.elem)
		c.mu.Unlock()
		<-e.ready
		return e.clip, e.err
	}
	e = &cacheEntry{id: id, path: path, size: st.Size(), mtime: st.ModTime(), ready: make(chan struct{})}
	e.elem = c.order.PushFront(e)
	c.entries[id] = e
	c.mu.Unlock()

	e.clip, e.err = c.load(path)
	close(e.ready)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries[id] != e {
		return e.clip, e.err // replaced or evicted while decoding
	}
	if e.err != nil {
		c.remove(e)
		return nil, e.err
	}
	e.bytes = e.clip.bytes()
	c.used += e.bytes
	for back := c.order.Back(); back != nil && (c.order.Len() > c.maxEntries || c.used > c.maxBytes); {
		victim := back.Value.(*cacheEntry)
		back = back.Prev()
		if victim != e {
			c.remove(victim)
		}
	}
	return e.clip, nil
}

// remove drops e. The caller holds c.mu.
func (c *clipCache) remove(e *cacheEntry) {
	c.order.Remove(e.elem)
	delete(c.entries, e.id)
	c.used -= e.bytes
}

// forget drops any cached clip for id.
func (c *clipCache) forget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[id]; ok {
		c.remove(e)
	}
}
