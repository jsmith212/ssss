package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	// ErrUnavailable: the root folder is missing or unreadable (unmounted
	// drive, sync client not running). Browse from the cache read-only.
	ErrUnavailable = errors.New("library folder is unavailable")
	// ErrNotLibrary: the folder exists but has no _library/library.json.
	// Never initialise one there unless the user explicitly asks to.
	ErrNotLibrary = errors.New("folder does not contain a library")
	// ErrExists: Create was asked to initialise a folder that already is a library.
	ErrExists = errors.New("folder already contains a library")
	// ErrPartial: _library holds files but no library.json, which is what a
	// half-synced library looks like; creating one would fork it.
	ErrPartial = errors.New("folder holds library data without library.json; it may still be syncing")
	// ErrCorrupt: library.json cannot be parsed.
	ErrCorrupt   = errors.New("library metadata is corrupt")
	ErrReadOnly  = errors.New("library is read-only")
	ErrClosed    = errors.New("library is closed")
	ErrNotFound  = errors.New("not found")
	ErrInvalid   = errors.New("invalid value")
	ErrDuplicate = errors.New("sample is already in the library")
	ErrPathTaken = errors.New("a file with that name already exists")
	ErrNotEmpty  = errors.New("pack is not empty")
)

const (
	defaultDebounce   = 500 * time.Millisecond
	defaultRetryDelay = 2 * time.Second
	// A steady stream of mutations (an import) must not postpone the write
	// forever; this bounds how much work a crash can lose.
	maxWriteDelay = 3 * time.Second
)

// Options tune a Library; the zero value is right for production.
type Options struct {
	// Debounce is how long to wait after a mutation before writing.
	Debounce time.Duration
	// RetryDelay is the wait before a failed background write is tried
	// again; it grows with every further failure.
	RetryDelay time.Duration
	// OnWriteError receives the error of a background (debounced) write,
	// once per streak of failures. The affected files stay dirty and the
	// write is retried on its own.
	OnWriteError func(error)
}

// Info describes an open library.
type Info struct {
	ID       string
	Name     string
	Root     string
	Created  time.Time
	ReadOnly bool
	// Message says why the library is read-only.
	Message string
	// Warnings lists files that were skipped while loading.
	Warnings []string
}

// state is everything loaded from _library.
type state struct {
	lib         libraryFile
	tags        tagsFile
	views       viewsFile
	packs       map[string]*pack
	collections map[string]*collection

	// byID maps a sample to the pack holding it. If two pack files list the
	// same sample the pack with the smaller ID owns it.
	byID map[string]*pack
	// pathKeys maps PathKey(pack.Dir + "/" + rec.Path) to the sample ID.
	pathKeys map[string]string
}

type fileStamp struct {
	size  int64
	mtime int64 // UnixNano
}

type dirtySet struct {
	lib, tags, views bool
	packs            map[string]struct{}
	collections      map[string]struct{}
	// heads are the dirty packs whose name was changed here. Pack headers
	// carry no revision, so this is what decides whose name survives a
	// merge with a version from another machine.
	heads map[string]struct{}
	// remove lists files (relative to _library) to delete on the next write.
	remove map[string]struct{}
}

func newDirtySet() dirtySet {
	return dirtySet{
		packs: map[string]struct{}{}, collections: map[string]struct{}{},
		heads: map[string]struct{}{}, remove: map[string]struct{}{},
	}
}

func (d *dirtySet) empty() bool {
	return !d.lib && !d.tags && !d.views && len(d.packs) == 0 && len(d.collections) == 0 && len(d.remove) == 0
}

// Library is an open library folder: its metadata in memory plus the
// machinery that writes it back. All methods are safe for concurrent use.
type Library struct {
	root string
	opts Options

	// writeMu serialises file writes and reloads; it is taken before mu.
	writeMu sync.Mutex

	mu         sync.RWMutex
	st         *state
	readOnly   bool
	message    string
	warnings   []string
	closed     bool
	dirty      dirtySet
	timer      *time.Timer
	firstDirty time.Time
	missing    map[string]struct{}
	// stamps is the version of every metadata file the state was last
	// reconciled with: read from, written to, or merged with.
	stamps map[string]fileStamp
	// unreadable holds the files whose version on disk could not be
	// parsed (half synced, damaged), by the stamp of that version. They
	// are never written over.
	unreadable map[string]fileStamp
	// failures counts background writes that failed in a row.
	failures   int
	onExternal func(Change)
}

func metaDir(root string) string { return filepath.Join(root, metaDirName) }

// IsLibrary reports whether root contains an initialised library.
func IsLibrary(root string) bool {
	st, err := os.Stat(filepath.Join(metaDir(root), libraryFileName))
	return err == nil && st.Mode().IsRegular()
}

// Create initialises a new library in root (creating the folder if needed)
// and opens it. Call it only on the user's explicit "Create library" choice:
// an existing folder is adopted as it is, files and all.
func Create(root, name string, opts Options) (*Library, error) {
	name = strings.TrimSpace(name)
	if root == "" || name == "" {
		return nil, fmt.Errorf("%w: library needs a folder and a name", ErrInvalid)
	}
	if IsLibrary(root) {
		return nil, ErrExists
	}
	meta := metaDir(root)
	if entries, err := os.ReadDir(meta); err == nil && hasLibraryData(meta, entries) {
		return nil, ErrPartial
	}
	for _, dir := range []string{
		filepath.Join(root, samplesDirName),
		filepath.Join(meta, packsDirName),
		filepath.Join(meta, collectionsDirName),
		filepath.Join(meta, trashDirName),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create library: %w", err)
		}
	}
	lf := libraryFile{V: FormatVersion, ID: "lib_" + ulid.Make().String(), Name: name, Created: time.Now().UTC().Truncate(time.Second)}
	data, err := json.Marshal(lf)
	if err != nil {
		return nil, err
	}
	if err := writeFileAtomic(filepath.Join(meta, libraryFileName), append(data, '\n')); err != nil {
		return nil, fmt.Errorf("create library: %w", err)
	}
	return Open(root, opts)
}

// hasLibraryData reports whether a _library folder without library.json
// still holds metadata files.
func hasLibraryData(meta string, entries []fs.DirEntry) bool {
	for _, e := range entries {
		if !e.IsDir() {
			return true
		}
		sub, err := os.ReadDir(filepath.Join(meta, e.Name()))
		if err != nil || len(sub) > 0 {
			return true
		}
	}
	return false
}

// Open loads the library in root. It returns ErrUnavailable when the folder
// cannot be reached and ErrNotLibrary when it has no library.json; it never
// initialises anything. A library containing files with a newer format
// version opens read-only (see Info). Conflict copies left by sync clients
// are merged before Open returns.
func Open(root string, opts Options) (*Library, error) {
	if opts.Debounce <= 0 {
		opts.Debounce = defaultDebounce
	}
	if opts.RetryDelay <= 0 {
		opts.RetryDelay = defaultRetryDelay
	}
	fi, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a folder", ErrUnavailable, root)
	}
	l := &Library{root: root, opts: opts, dirty: newDirtySet(), missing: map[string]struct{}{}}
	res, err := loadState(root)
	if err != nil {
		return nil, err
	}
	l.install(res)
	if !l.readOnly {
		for _, dir := range []string{packsDirName, collectionsDirName, trashDirName} {
			if err := os.MkdirAll(filepath.Join(metaDir(root), dir), 0o755); err != nil {
				l.readOnly, l.message = true, "the library folder is not writable: "+err.Error()
				break
			}
		}
	}
	l.foldLocked()
	l.st.reindex()
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	if _, err := l.mergeConflictsHeld(res.conflicts); err != nil {
		l.warnings = append(l.warnings, err.Error())
	}
	l.mu.Lock()
	l.scheduleLocked()
	l.mu.Unlock()
	return l, nil
}

// install puts freshly loaded state in place; l is not shared yet.
func (l *Library) install(res *loadResult) {
	l.st = res.st
	l.readOnly = res.readOnlyMsg != ""
	l.message = res.readOnlyMsg
	l.warnings = res.warnings
	l.stamps = res.stamps
	l.unreadable = res.unreadable
}

// Info returns the library's identity and status.
func (l *Library) Info() Info {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return Info{
		ID: l.st.lib.ID, Name: l.st.lib.Name, Root: l.root, Created: l.st.lib.Created,
		ReadOnly: l.readOnly, Message: l.message, Warnings: append([]string(nil), l.warnings...),
	}
}

// Root is the library folder.
func (l *Library) Root() string { return l.root }

// SamplesDir is the folder holding the audio files.
func (l *Library) SamplesDir() string { return filepath.Join(l.root, samplesDirName) }

// TrashDir is where removed files and merged conflict copies go.
func (l *Library) TrashDir() string { return filepath.Join(metaDir(l.root), trashDirName) }

// ReadOnly reports whether mutations are refused.
func (l *Library) ReadOnly() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.readOnly
}

// Close writes pending changes and refuses further mutations.
func (l *Library) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	return l.Flush()
}

func now() time.Time { return time.Now().UTC() }

// reindex rebuilds the derived lookup maps from the packs.
func (st *state) reindex() {
	st.byID = map[string]*pack{}
	st.pathKeys = map[string]string{}
	for _, id := range sortedKeys(st.packs) {
		p := st.packs[id]
		for sid, rec := range p.Samples {
			if _, dup := st.byID[sid]; dup {
				continue
			}
			st.byID[sid] = p
			st.pathKeys[sampleKey(p, rec.Path)] = sid
		}
	}
}

func sampleKey(p *pack, rel string) string {
	return PathKey(p.Dir + "/" + rel)
}
