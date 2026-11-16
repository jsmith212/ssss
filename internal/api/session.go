package api

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"ssss/internal/index"
	"ssss/internal/ingest"
	"ssss/internal/library"
	"ssss/internal/model"
)

// session is one opened library with its per-machine cache. While the
// library folder cannot be reached, lib and eng are nil and idx (if this
// machine has a cache of that library) serves read-only browsing.
type session struct {
	lib      *library.Library
	idx      *index.Index
	eng      *ingest.Engine
	cacheDir string
	history  history

	// unlock releases the cache folder's lock; nil when none is held.
	unlock func()
	// cache says how the cache was obtained, which is also how far the
	// attempts to get a working one have gone.
	cache cacheMode
	// done is a toast to show once the session has warmed up.
	done string
}

// cacheMode is one of the ways to give a library a cache, in the order they
// are tried when the previous one turns out not to work.
type cacheMode int

const (
	cacheKept  cacheMode = iota // the cache folder as it is
	cacheFresh                  // the cache folder, index deleted and rebuilt
	cacheTemp                   // a temporary folder, gone when the session ends
)

// close ends the session. With cacheDoomed set, complaints from the index
// are dropped: it is about to be deleted. What the library has to say about
// its own files is reported either way (an engine that could not write them
// leaves that to the library's own close).
func (s *session) close(cacheDoomed bool) error {
	if s == nil {
		return nil
	}
	var errs []error
	keep := func(err error) {
		if !cacheDoomed {
			errs = append(errs, err)
		}
	}
	if s.eng != nil {
		keep(s.eng.Close())
	}
	if s.lib != nil {
		errs = append(errs, s.lib.Close())
	}
	if s.idx != nil {
		keep(s.idx.Close())
	}
	if s.unlock != nil {
		s.unlock()
	}
	if s.cache == cacheTemp && s.cacheDir != "" {
		os.RemoveAll(s.cacheDir)
	}
	return errors.Join(errs...)
}

func statusOf(s *session) string {
	switch {
	case s == nil:
		return model.StatusNone
	case s.lib == nil:
		return model.StatusUnavailable
	case s.lib.ReadOnly():
		return model.StatusReadOnly
	}
	return model.StatusOK
}

// stateLocked builds the AppState; the caller holds mu.
func (a *API) stateLocked() model.AppState {
	return model.AppState{
		Status:             a.status,
		LibraryPath:        a.cfg.LibraryPath,
		LibraryName:        a.cfg.LibraryName,
		Message:            a.message,
		Platform:           runtime.GOOS,
		DefaultLibraryPath: defaultLibraryPath(),
		Settings:           a.cfg.Settings,
	}
}

func defaultLibraryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Music", "Samples")
}

func (a *API) libraryOptions() library.Options {
	return library.Options{OnWriteError: func(err error) {
		a.toastError(fmt.Errorf("could not save library changes: %w", err))
		a.libraryLost(err)
	}}
}

func (a *API) notifier() ingest.Notifier {
	return ingest.Notifier{
		Progress: func(event string, p model.Progress) { a.emit(event, p) },
		Changed: func(ids []string, structural bool) {
			a.emit(model.EventSamplesChanged, model.SamplesChanged{IDs: ids, Structural: structural})
		},
		Failed: func(err error) {
			a.toastError(err)
			a.libraryLost(err)
		},
	}
}

// libraryLost reacts to background work reporting that the library folder
// has gone (drive unplugged, folder moved): the session switches to browsing
// the cache, as it would at the next rescan, instead of accepting changes
// that can no longer be saved. It may be called from inside the engine, so
// the switch, which waits for the engine, happens on another goroutine.
func (a *API) libraryLost(err error) {
	if !errors.Is(err, library.ErrUnavailable) || a.closing.Load() {
		return
	}
	if s := a.current(); s != nil && s.lib != nil {
		a.bg.Go(func() { a.lose(s, err) })
	}
}

// lose makes s, if it is still the session, an unavailable one.
func (a *API) lose(s *session, cause error) {
	a.openMu.Lock()
	defer a.openMu.Unlock()
	if a.current() != s || a.closing.Load() {
		return
	}
	a.setUnavailable(s.lib.Root(), s.lib.Info().ID, cause)
}

// saveConfigLocked persists the settings file; the caller holds mu.
func (a *API) saveConfigLocked() error {
	if a.cfgPath == "" {
		return nil
	}
	return library.SaveConfig(a.cfgPath, a.cfg)
}

// installLocked makes s the active session and returns the one it replaced,
// which the caller closes after releasing mu: closing waits for background
// work that may itself be waiting for mu.
func (a *API) installLocked(s *session, path, message string) *session {
	old := a.sess
	a.sess = s
	a.status = statusOf(s)
	a.message = message
	a.cfg.LibraryPath = path
	peaks := ""
	if s != nil && s.lib != nil {
		info := s.lib.Info()
		a.cfg.LibraryID, a.cfg.LibraryName = info.ID, info.Name
		peaks = ingest.PeaksDir(s.cacheDir)
		if a.message == "" {
			a.message = info.Message
		}
	}
	a.audio.SetPeaksDir(peaks)
	a.confirmMissing.Store(false)
	return old
}

// sameFolder reports whether two paths name the same existing folder.
func sameFolder(a, b string) bool {
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}

// closeIfSame closes the active session first when it has path open: two
// Library values on one folder would overwrite each other's files. Until
// the caller installs the next session there is a placeholder without a
// library. The caller holds openMu.
func (a *API) closeIfSame(path string, cacheDoomed bool) {
	a.mu.Lock()
	s := a.sess
	same := s != nil && s.lib != nil && sameFolder(s.lib.Root(), path)
	if same {
		a.sess = &session{}
		a.status = model.StatusUnavailable
	}
	a.mu.Unlock()
	if same {
		a.closeSession(s, cacheDoomed)
	}
}

func (a *API) closeSession(s *session, cacheDoomed bool) {
	if err := s.close(cacheDoomed); err != nil {
		a.toastError(fmt.Errorf("could not close the library cleanly: %w", err))
	}
}

// discardIndex deletes the index database of a cache folder.
func discardIndex(cacheDir string) {
	base := ingest.IndexPath(cacheDir)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(base + suffix)
	}
}

// openCache gives a library its per-machine cache: the folder, the lock that
// keeps a second copy of the app out of it, and the index. note is non-empty
// when the usual cache could not be used and a temporary one stands in.
//
// A cache that cannot be opened is no reason to call the library
// unavailable: the cache is derived data, and the library folder is fine.
// Only the lock being held refuses the library, because a second writer on
// the same folder is what must not happen.
func openCache(libraryID string, mode cacheMode) (s *session, note string, err error) {
	s = &session{cache: mode}
	var cause error
	if mode != cacheTemp {
		cause = func() error {
			dir, err := library.CacheDir(libraryID)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			unlock, err := lockFile(filepath.Join(dir, "lock"))
			if err != nil {
				return err
			}
			if mode == cacheFresh {
				discardIndex(dir)
			}
			idx, err := index.Open(ingest.IndexPath(dir))
			if err != nil {
				unlock()
				return err
			}
			s.cacheDir, s.idx, s.unlock = dir, idx, unlock
			return nil
		}()
		if cause == nil {
			return s, "", nil
		}
		if errors.Is(cause, errLocked) {
			return nil, "", errLocked
		}
	}
	dir, err := os.MkdirTemp("", "ssss-cache-")
	if err != nil {
		return nil, "", errors.Join(cause, err)
	}
	idx, err := index.Open(ingest.IndexPath(dir))
	if err != nil {
		os.RemoveAll(dir)
		return nil, "", errors.Join(cause, err)
	}
	s.cache, s.cacheDir, s.idx = cacheTemp, dir, idx
	note = "The local cache cannot be used, so a temporary one is rebuilt for this session"
	if cause != nil {
		note += ": " + cause.Error()
	}
	return s, note, nil
}

// activate opens the cache for an opened library, makes it the active
// session and starts its background work. The caller holds openMu.
func (a *API) activate(lib *library.Library, mode cacheMode, done string) (model.AppState, error) {
	info := lib.Info()
	s, note, err := openCache(info.ID, mode)
	if err != nil {
		lib.Close()
		return a.state(), err
	}
	s.lib, s.done = lib, done
	s.eng = ingest.New(lib, s.idx, s.cacheDir, a.notifier())
	a.mu.Lock()
	old := a.installLocked(s, info.Root, "")
	saveErr := a.saveConfigLocked()
	state := a.stateLocked()
	a.mu.Unlock()
	a.closeSession(old, false)
	if saveErr != nil {
		a.toastError(saveErr)
	}
	if note != "" {
		a.toast(model.ToastError, note)
	}
	for _, warning := range info.Warnings {
		a.toastError(fmt.Errorf("skipped %s", warning))
	}
	a.emit(model.EventState, state)
	a.bg.Go(func() { a.warmUp(s) })
	return state, nil
}

// warmUp brings a freshly opened session up to date: the index with the
// library, the library with the disk, and analysis with both.
func (a *API) warmUp(s *session) {
	if err := a.checkIndex(s); err != nil {
		// The index is a cache: one that cannot be brought up to date or
		// read is thrown away and rebuilt, first in place, then in a
		// temporary folder.
		if s.cache < cacheTemp && a.recache(s, s.cache+1, s.done) {
			return
		}
		if a.current() == s {
			a.toastError(fmt.Errorf("could not update the local index: %w", err))
		}
	}
	a.emit(model.EventSamplesChanged, model.SamplesChanged{Structural: true})
	a.runRescan(s, ingest.RescanOptions{Quiet: true})
	s.eng.AnalyzePending()
	if s.done != "" && a.current() == s {
		a.toast(model.ToastInfo, s.done)
	}
}

// checkIndex syncs the index with the library and reads from it once. The
// read matters: a database damaged past its first page opens and even takes
// writes, and then fails every search.
func (a *API) checkIndex(s *session) error {
	// Before anything is written to it. This reads the whole file, which
	// is why it happens here in the background and not when opening.
	if err := s.idx.Check(); err != nil {
		return err
	}
	if err := s.eng.SyncIndex(false); err != nil {
		return err
	}
	if _, err := s.idx.Sidebar(nil, nil); err != nil {
		return err
	}
	_, err := s.idx.Search(model.SearchRequest{Sort: model.SortName, Limit: 1})
	return err
}

// recache closes s, if it is still the session, and opens its library again
// with another kind of cache. It reports whether it took over.
func (a *API) recache(s *session, mode cacheMode, done string) bool {
	a.openMu.Lock()
	defer a.openMu.Unlock()
	if a.current() != s || a.closing.Load() {
		return false
	}
	path := s.lib.Root()
	a.closeIfSame(path, true)
	lib, err := library.Open(path, a.libraryOptions())
	if err == nil {
		_, err = a.activate(lib, mode, done)
	}
	if err != nil {
		a.setUnavailable(path, s.lib.Info().ID, err)
	}
	return true
}

// openConfigured opens the library the settings point at. A folder that
// cannot be opened leaves the app browsing this machine's cache of it,
// read-only; nothing is ever initialised here. The caller holds openMu.
func (a *API) openConfigured() model.AppState {
	a.mu.RLock()
	path, id := a.cfg.LibraryPath, a.cfg.LibraryID
	a.mu.RUnlock()
	if path == "" {
		return a.state()
	}
	a.closeIfSame(path, false)
	lib, err := library.Open(path, a.libraryOptions())
	if err == nil {
		var state model.AppState
		if state, err = a.activate(lib, cacheKept, ""); err == nil {
			return state
		}
	}
	return a.setUnavailable(path, id, err)
}

// setUnavailable switches to browsing the cached index of library id. The
// caller holds openMu.
func (a *API) setUnavailable(path, id string, cause error) model.AppState {
	s := &session{}
	if cacheDir, err := library.CacheDir(id); err == nil {
		// Only an existing cache is opened: creating an empty one here
		// would just be litter.
		if _, err := os.Stat(ingest.IndexPath(cacheDir)); err == nil {
			if idx, err := index.Open(ingest.IndexPath(cacheDir)); err == nil {
				s.idx, s.cacheDir = idx, cacheDir
			}
		}
	}
	a.mu.Lock()
	old := a.installLocked(s, path, cause.Error())
	state := a.stateLocked()
	a.mu.Unlock()
	a.closeSession(old, false)
	a.emit(model.EventState, state)
	a.emit(model.EventSamplesChanged, model.SamplesChanged{Structural: true})
	return state
}
