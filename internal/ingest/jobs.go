package ingest

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"ssss/internal/library"
	"ssss/internal/model"
)

// progressInterval throttles progress events to about ten a second.
const progressInterval = 100 * time.Millisecond

// changeInterval batches "samples changed" notifications from background
// work, so a fast import refreshes the list a few times a second instead of
// once per file.
const changeInterval = 250 * time.Millisecond

func newJobID() string { return "job_" + ulid.Make().String() }

// job is one cancellable background task with throttled progress.
type job struct {
	e      *Engine
	event  string
	cancel context.CancelFunc

	mu   sync.Mutex
	p    model.Progress
	sent time.Time
}

type jobSet struct {
	mu   sync.Mutex
	byID map[string]*job
}

func (s *jobSet) init() { s.byID = map[string]*job{} }

func (s *jobSet) cancel(id string) bool {
	s.mu.Lock()
	j := s.byID[id]
	s.mu.Unlock()
	if j == nil {
		return false
	}
	j.cancel()
	return true
}

func (s *jobSet) cancelAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.byID {
		j.cancel()
	}
}

// startJob registers a job and counts it as active until finish.
func (e *Engine) startJob(kind string) (*job, context.Context) {
	ctx, cancel := context.WithCancel(e.ctx)
	j := &job{
		e: e, event: model.EventIngestProgress, cancel: cancel,
		p: model.Progress{JobID: newJobID(), Kind: kind, Errors: []string{}},
	}
	e.jobs.mu.Lock()
	e.jobs.byID[j.p.JobID] = j
	e.jobs.mu.Unlock()
	e.begin(1)
	return j, ctx
}

func (j *job) id() string { return j.p.JobID }

// update changes the progress and emits it if the last event is old enough.
func (j *job) update(fn func(p *model.Progress)) {
	j.mu.Lock()
	fn(&j.p)
	due := time.Since(j.sent) >= progressInterval
	var snap model.Progress
	if due {
		j.sent = time.Now()
		snap = j.snapshotLocked()
	}
	j.mu.Unlock()
	if due {
		j.emit(snap)
	}
}

func (j *job) addError(name string, err error) {
	j.update(func(p *model.Progress) {
		p.Failed++
		if len(p.Errors) < model.MaxProgressErrors {
			p.Errors = append(p.Errors, fmt.Sprintf("%s: %v", name, err))
		}
	})
}

func (j *job) snapshotLocked() model.Progress {
	snap := j.p
	snap.Errors = slices.Clone(j.p.Errors)
	return snap
}

func (j *job) emit(p model.Progress) {
	if j.e.notify.Progress != nil {
		j.e.notify.Progress(j.event, p)
	}
}

// finish emits the final state, whatever the throttle says. With silent set
// nothing is emitted: a quiet rescan that found nothing to do stays quiet.
func (j *job) finish(cancelled, silent bool) model.Progress {
	j.mu.Lock()
	j.p.Finished = true
	j.p.Cancelled = cancelled
	j.p.Current = ""
	snap := j.snapshotLocked()
	j.mu.Unlock()

	j.cancel()
	j.e.jobs.mu.Lock()
	delete(j.e.jobs.byID, snap.JobID)
	j.e.jobs.mu.Unlock()
	j.e.changes.flush()
	if !silent {
		j.emit(snap)
	}
	j.e.end(1)
	return snap
}

// changeBatcher coalesces change notifications from background work.
type changeBatcher struct {
	e *Engine

	mu         sync.Mutex
	ids        map[string]struct{}
	all        bool
	structural bool
	timer      *time.Timer
	stopped    bool
}

func (b *changeBatcher) init(e *Engine) {
	b.e = e
	b.ids = map[string]struct{}{}
}

func (b *changeBatcher) add(ch library.Change) {
	if ch.Empty() {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, id := range ch.IDs {
		b.ids[id] = struct{}{}
	}
	for _, id := range ch.Removed {
		b.ids[id] = struct{}{}
	}
	b.all = b.all || ch.All
	b.structural = b.structural || ch.Structural
	if b.timer == nil && !b.stopped {
		b.timer = time.AfterFunc(changeInterval, b.flush)
	}
}

func (b *changeBatcher) flush() {
	b.mu.Lock()
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	if len(b.ids) == 0 && !b.all && !b.structural {
		b.mu.Unlock()
		return
	}
	var ids []string
	if !b.all {
		ids = make([]string, 0, len(b.ids))
		for id := range b.ids {
			ids = append(ids, id)
		}
		slices.Sort(ids)
	}
	structural := b.structural
	b.ids = map[string]struct{}{}
	b.all, b.structural = false, false
	b.mu.Unlock()

	if b.e.notify.Changed != nil {
		b.e.notify.Changed(ids, structural)
	}
}

func (b *changeBatcher) stop() {
	b.mu.Lock()
	b.stopped = true
	b.mu.Unlock()
	b.flush()
}
