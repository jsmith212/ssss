package ingest

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"runtime"
	"slices"
	"sync"
	"time"

	"ssss/internal/analysis"
	"ssss/internal/audio"
	"ssss/internal/library"
	"ssss/internal/model"
)

// analysisSeconds bounds how much audio tempo and key detection look at; a
// long recording says nothing more about either after the first minutes.
const analysisSeconds = 120

// analyzer is the background analysis pool. One "job" lasts from the first
// queued sample until the queue drains; the next sample starts a new one.
type analyzer struct {
	e *Engine

	mu        sync.Mutex
	cv        *sync.Cond
	queue     []string
	queued    map[string]struct{}
	running   int
	workers   int
	stopped   bool
	cancelled bool
	// holds counts imports in flight. Analysis usually outruns the copy;
	// without the hold every lull would end the job and the next file
	// would start another, one progress pill after the other.
	holds int
	p     model.Progress
	sent  time.Time
}

func (a *analyzer) init(e *Engine) {
	a.e = e
	a.cv = sync.NewCond(&a.mu)
	a.queued = map[string]struct{}{}
}

func poolSize() int { return max(1, runtime.NumCPU()-2) }

// enqueue adds samples to the queue; ones already waiting are not doubled.
func (a *analyzer) enqueue(ids []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return
	}
	n := 0
	for _, id := range ids {
		if _, dup := a.queued[id]; dup {
			continue
		}
		a.queued[id] = struct{}{}
		a.queue = append(a.queue, id)
		n++
	}
	if n == 0 {
		return
	}
	if a.p.JobID == "" {
		a.p = model.Progress{JobID: newJobID(), Kind: model.JobAnalysis, Errors: []string{}}
		a.cancelled = false
	}
	a.p.Total += n
	a.e.begin(n)
	for a.workers < min(poolSize(), len(a.queue)+a.running) {
		a.workers++
		go a.work()
	}
	a.cv.Broadcast()
}

func (a *analyzer) work() {
	a.mu.Lock()
	for {
		for len(a.queue) == 0 && !a.stopped {
			a.cv.Wait()
		}
		if a.stopped {
			a.workers--
			a.mu.Unlock()
			return
		}
		id := a.queue[0]
		a.queue = a.queue[1:]
		a.running++
		a.mu.Unlock()

		err := a.e.analyzeSample(id)

		a.mu.Lock()
		a.running--
		delete(a.queued, id)
		a.p.Done++
		if err != nil {
			a.p.Failed++
			if len(a.p.Errors) < model.MaxProgressErrors {
				a.p.Errors = append(a.p.Errors, err.Error())
			}
		}
		snap, emit := a.progressLocked()
		a.mu.Unlock()
		if emit {
			a.emit(snap)
		}
		a.e.end(1)
		a.mu.Lock()
	}
}

// progressLocked decides whether to emit after a sample: always when the
// queue has drained, otherwise at the throttled rate.
func (a *analyzer) progressLocked() (model.Progress, bool) {
	if len(a.queue) == 0 && a.running == 0 && a.holds == 0 {
		a.p.Finished = true
		a.p.Cancelled = a.cancelled
		snap := a.p
		a.p = model.Progress{}
		return snap, true
	}
	if time.Since(a.sent) < progressInterval {
		return model.Progress{}, false
	}
	a.sent = time.Now()
	snap := a.p
	snap.Errors = slices.Clone(a.p.Errors)
	return snap, true
}

func (a *analyzer) emit(p model.Progress) {
	if p.Finished {
		a.e.changes.flush()
	}
	if a.e.notify.Progress != nil {
		a.e.notify.Progress(model.EventAnalysisProgress, p)
	}
}

// hold keeps the current analysis job open until release, even when the
// queue runs dry.
func (a *analyzer) hold() {
	a.mu.Lock()
	a.holds++
	a.mu.Unlock()
}

func (a *analyzer) release() {
	a.mu.Lock()
	a.holds--
	var snap model.Progress
	emit := false
	if a.p.JobID != "" && a.holds == 0 && len(a.queue) == 0 && a.running == 0 {
		snap, emit = a.progressLocked()
	}
	a.mu.Unlock()
	if emit {
		a.emit(snap)
	}
}

// dropQueueLocked forgets the waiting samples and returns how many.
func (a *analyzer) dropQueueLocked() int {
	n := len(a.queue)
	for _, id := range a.queue {
		delete(a.queued, id)
	}
	a.queue = nil
	a.p.Total -= n
	return n
}

// cancel drops the waiting samples of the job; ones being analysed finish.
func (a *analyzer) cancel(jobID string) bool {
	a.mu.Lock()
	if jobID == "" || a.p.JobID != jobID {
		a.mu.Unlock()
		return false
	}
	n := a.dropQueueLocked()
	a.cancelled = true
	var snap model.Progress
	emit := false
	if a.running == 0 {
		snap, emit = a.progressLocked()
	}
	a.mu.Unlock()
	if emit {
		a.emit(snap)
	}
	a.e.end(n)
	return true
}

func (a *analyzer) stop() {
	a.mu.Lock()
	a.stopped = true
	n := a.dropQueueLocked()
	a.cv.Broadcast()
	a.mu.Unlock()
	a.e.end(n)
}

// Analyze queues samples for (re-)analysis.
func (e *Engine) Analyze(ids []string) {
	e.analysis.enqueue(ids)
}

// AnalyzePending queues every sample that has not been analysed yet, which
// is how an import interrupted by quitting picks up again.
func (e *Engine) AnalyzePending() {
	if e.lib.ReadOnly() {
		return
	}
	var ids []string
	for _, s := range e.lib.Samples() {
		if !s.Analyzed {
			ids = append(ids, s.ID)
		}
	}
	e.analysis.enqueue(ids)
}

func (e *Engine) packDir(id string) string {
	e.syncMu.Lock()
	defer e.syncMu.Unlock()
	return e.packs[id].Dir
}

// analysisPath is the path analysis parses for tempo, key and tags: the
// pack's own folder name counts too ("Dubstep 140/Bass_01.wav").
func analysisPath(packDir, rel string) string {
	return path.Join(path.Base(packDir), rel)
}

func hintsFrom(m audio.Meta) analysis.Hints {
	var h analysis.Hints
	if !m.HasAcid {
		return h
	}
	switch {
	case m.OneShot:
		h.Type = analysis.TypeOneShot
	case m.Beats > 0 && m.Tempo > 0:
		// Many writers leave a default acid chunk on everything; only one
		// that states a length in beats is describing a loop.
		h.Type = analysis.TypeLoop
		h.BPM = m.Tempo
	}
	return h
}

// analyzeSample decodes one file once and derives everything from it: the
// peaks, the format facts and the detected type, tempo and key, merged with
// what is already stored by source priority.
func (e *Engine) analyzeSample(id string) error {
	packID, rec, ok := e.lib.Record(id)
	if !ok {
		return nil // removed while queued
	}
	abs, _ := e.lib.AbsPath(id)
	name := path.Base(rec.Path)

	info, err := audio.Probe(abs)
	if errors.Is(err, fs.ErrNotExist) {
		// Not on this machine (yet); stays unanalysed for a later run.
		return nil
	}
	if err != nil {
		e.applyAnalysis(id, library.Analysis{})
		return fmt.Errorf("%s: %w", name, unwrapPath(err))
	}
	result := library.Analysis{Fmt: string(info.Format), Dur: info.Duration(), SR: info.Rate, Ch: info.Channels}
	// The record follows the file as analysed, which a re-analysis after
	// the file was edited in place relies on.
	if st, err := os.Stat(abs); err == nil {
		result.Size, result.MTime = st.Size(), st.ModTime().UTC()
	}
	// Decode what the player serves, so the cached peaks match what plays.
	// The decode's claim on the process-wide memory budget is held until
	// the analysis is done with the audio; nothing below decodes again.
	pcm, _, release, err := audio.DecodeHeld(abs, audio.MaxPlaybackSeconds*info.Rate)
	defer release()
	if err != nil {
		e.applyAnalysis(id, result)
		return fmt.Errorf("%s: %w", name, unwrapPath(err))
	}
	stereo := pcm.Stereo()
	result.Mini = base64.StdEncoding.EncodeToString(audio.Mini(stereo))
	// The peaks cache is an optimisation; the server recomputes on a miss.
	_ = audio.SavePeaks(PeaksDir(e.cacheDir), id, audio.Full(stereo))

	mono := pcm.MonoN(analysisSeconds * pcm.Rate)
	hints := hintsFrom(info.Meta)
	// Analysis sees the opening of a long file and has to know that it is
	// not looking at all of it.
	hints.Dur = info.Duration()
	res := analysis.Analyze(mono, pcm.Rate, analysisPath(e.packDir(packID), rec.Path), hints)
	merged := analysis.Merge(analysis.Meta{
		Type: rec.Type, TypeSrc: rec.TypeSrc,
		BPM: rec.BPM, BPMSrc: rec.BPMSrc,
		Key: rec.Key, KeySrc: rec.KeySrc,
	}, res)
	result.Detected = &library.Detected{
		BPM: res.BPM, BPMConf: res.BPMConf, Key: res.Key, KeyConf: res.KeyConf, KeyRoot: res.Key != "" && res.KeyRootOnly, Type: res.Type,
	}
	result.Type, result.TypeSrc = merged.Type, merged.TypeSrc
	result.BPM, result.BPMSrc = merged.BPM, merged.BPMSrc
	result.Key, result.KeySrc = merged.Key, merged.KeySrc
	return e.applyAnalysis(id, result)
}

func (e *Engine) applyAnalysis(id string, a library.Analysis) error {
	ch, err := e.lib.ApplyAnalysis(id, a)
	if errors.Is(err, library.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	e.record(ch, 0)
	return nil
}
