package ingest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"ssss/internal/library"
)

// statCache remembers, per sample, the size and time of the file version on
// this machine that is known to hold the sample's content. A rescan compares
// what it finds with that and only reads a file whose stat has changed since
// it was last verified here.
//
// The cache is per machine on purpose. The time a pack file stores for a
// sample is the time on the machine that imported it, and not every sync
// client carries file times across; comparing against it would have each
// machine re-read the whole library, or have two machines take turns
// "correcting" the stored time for ever.
type statCache struct {
	path  string
	files map[string][2]int64 // sample ID -> size, mtime in unix nanoseconds
	dirty bool
}

func statCachePath(cacheDir string) string { return filepath.Join(cacheDir, "files.json") }

// loadStatCache reads the cache; a missing or damaged file is an empty one.
func loadStatCache(cacheDir string) *statCache {
	c := &statCache{path: statCachePath(cacheDir), files: map[string][2]int64{}}
	if data, err := os.ReadFile(c.path); err == nil {
		if json.Unmarshal(data, &c.files) != nil || c.files == nil {
			c.files = map[string][2]int64{}
		}
	}
	return c
}

func (c *statCache) set(id string, st fileStat) {
	c.files[id] = [2]int64{st.size, st.mtime.UnixNano()}
	c.dirty = true
}

// differs reports whether the file found for a sample needs to be read to
// know that it still is that sample. A file never verified on this machine
// is taken on trust if its size is the recorded one: its time proves
// nothing (see above), and reading every file of a freshly synced library
// would also make a cloud client download each one.
func (c *statCache) differs(sf library.SampleFile, st fileStat) bool {
	if v, ok := c.files[sf.ID]; ok {
		return v != [2]int64{st.size, st.mtime.UnixNano()}
	}
	if sf.Size != 0 && sf.Size != st.size {
		return true
	}
	c.set(sf.ID, st)
	return false
}

// save writes the cache if it changed, keeping only the samples in keep. It
// is an optimisation and a failure to write it costs nothing but a re-read.
func (c *statCache) save(keep map[string]struct{}) {
	if c == nil || !c.dirty {
		return
	}
	for id := range c.files {
		if _, ok := keep[id]; !ok {
			delete(c.files, id)
		}
	}
	data, err := json.Marshal(c.files)
	if err != nil {
		return
	}
	tmp := c.path + ".part"
	if os.WriteFile(tmp, data, 0o644) == nil && os.Rename(tmp, c.path) == nil {
		c.dirty = false
	}
}

// staleTempAge is how old a leftover import temp file must be before it is
// deleted: older than any copy still in progress, here or in a second copy
// of the app, whose temp file is written to (and so stays fresh) throughout.
const staleTempAge = time.Hour

// sweepTemps deletes partial copies that an import left in Samples when the
// app was killed or the machine lost power. Nothing else ever removes them:
// their names are ones scans and sync clients skip. It runs once per
// session, from the first rescan.
func (e *Engine) sweepTemps() {
	if e.swept {
		return
	}
	e.swept = true
	entries, err := os.ReadDir(e.lib.SamplesDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !isImportTemp(entry.Name()) || !entry.Type().IsRegular() {
			continue
		}
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > staleTempAge {
			os.Remove(filepath.Join(e.lib.SamplesDir(), entry.Name()))
		}
	}
}
