package api

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"ssss/internal/analysis"
	"ssss/internal/ingest"
	"ssss/internal/library"
	"ssss/internal/model"
)

const (
	// focusDebounce waits for the window to stay focused before rescanning:
	// alt-tabbing through it should not start a scan.
	focusDebounce = 1500 * time.Millisecond
	// focusInterval is the least time between two focus-triggered rescans.
	focusInterval = 15 * time.Second
)

// audioFilter is the pattern for the native file dialog.
const audioFilter = "*.wav;*.wave;*.aif;*.aiff;*.aifc;*.flac;*.mp3;*.ogg"

// GetState returns the current library status and settings. The frontend
// asks for it once its event listeners are in place, which is the moment
// toasts raised during startup can finally be delivered.
func (a *API) GetState() model.AppState {
	a.releaseToasts()
	return a.state()
}

func (a *API) state() model.AppState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.stateLocked()
}

// CreateLibrary initialises a new library at path and opens it.
func (a *API) CreateLibrary(path, name string) (model.AppState, error) {
	path = strings.TrimSpace(path)
	if name = strings.TrimSpace(name); name == "" {
		name = "Samples"
	}
	a.openMu.Lock()
	defer a.openMu.Unlock()
	lib, err := library.Create(path, name, a.libraryOptions())
	if err != nil {
		return a.state(), err
	}
	return a.activate(lib, cacheKept, "")
}

// OpenLibrary opens an existing library at path. A folder without a library
// is an error; it is never initialised here.
func (a *API) OpenLibrary(path string) (model.AppState, error) {
	path = strings.TrimSpace(path)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return a.state(), fmt.Errorf("folder not found: %s", path)
	}
	if !library.IsLibrary(path) {
		return a.state(), library.ErrNotLibrary
	}
	a.openMu.Lock()
	defer a.openMu.Unlock()
	a.closeIfSame(path, false)
	lib, err := library.Open(path, a.libraryOptions())
	if err == nil {
		var state model.AppState
		if state, err = a.activate(lib, cacheKept, ""); err == nil {
			return state, nil
		}
	}
	// closeIfSame may have closed the library that was open; go back to
	// whatever the settings point at.
	return a.openConfigured(), err
}

// ChooseFolder shows a native folder dialog; it returns "" when cancelled.
func (a *API) ChooseFolder(title string) (string, error) {
	if a.app == nil {
		return "", errNoWindow
	}
	dialog := a.app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		SetTitle(title)
	if a.window != nil {
		dialog.AttachToWindow(a.window)
	}
	return dialog.PromptForSingleSelection()
}

// ChooseFiles shows a native multi-file dialog filtered to audio files.
func (a *API) ChooseFiles(title string) ([]string, error) {
	if a.app == nil {
		return []string{}, errNoWindow
	}
	dialog := a.app.Dialog.OpenFile().
		CanChooseFiles(true).
		AddFilter("Audio files", audioFilter).
		AddFilter("All files", "*").
		SetTitle(title)
	if a.window != nil {
		dialog.AttachToWindow(a.window)
	}
	paths, err := dialog.PromptForMultipleSelection()
	if paths == nil {
		paths = []string{}
	}
	return paths, err
}

// RetryLibrary re-checks a library that was unavailable.
func (a *API) RetryLibrary() model.AppState {
	return a.reopenIfAway()
}

// reopenIfAway opens the configured library if the session has none. While
// another transition is under way it does nothing: that one is already
// deciding what the session becomes.
func (a *API) reopenIfAway() model.AppState {
	if !a.openMu.TryLock() {
		return a.state()
	}
	defer a.openMu.Unlock()
	if s := a.current(); s != nil && s.lib != nil || a.closing.Load() {
		return a.state()
	}
	return a.openConfigured()
}

// Rescan reconciles the library folder with the pack files in the
// background; progress arrives through events.
func (a *API) Rescan() error {
	s, err := a.open()
	if err != nil {
		return err
	}
	opts := ingest.RescanOptions{AllowMassMissing: a.confirmMissing.Load()}
	a.bg.Go(func() { a.runRescan(s, opts) })
	return nil
}

// runRescan rescans and turns the outcome into state and toasts.
func (a *API) runRescan(s *session, opts ingest.RescanOptions) {
	res, err := s.eng.Rescan(opts)
	a.focusMu.Lock()
	a.lastRescan = time.Now()
	a.focusMu.Unlock()
	switch {
	case errors.Is(err, ingest.ErrBusy) || res.Cancelled:
		return
	case errors.Is(err, library.ErrUnavailable), errors.Is(err, library.ErrNotLibrary):
		a.lose(s, err)
		return
	case err != nil:
		// A session that has been replaced meanwhile fails in ways that
		// are nobody's concern any more.
		if a.current() == s {
			a.toastError(fmt.Errorf("rescan: %w", err))
		}
		return
	}
	if res.MissingStopped > 0 {
		// Asked once; a quiet rescan that trips again stays quiet.
		if !a.confirmMissing.Swap(true) || !opts.Quiet {
			a.toast(model.ToastError, fmt.Sprintf(
				"%d files are missing from the library folder. Nothing was changed in case it is still syncing; run Rescan again to mark them missing.",
				res.MissingStopped))
		}
		return
	}
	a.confirmMissing.Store(false)
	if !opts.Quiet {
		a.toast(model.ToastInfo, rescanSummary(res))
	}
}

func rescanSummary(res ingest.RescanResult) string {
	var parts []string
	add := func(n int, what string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, what))
		}
	}
	add(res.Added, "added")
	add(res.Healed, "relocated")
	add(res.Changed, "changed on disk")
	add(res.Found, "found again")
	add(res.Missing, "missing")
	add(res.Failed, "could not be added")
	add(res.Waiting, "left until their pack file has synced")
	add(res.Ignored, "previously removed, ignored")
	if len(parts) == 0 {
		return "Library is up to date"
	}
	return "Rescan: " + strings.Join(parts, ", ")
}

// RebuildCache throws the local index away and rebuilds it from the pack
// files. The database file itself is deleted, not emptied, so this also
// recovers from one that is damaged. The session is reopened for it, which
// ends the undo history.
func (a *API) RebuildCache() error {
	s, err := a.open()
	if err != nil {
		return err
	}
	a.bg.Go(func() { a.recache(s, cacheFresh, "Cache rebuilt") })
	return nil
}

// UpdateSettings validates and persists s and returns what was stored.
func (a *API) UpdateSettings(s model.Settings) (model.Settings, error) {
	s = library.NormalizeSettings(s)
	if s.ProjectKey != "" {
		// An unreadable key is dropped rather than refused: the settings
		// arrive as a whole and the rest must still be saved.
		s.ProjectKey, _ = analysis.NormalizeKey(s.ProjectKey)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Settings = s
	return s, a.saveConfigLocked()
}

// start loads the settings and opens the last library.
func (a *API) start() {
	path, err := library.ConfigPath()
	if err != nil {
		a.toastError(err)
		return
	}
	cfg, err := library.LoadConfig(path)
	if err != nil {
		a.toastError(err)
	}
	a.mu.Lock()
	a.cfgPath, a.cfg = path, cfg
	a.mu.Unlock()
	a.openMu.Lock()
	defer a.openMu.Unlock()
	a.openConfigured()
}

// windowFocused schedules a quiet rescan once the window has kept focus for
// a moment: that is when files synced or moved by other programs show up.
// An unavailable library is retried instead.
func (a *API) windowFocused() {
	a.focusMu.Lock()
	defer a.focusMu.Unlock()
	if a.focusTimer != nil {
		a.focusTimer.Stop()
	}
	a.focusTimer = time.AfterFunc(focusDebounce, func() {
		a.focusMu.Lock()
		recent := time.Since(a.lastRescan) < focusInterval
		a.focusMu.Unlock()
		s := a.current()
		switch {
		case s == nil || recent:
		case s.lib == nil:
			// Only a folder that is back is worth reopening the session for.
			if library.IsLibrary(a.GetState().LibraryPath) {
				a.openConfigured()
			}
		default:
			a.bg.Go(func() { a.runRescan(s, ingest.RescanOptions{Quiet: true}) })
		}
	})
}

// shutdown stops background work and writes everything that is pending.
func (a *API) shutdown() {
	a.focusMu.Lock()
	if a.focusTimer != nil {
		a.focusTimer.Stop()
	}
	a.focusMu.Unlock()

	a.closing.Store(true)
	// Taking openMu lets a transition in flight finish and keeps a late
	// one from installing a session nobody will close. It is released
	// before waiting for the background work, some of which wants it.
	a.openMu.Lock()
	a.mu.Lock()
	s := a.sess
	a.sess = nil
	a.status = statusOf(nil)
	saveErr := a.saveConfigLocked()
	a.mu.Unlock()
	if err := errors.Join(saveErr, s.close(false)); err != nil {
		// Nobody is left to show a toast to.
		fmt.Fprintln(os.Stderr, "ssss: shutdown:", err)
	}
	a.openMu.Unlock()
	a.bg.Wait()
}
