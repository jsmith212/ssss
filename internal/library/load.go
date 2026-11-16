package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

type loadResult struct {
	st          *state
	readOnlyMsg string
	warnings    []string
	stamps      map[string]fileStamp
	// unreadable are the files that could not be read or parsed.
	unreadable map[string]fileStamp
	// conflicts are sync-client conflict copies, relative to _library.
	conflicts []string
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// isConflictCopy recognises the names Dropbox, ownCloud/Nextcloud and
// Syncthing give to the losing side of a concurrent edit.
func isConflictCopy(name string) bool {
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".json") {
		return false
	}
	return strings.Contains(lower, "conflicted copy") ||
		strings.Contains(lower, ".sync-conflict-") ||
		strings.Contains(lower, " (conflict")
}

// conflictStem recovers the canonical file stem from a conflict copy's name.
func conflictStem(name string) string {
	lower := strings.ToLower(name)
	cut := len(name)
	for _, marker := range []string{".sync-conflict-", " (conflict", "conflicted copy"} {
		i := strings.Index(lower, marker)
		if i < 0 {
			continue
		}
		if marker == "conflicted copy" {
			// "name (Ann's conflicted copy 2026-01-02).json"
			if j := strings.LastIndex(lower[:i], " ("); j >= 0 {
				i = j
			}
		}
		cut = min(cut, i)
	}
	return strings.TrimSpace(strings.TrimSuffix(name[:cut], ".json"))
}

func isTempName(name string) bool {
	return strings.HasPrefix(name, "~$") || strings.HasSuffix(name, ".part")
}

func isSafeID(id string) bool {
	if id == "" || len(id) > 80 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// scanMeta lists the metadata files (relative to _library, slash-separated)
// with their size and mtime.
func scanMeta(root string) (map[string]fileStamp, error) {
	meta := metaDir(root)
	stamps := map[string]fileStamp{}
	for _, sub := range []string{".", packsDirName, collectionsDirName} {
		entries, err := os.ReadDir(filepath.Join(meta, sub))
		if errors.Is(err, fs.ErrNotExist) && sub != "." {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || isTempName(name) || !strings.HasSuffix(strings.ToLower(name), ".json") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue // deleted while listing
			}
			stamps[path.Join(sub, name)] = fileStamp{size: info.Size(), mtime: info.ModTime().UnixNano()}
		}
	}
	return stamps, nil
}

func loadState(root string) (*loadResult, error) {
	meta := metaDir(root)
	data, err := os.ReadFile(filepath.Join(meta, libraryFileName))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, ErrNotLibrary
	case err != nil:
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	st := &state{packs: map[string]*pack{}, collections: map[string]*collection{}}
	if err := json.Unmarshal(data, &st.lib); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrCorrupt, libraryFileName, err)
	}
	if st.lib.ID == "" {
		return nil, fmt.Errorf("%w: %s has no id", ErrCorrupt, libraryFileName)
	}
	res := &loadResult{st: st, unreadable: map[string]fileStamp{}}
	maxV := st.lib.V
	warn := func(rel string, err error) {
		res.warnings = append(res.warnings, fmt.Sprintf("%s: %v", rel, err))
	}
	// unreadable is for content that may yet turn out fine (a file still
	// being delivered); it keeps the file from being written over.
	unreadable := func(rel string, err error) {
		warn(rel, err)
		res.unreadable[rel] = res.stamps[rel]
	}

	res.stamps, err = scanMeta(root)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	for _, rel := range sortedKeys(res.stamps) {
		dir, name := path.Split(rel)
		dir = strings.TrimSuffix(dir, "/")
		if isConflictCopy(name) {
			res.conflicts = append(res.conflicts, rel)
			continue
		}
		if dir == "" && name == libraryFileName {
			continue
		}
		data, err := os.ReadFile(filepath.Join(meta, filepath.FromSlash(rel)))
		if err != nil {
			unreadable(rel, err)
			continue
		}
		stem := strings.TrimSuffix(name, path.Ext(name))
		switch {
		case dir == "" && name == tagsFileName:
			if err := json.Unmarshal(data, &st.tags); err != nil {
				st.tags = tagsFile{}
				unreadable(rel, err)
			}
			maxV = max(maxV, st.tags.V)
		case dir == "" && name == viewsFileName:
			if err := json.Unmarshal(data, &st.views); err != nil {
				st.views = viewsFile{}
				unreadable(rel, err)
			}
			maxV = max(maxV, st.views.V)
		case dir == packsDirName:
			p := &pack{}
			if err := json.Unmarshal(data, p); err != nil {
				unreadable(rel, err)
				continue
			}
			if p.ID == "" {
				p.ID = stem
			}
			if _, dup := st.packs[p.ID]; dup || !isSafeID(p.ID) {
				warn(rel, fmt.Errorf("duplicate or invalid pack id %q", p.ID))
				continue
			}
			p.file = name
			st.packs[p.ID] = p
			maxV = max(maxV, p.V)
		case dir == collectionsDirName:
			c := &collection{}
			if err := json.Unmarshal(data, c); err != nil {
				unreadable(rel, err)
				continue
			}
			if c.ID == "" {
				c.ID = stem
			}
			if _, dup := st.collections[c.ID]; dup || !isSafeID(c.ID) || c.ID+".json" != name {
				warn(rel, fmt.Errorf("duplicate or misnamed collection %q", c.ID))
				continue
			}
			st.collections[c.ID] = c
			maxV = max(maxV, c.V)
		}
	}
	if st.tags.Tags == nil {
		st.tags.Tags = map[string]tagDecor{}
	}
	if maxV > FormatVersion {
		res.readOnlyMsg = newerFormatMessage(maxV)
	}
	st.reindex()
	return res, nil
}
