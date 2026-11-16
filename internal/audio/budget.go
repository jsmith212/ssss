package audio

import (
	"container/list"
	"sync"
)

// decodeBudgetBytes is how much decoded audio may be in the making, or held
// for analysis, at once across the whole process. Samples are small and pass
// many at a time; five-minute stretches of long recordings, which cost over
// 100 MB each, go through two or three at a time however many workers ask.
const decodeBudgetBytes = 256 << 20

// decodeBudget is shared by every decode in the process: the analysis workers
// and the player's requests draw on the same memory.
var decodeBudget = newBudget(decodeBudgetBytes)

// budget is a weighted semaphore over bytes. Waiters are served in order, so
// a large request is not starved by a stream of small ones; urgent requests
// (someone is waiting to hear the sample) go ahead of the rest.
type budget struct {
	capacity int64

	mu      sync.Mutex
	used    int64
	waiting list.List // of *budgetWaiter, next to be served first
}

type budgetWaiter struct {
	n      int64
	urgent bool
	ready  chan struct{}
}

func newBudget(capacity int64) *budget {
	return &budget{capacity: capacity}
}

// acquire blocks until n bytes are free and returns the amount to hand back
// to release. A request for more than the whole budget is granted the whole
// budget, so it runs alone rather than never.
func (b *budget) acquire(n int64, urgent bool) int64 {
	n = max(0, min(n, b.capacity))
	b.mu.Lock()
	if b.used+n <= b.capacity && (urgent || b.waiting.Len() == 0) {
		b.used += n
		b.mu.Unlock()
		return n
	}
	w := &budgetWaiter{n: n, urgent: urgent, ready: make(chan struct{})}
	if urgent {
		// Behind the urgent requests already waiting, ahead of the others.
		at := b.waiting.Front()
		for at != nil && at.Value.(*budgetWaiter).urgent {
			at = at.Next()
		}
		if at != nil {
			b.waiting.InsertBefore(w, at)
		} else {
			b.waiting.PushBack(w)
		}
	} else {
		b.waiting.PushBack(w)
	}
	b.mu.Unlock()
	<-w.ready
	return n
}

func (b *budget) release(n int64) {
	if n == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used -= n
	for front := b.waiting.Front(); front != nil; front = b.waiting.Front() {
		w := front.Value.(*budgetWaiter)
		if b.used+w.n > b.capacity {
			break
		}
		b.used += w.n
		b.waiting.Remove(front)
		close(w.ready)
	}
}

// hold is one decode's claim on a budget. The nil hold claims nothing, which
// is what probes and tests use.
type hold struct {
	b      *budget
	urgent bool

	mu sync.Mutex
	n  int64
}

// reserve claims the memory for a decode of the given size: the planar PCM
// plus one more channel for the downmix or fold-down made from it. Decoders
// call it once the headers have told them how much they will allocate.
func (h *hold) reserve(frames, channels int) {
	if h == nil {
		return
	}
	h.release()
	n := h.b.acquire(4*int64(frames)*int64(channels+1), h.urgent)
	h.mu.Lock()
	h.n = n
	h.mu.Unlock()
}

// release gives the claim back. It is safe to call more than once.
func (h *hold) release() {
	if h == nil {
		return
	}
	h.mu.Lock()
	n := h.n
	h.n = 0
	h.mu.Unlock()
	h.b.release(n)
}
