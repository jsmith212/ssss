package audio

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBudgetBoundsConcurrentUse(t *testing.T) {
	b := newBudget(100)
	var inUse, peak atomic.Int64
	var wg sync.WaitGroup
	for i := range 64 {
		wg.Go(func() {
			want := int64(10 + i%5*20) // 10..90
			n := b.acquire(want, i%7 == 0)
			now := inUse.Add(n)
			for p := peak.Load(); now > p && !peak.CompareAndSwap(p, now); p = peak.Load() {
			}
			time.Sleep(time.Millisecond)
			inUse.Add(-n)
			b.release(n)
		})
	}
	wg.Wait()
	if peak.Load() > 100 {
		t.Errorf("%d bytes in use at once, budget is 100", peak.Load())
	}
	if b.used != 0 || b.waiting.Len() != 0 {
		t.Errorf("budget not returned: used %d, %d waiting", b.used, b.waiting.Len())
	}
}

// A request larger than the whole budget runs alone instead of never.
func TestBudgetOversizedRequest(t *testing.T) {
	b := newBudget(100)
	small := b.acquire(10, false)
	done := make(chan int64)
	go func() { done <- b.acquire(1000, false) }()
	waitFor(t, func() bool { return b.queued() == 1 })
	b.release(small)
	if n := <-done; n != 100 {
		t.Errorf("oversized request was granted %d, want the whole budget", n)
	}
	b.release(100)
}

// Waiters are served in order, except that urgent ones go first.
func TestBudgetOrder(t *testing.T) {
	b := newBudget(100)
	all := b.acquire(100, false)
	var mu sync.Mutex
	var order []string
	enqueue := func(name string, n int64, urgent bool) {
		before := b.queued()
		go func() {
			got := b.acquire(n, urgent)
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			b.release(got)
		}()
		waitFor(t, func() bool { return b.queued() == before+1 })
	}
	enqueue("big", 100, false)
	enqueue("small", 1, false)
	enqueue("urgent 1", 100, true)
	enqueue("urgent 2", 100, true)
	b.release(all)
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(order) == 4 })
	want := []string{"urgent 1", "urgent 2", "big", "small"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("served in order %v, want %v", order, want)
		}
	}
	// With room to spare, an urgent request does not queue behind anyone.
	half := b.acquire(50, false)
	go b.acquire(100, false)
	waitFor(t, func() bool { return b.queued() == 1 })
	b.release(b.acquire(50, true))
	b.release(half)
}

func (b *budget) queued() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.waiting.Len()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
	}
}

// swapBudget replaces the process-wide budget for one test.
func swapBudget(t *testing.T, capacity int64) *budget {
	t.Helper()
	old := decodeBudget
	decodeBudget = newBudget(capacity)
	t.Cleanup(func() { decodeBudget = old })
	return decodeBudget
}

func TestDecodeHeldClaimsUntilReleased(t *testing.T) {
	path := writeTemp(t, "a.wav", buildWAV(wavSpec{enc: encIntLE, size: 2, frames: 1000})) // stereo
	b := swapBudget(t, 1<<20)

	pcm, _, release, err := DecodeHeld(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(4 * pcm.Frames * (pcm.Channels + 1)); b.used != want {
		t.Errorf("holding %d bytes, want %d", b.used, want)
	}
	release()
	release() // harmless
	if b.used != 0 {
		t.Errorf("%d bytes still claimed after release", b.used)
	}

	if _, _, err := DecodeN(path, 0); err != nil || b.used != 0 {
		t.Errorf("DecodeN: err %v, %d bytes still claimed", err, b.used)
	}
	if _, err := loadClip(path, 10); err != nil || b.used != 0 {
		t.Errorf("loadClip: err %v, %d bytes still claimed", err, b.used)
	}
	if _, err := Probe(path); err != nil || b.used != 0 {
		t.Errorf("Probe: err %v, %d bytes claimed", err, b.used)
	}

	// Failures give the claim back too, and still hand out a release.
	bad := writeTemp(t, "bad.flac", buildFLAC(t, 16, 2)[:2000]) // ends mid-stream
	_, _, release, _ = DecodeHeld(filepath.Join(t.TempDir(), "missing.wav"), 0)
	release()
	if _, _, release, err = DecodeHeld(bad, 0); err == nil {
		t.Error("truncated FLAC decoded without error")
	}
	release()
	if b.used != 0 {
		t.Errorf("%d bytes still claimed after failed decodes", b.used)
	}
}

// Many workers decoding at once never hold more than the budget.
func TestDecodeHeldIsBounded(t *testing.T) {
	path := writeTemp(t, "a.wav", buildWAV(wavSpec{enc: encIntLE, size: 2, channels: 1, frames: 1000}))
	const each = 4 * 1000 * 2
	swapBudget(t, 3*each)
	var held, peak atomic.Int64
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			_, _, release, err := DecodeHeld(path, 0)
			if err != nil {
				t.Error(err)
				return
			}
			now := held.Add(1)
			for p := peak.Load(); now > p && !peak.CompareAndSwap(p, now); p = peak.Load() {
			}
			time.Sleep(time.Millisecond)
			held.Add(-1)
			release()
		})
	}
	wg.Wait()
	if peak.Load() > 3 {
		t.Errorf("%d decodes held at once, budget allows 3", peak.Load())
	}
}

func TestMonoN(t *testing.T) {
	p := newPCM(100, 2, 4)
	copy(p.Data[0], []float32{1, 1, 1, 1})
	copy(p.Data[1], []float32{0, 1, 0, 1})
	if got := p.MonoN(3); len(got) != 3 || got[0] != 0.5 || got[1] != 1 {
		t.Errorf("MonoN(3) = %v", got)
	}
	if got := p.MonoN(99); len(got) != 4 {
		t.Errorf("MonoN past the end has %d frames", len(got))
	}
	one := newPCM(100, 1, 4)
	if got := one.MonoN(2); len(got) != 2 || &got[0] != &one.Data[0][0] {
		t.Error("MonoN of a mono signal should be a view of it")
	}
}
