// Package api is the Wails-bound service the frontend talks to. Every
// exported method on API is part of the frontend contract and appears in the
// generated TypeScript bindings, so helpers that the host needs but the
// frontend must not see are unexported methods reached through package-level
// functions (see host.go).
package api

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"ssss/internal/audio"
	"ssss/internal/library"
	"ssss/internal/model"
)

var (
	errNoLibrary   = errors.New("no library is open")
	errUnavailable = errors.New("the library folder is unavailable")
	errNoWindow    = errors.New("the application window is not ready")
	// errLocked: another copy of the app has the library's cache.
	errLocked = errors.New("this library is already open in another ssss window on this computer")
)

// API implements the service contract. Its state is the configuration file
// plus at most one session: the open library, or the cached index of a
// library whose folder cannot be reached.
type API struct {
	app    *application.App
	window *application.WebviewWindow
	// sink, when set, receives every emitted event; tests use it in place
	// of a running application.
	sink func(name string, data any)

	audio *audio.Server

	// openMu serialises session transitions (open, create, lose, rebuild,
	// shutdown): two of them interleaving would open the same folder
	// twice, and two Library values on one folder write over each other.
	// It is taken before mu and never held while waiting for bg.
	openMu sync.Mutex

	mu      sync.RWMutex
	cfgPath string
	cfg     library.Config
	sess    *session
	status  string
	message string

	// bg tracks goroutines started on behalf of a session.
	bg sync.WaitGroup

	focusMu    sync.Mutex
	focusTimer *time.Timer
	lastRescan time.Time
	// confirmMissing is set after a rescan stopped at the missing-files
	// safety check; the next manual rescan is then the user's go-ahead.
	confirmMissing atomic.Bool
	// closing silences events once shutdown has begun.
	closing atomic.Bool

	// Toasts raised before the frontend can hear them (while the last
	// library is opened at startup) wait here for its first GetState.
	toastMu sync.Mutex
	holding bool
	held    []model.Toast
}

// New returns an API with no library open and default settings. It reads
// nothing from disk; see Start.
func New() *API {
	a := &API{cfg: library.DefaultConfig()}
	a.status = statusOf(nil)
	a.audio = audio.NewServer(a.resolveAudio, "")
	return a
}

// current returns the active session, which may be nil.
func (a *API) current() *session {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sess
}

// open returns the session if it has a library to read from.
func (a *API) open() (*session, error) {
	s := a.current()
	switch {
	case s == nil:
		return nil, errNoLibrary
	case s.lib == nil:
		return nil, errUnavailable
	}
	return s, nil
}

// writable returns the session if its library accepts changes.
func (a *API) writable() (*session, error) {
	s, err := a.open()
	if err != nil {
		return nil, err
	}
	if s.lib.ReadOnly() {
		return nil, library.ErrReadOnly
	}
	return s, nil
}

func (a *API) resolveAudio(id string) (string, bool) {
	s := a.current()
	if s == nil || s.lib == nil {
		return "", false
	}
	return s.lib.AbsPath(id)
}
