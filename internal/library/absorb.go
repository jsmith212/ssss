package library

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// metaFile is one metadata file as found on disk.
type metaFile struct {
	rel    string // relative to _library, slash-separated
	exists bool
	stamp  fileStamp
	data   []byte
	err    error // why it could not be read
}

// readMeta reads a metadata file together with the stamp of the version
// read. A file replaced while it was being read is read again.
func readMeta(root, rel string) metaFile {
	abs := filepath.Join(metaDir(root), filepath.FromSlash(rel))
	f := metaFile{rel: rel}
	for range 3 {
		f.stamp, f.exists, f.err = statFile(abs)
		if f.err != nil || !f.exists {
			return f
		}
		f.data, f.err = os.ReadFile(abs)
		after, exists, err := statFile(abs)
		if err == nil && exists && after == f.stamp {
			return f
		}
	}
	if f.err == nil {
		f.err = fmt.Errorf("%s keeps changing", rel)
	}
	return f
}

// absorbHeld brings the in-memory state up to date with metadata files that
// someone else (a sync client delivering another machine's work, a second
// copy of the app) changed on disk. Of rels, the files whose stamp is not
// the one this library last read or wrote are read and merged:
//
//   - an object with no unsaved local changes is replaced by the version on
//     disk, which is simply newer;
//   - an object with unsaved changes is merged with it (newest revision per
//     sample, union of tombstones and of collection members) and stays
//     dirty, so the merged result is what gets written;
//   - a file that cannot be parsed is remembered as unreadable and left
//     strictly alone until it changes again.
//
// Nothing here replaces the state as a whole, so mutations may run at any
// time before, between and after. The caller holds writeMu but not mu.
// warnings describe files that were skipped; err means the folder is no
// longer this library and nothing may be written to it.
func (l *Library) absorbHeld(rels []string) (changed bool, warnings []string, err error) {
	// library.json first: if the folder is gone or is another library,
	// nothing else in it is ours to merge.
	slices.SortFunc(rels, func(a, b string) int {
		if (a == libraryFileName) != (b == libraryFileName) {
			if a == libraryFileName {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})
	rels = slices.Compact(rels)
	l.mu.RLock()
	known := maps.Clone(l.stamps)
	l.mu.RUnlock()
	var files []metaFile
	for _, rel := range rels {
		if isConflictCopy(path.Base(rel)) {
			continue
		}
		f := readMeta(l.root, rel)
		was, had := known[rel]
		if f.err == nil && f.exists == had && (!had || f.stamp == was) {
			continue
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return false, nil, nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	packs := false
	for _, f := range files {
		c, warn, err := l.absorbLocked(f)
		if err != nil {
			return changed, warnings, err
		}
		if warn != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", f.rel, warn))
		}
		changed = changed || c
		packs = packs || c && strings.HasPrefix(f.rel, packsDirName+"/")
	}
	if packs {
		l.foldLocked()
		l.st.reindex()
		for id := range l.missing {
			if l.st.byID[id] == nil {
				delete(l.missing, id)
			}
		}
	}
	return changed, warnings, nil
}

// absorbLocked merges one changed file. warn says why the file was left
// out; err is fatal for the whole library.
func (l *Library) absorbLocked(f metaFile) (changed bool, warn, err error) {
	st := l.st
	dir, name := path.Split(f.rel)
	dir = strings.TrimSuffix(dir, "/")
	stem := strings.TrimSuffix(name, path.Ext(name))
	if dir == "" && name == libraryFileName {
		return l.absorbIdentityLocked(f)
	}

	if !f.exists {
		delete(l.stamps, f.rel)
		delete(l.unreadable, f.rel)
		switch {
		case dir == "" && name == tagsFileName && !l.dirty.tags && len(st.tags.Tags) > 0:
			st.tags = tagsFile{Tags: map[string]tagDecor{}}
			changed = true
		case dir == "" && name == viewsFileName && !l.dirty.views && len(st.views.Views) > 0:
			st.views = viewsFile{}
			changed = true
		case dir == collectionsDirName:
			// Deleted on another machine, unless there are local changes
			// to it: losing an addition is worse than keeping a collection.
			if _, dirty := l.dirty.collections[stem]; !dirty && st.collections[stem] != nil {
				delete(st.collections, stem)
				changed = true
			}
		}
		// A pack whose file vanished stays as it is in memory. No version of
		// this app deletes the file of a pack that has samples, so this is a
		// sync client mid-replace or a mistake, and dropping the records
		// would have the next rescan adopt the audio again without them.
		return changed, nil, nil
	}

	unreadable := func(err error) (bool, error, error) {
		l.stamps[f.rel] = f.stamp
		l.unreadable[f.rel] = f.stamp
		return false, err, nil
	}
	if f.err != nil {
		return unreadable(f.err)
	}
	version := 0
	switch {
	case dir == "" && name == tagsFileName:
		var src tagsFile
		if err := json.Unmarshal(f.data, &src); err != nil {
			return unreadable(err)
		}
		if src.Tags == nil {
			src.Tags = map[string]tagDecor{}
		}
		if l.dirty.tags {
			mergeTags(&st.tags, &src)
		} else {
			st.tags = src
		}
		version = src.V
	case dir == "" && name == viewsFileName:
		var src viewsFile
		if err := json.Unmarshal(f.data, &src); err != nil {
			return unreadable(err)
		}
		if l.dirty.views {
			// A fresh slice: readers hold the old one.
			merged := viewsFile{V: st.views.V, Views: slices.Clone(st.views.Views), extra: st.views.extra}
			mergeViews(&merged, &src)
			st.views = merged
		} else {
			st.views = src
		}
		version = src.V
	case dir == packsDirName:
		src := &pack{}
		if err := json.Unmarshal(f.data, src); err != nil {
			return unreadable(err)
		}
		if src.ID == "" {
			src.ID = stem
		}
		cur := st.packs[src.ID]
		if !isSafeID(src.ID) || cur != nil && cur.file != name {
			l.stamps[f.rel] = f.stamp
			return false, fmt.Errorf("duplicate or invalid pack id %q", src.ID), nil
		}
		src.file = name
		_, dirty := l.dirty.packs[src.ID]
		switch {
		case cur == nil:
			// New from another machine, or a pack dropped here that was
			// changed there in the meantime: the change wins.
			st.packs[src.ID] = src
			delete(l.dirty.remove, f.rel)
		case dirty:
			mergePack(cur, src)
			if _, renamed := l.dirty.heads[src.ID]; !renamed {
				cur.packHeader = src.packHeader
			}
			for k, v := range src.extra {
				if cur.extra == nil {
					cur.extra = extras{}
				}
				cur.extra[k] = v
			}
		default:
			st.packs[src.ID] = src
		}
		version = src.V
	case dir == collectionsDirName:
		src := &collection{}
		if err := json.Unmarshal(f.data, src); err != nil {
			return unreadable(err)
		}
		if src.ID == "" {
			src.ID = stem
		}
		if !isSafeID(src.ID) || src.ID != stem {
			l.stamps[f.rel] = f.stamp
			return false, fmt.Errorf("misnamed collection %q", src.ID), nil
		}
		cur := st.collections[src.ID]
		_, dirty := l.dirty.collections[src.ID]
		switch {
		case cur == nil:
			st.collections[src.ID] = src
			delete(l.dirty.remove, f.rel)
		case dirty:
			mergeCollection(cur, src)
		default:
			st.collections[src.ID] = src
		}
		version = src.V
	default:
		// Not a file this version knows; only remember having seen it.
		l.stamps[f.rel] = f.stamp
		return false, nil, nil
	}
	l.stamps[f.rel] = f.stamp
	delete(l.unreadable, f.rel)
	l.newerFormatLocked(version)
	return true, nil, nil
}

// absorbIdentityLocked checks that library.json still names this library.
func (l *Library) absorbIdentityLocked(f metaFile) (changed bool, warn, err error) {
	if !f.exists {
		return false, nil, fmt.Errorf("%w: %s is missing", ErrUnavailable, libraryFileName)
	}
	if f.err != nil {
		return false, nil, fmt.Errorf("%w: %v", ErrUnavailable, f.err)
	}
	var lf libraryFile
	if err := json.Unmarshal(f.data, &lf); err != nil {
		return false, nil, fmt.Errorf("%w: %s: %v", ErrCorrupt, libraryFileName, err)
	}
	if lf.ID != l.st.lib.ID {
		return false, nil, fmt.Errorf("%w: the folder now holds a different library", ErrUnavailable)
	}
	l.stamps[f.rel] = f.stamp
	l.newerFormatLocked(lf.V)
	if l.dirty.lib {
		return false, nil, nil
	}
	l.st.lib = lf
	return true, nil, nil
}

// newerFormatLocked makes the library read-only once a file written by a
// newer version of the app has been seen.
func (l *Library) newerFormatLocked(v int) {
	if v > FormatVersion && !l.readOnly {
		l.readOnly, l.message = true, newerFormatMessage(v)
	}
}

func newerFormatMessage(v int) string {
	return fmt.Sprintf("This library was written by a newer version of ssss (format %d); update to make changes.", v)
}

// foldLocked merges packs that claim the same folder into one. Two such
// packs come about when a rescan adopted the files of a pack whose own file
// had not arrived yet, or when two machines imported the same folder while
// offline. Left alone, every file would belong to two packs and a removal
// would only reach one of them. The pack that survives is chosen the same
// way on every machine, so they converge: an imported pack before one a
// rescan made up, then the older ID.
func (l *Library) foldLocked() {
	if l.readOnly {
		return
	}
	st := l.st
	byDir := map[string][]*pack{}
	for _, id := range sortedKeys(st.packs) {
		p := st.packs[id]
		if p.Dir != "" {
			key := PathKey(p.Dir)
			byDir[key] = append(byDir[key], p)
		}
	}
	for _, group := range byDir {
		if len(group) < 2 {
			continue
		}
		slices.SortStableFunc(group, func(a, b *pack) int {
			return btoi(a.Source == SourceScan) - btoi(b.Source == SourceScan)
		})
		keep := group[0]
		for _, p := range group[1:] {
			mergePack(keep, p)
			delete(st.packs, p.ID)
			delete(l.dirty.packs, p.ID)
			delete(l.dirty.heads, p.ID)
			l.dirty.remove[path.Join(packsDirName, p.file)] = struct{}{}
		}
		l.dirtyPack(keep.ID)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Available reports whether root still holds this library's library.json.
// The folder itself existing proves nothing: an unmounted drive leaves its
// empty mount point behind. Anything about to create files under root
// checks this first, so that a library is never regrown in a place it has
// left.
func (l *Library) Available() error {
	info, err := os.Stat(filepath.Join(metaDir(l.root), libraryFileName))
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is missing", ErrUnavailable, libraryFileName)
	}
	return nil
}

// Unreadable lists the metadata files (relative to _library) whose current
// version could not be parsed. While a pack file is among them the library
// does not know which samples it describes.
func (l *Library) Unreadable() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return sortedKeys(l.unreadable)
}

// OnExternalChange registers fn to be told when a write found files changed
// by someone else and merged them into the state. Reload reports the same
// through its return value instead.
func (l *Library) OnExternalChange(fn func(Change)) {
	l.mu.Lock()
	l.onExternal = fn
	l.mu.Unlock()
}
