// Package ingest copies files into the library with hashing and dedupe, runs
// the background analysis worker pool, rescans the library and keeps the
// local index in step with it.
package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ssss/internal/index"
	"ssss/internal/library"
	"ssss/internal/model"
)

// ErrBusy is returned when a rescan is asked for while one is running.
var ErrBusy = errors.New("a rescan is already running")

// Notifier receives what the engine does in the background. Every callback
// may be nil and must be safe for concurrent use.
type Notifier struct {
	// Progress reports a job; event is model.EventIngestProgress or
	// model.EventAnalysisProgress.
	Progress func(event string, p model.Progress)
	// Changed says which samples to refetch; nil ids means all of them.
	Changed func(ids []string, structural bool)
	// Failed reports a background problem worth showing to the user.
	Failed func(err error)
}

// Engine keeps the index in step with one open library and runs the work
// that changes it in the background: imports, analysis and rescans. Close it
// before closing the library or the index.
type Engine struct {
	lib      *library.Library
	idx      *index.Index
	cacheDir string
	notify   Notifier

	ctx  context.Context
	stop context.CancelFunc

	// syncMu serialises index writes derived from library state, so an
	// upsert never lands in the middle of a full sync.
	syncMu sync.Mutex
	packs  map[string]model.PackInfo
	colls  map[string]library.CollectionList
	marked bool
	// stale is set when an index write failed: the index then lacks a
	// change the library has, and nothing but a full sync can tell which.
	stale      bool
	lastRepair time.Time

	jobs     jobSet
	changes  changeBatcher
	analysis analyzer
	rescanMu sync.Mutex
	// ignored remembers files under Samples that a rescan looked at and
	// left alone (not audio, a second copy, a failed adoption), so the next
	// rescan does not read them again. Guarded by rescanMu.
	ignored map[string]fileStat
	// waiting are files a rescan has hashed but not adopted yet, with
	// their hash, so that looking again costs nothing. Guarded by rescanMu.
	waiting map[string]hashedFile
	// warned are the reload warnings the last rescan reported.
	warned map[string]struct{}
	// verified is this machine's record of the version of each sample's
	// file that is known to match the library. Guarded by rescanMu.
	verified *statCache
	swept    bool

	// active counts background work in flight, for Wait.
	activeMu sync.Mutex
	activeCv *sync.Cond
	active   int
}

// New returns an engine for lib and idx. cacheDir is the library's
// per-machine cache folder; full-resolution peaks go under it.
func New(lib *library.Library, idx *index.Index, cacheDir string, n Notifier) *Engine {
	ctx, stop := context.WithCancel(context.Background())
	e := &Engine{
		lib: lib, idx: idx, cacheDir: cacheDir, notify: n, ctx: ctx, stop: stop,
		packs: map[string]model.PackInfo{}, colls: map[string]library.CollectionList{},
		ignored: map[string]fileStat{}, waiting: map[string]hashedFile{},
	}
	e.activeCv = sync.NewCond(&e.activeMu)
	e.jobs.init()
	e.changes.init(e)
	e.analysis.init(e)
	// A write can find that another machine's files arrived and merge them
	// in; the index and the frontend have to follow as after a reload.
	lib.OnExternalChange(func(ch library.Change) {
		e.record(ch, 0)
		e.AnalyzePending()
	})
	return e
}

// PeaksDir is where full-resolution peaks are cached.
func PeaksDir(cacheDir string) string { return filepath.Join(cacheDir, "peaks") }

// IndexPath is the index database inside a library's cache folder.
func IndexPath(cacheDir string) string { return filepath.Join(cacheDir, "index.db") }

// dirtyMarker exists while the index may hold changes the pack files do
// not: from the first change of a session until a clean Close. Finding it at
// startup means the last session crashed and the stamps cannot be trusted.
func dirtyMarker(cacheDir string) string { return filepath.Join(cacheDir, "dirty") }

// Close cancels background work, waits for it, writes the library and
// records in the index which pack files it now reflects.
func (e *Engine) Close() error {
	e.stop()
	e.jobs.cancelAll()
	e.analysis.stop()
	e.Wait()
	e.changes.stop()
	if e.lib.ReadOnly() {
		return nil
	}
	if err := e.lib.Flush(); err != nil {
		return err
	}
	return e.stampPacks()
}

// Wait blocks until no import, rescan or analysis is running and every
// pending change notification has been delivered.
func (e *Engine) Wait() {
	e.activeMu.Lock()
	for e.active > 0 {
		e.activeCv.Wait()
	}
	e.activeMu.Unlock()
	e.changes.flush()
}

func (e *Engine) begin(n int) {
	e.activeMu.Lock()
	e.active += n
	e.activeMu.Unlock()
}

func (e *Engine) end(n int) {
	e.activeMu.Lock()
	e.active -= n
	if e.active <= 0 {
		e.activeCv.Broadcast()
	}
	e.activeMu.Unlock()
}

func (e *Engine) fail(err error) {
	if err != nil && e.notify.Failed != nil && e.ctx.Err() == nil {
		e.notify.Failed(err)
	}
}

// Cancel stops the job with the given id; it reports whether there was one.
func (e *Engine) Cancel(jobID string) bool {
	if e.analysis.cancel(jobID) {
		return true
	}
	return e.jobs.cancel(jobID)
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
