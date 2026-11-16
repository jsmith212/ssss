package library

import (
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"

	"ssss/internal/model"
)

func packInfo(p *pack, count int) model.PackInfo {
	return model.PackInfo{ID: p.ID, Name: p.Name, Dir: p.Dir, Source: p.Source, Count: count}
}

func (l *Library) toSample(p *pack, id string, rec *SampleRecord) model.Sample {
	ext := path.Ext(rec.Path)
	added := rec.Added
	if added.IsZero() {
		added = p.Added
	}
	_, missing := l.missing[id]
	tags := slices.Clone(rec.Tags)
	if tags == nil {
		tags = []string{}
	}
	s := model.Sample{
		ID: id, PackID: p.ID, PackName: p.Name,
		Name:    strings.TrimSuffix(path.Base(rec.Path), ext),
		Ext:     strings.ToLower(strings.TrimPrefix(ext, ".")),
		RelPath: rec.Path,
		Size:    rec.Size, Dur: rec.Dur, SR: rec.SR, Ch: rec.Ch,
		Type: rec.Type, BPM: rec.BPM, BPMSrc: rec.BPMSrc, Key: rec.Key, KeySrc: rec.KeySrc,
		Tags: tags, Fav: rec.Fav, Missing: missing, Analyzed: rec.Analyzed, Mini: rec.Mini,
	}
	// Only the detected key can be a bare root; a key from the name, from
	// Splice or from the user states its mode.
	if d := rec.Detected; d != nil && d.KeyRoot && rec.KeySrc == model.SrcDetected && rec.Key == d.Key {
		s.KeyRoot = true
	}
	if !added.IsZero() {
		s.Added = added.UnixMilli()
	}
	if s.Ext == "" {
		s.Ext = rec.Fmt
	}
	return s
}

// Has reports whether the sample is in the library.
func (l *Library) Has(id string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.st.byID[id] != nil
}

// Tombstoned reports whether the sample was deliberately removed, so a scan
// that meets its content again should not re-add it.
func (l *Library) Tombstoned(id string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.st.byID[id] != nil {
		return false
	}
	for _, p := range l.st.packs {
		if _, ok := p.Removed[id]; ok {
			return true
		}
	}
	return false
}

// Sample returns the list row for id.
func (l *Library) Sample(id string) (model.Sample, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	p := l.st.byID[id]
	if p == nil {
		return model.Sample{}, false
	}
	return l.toSample(p, id, p.Samples[id]), true
}

// Record returns a copy of the stored record and the ID of its pack.
func (l *Library) Record(id string) (packID string, rec SampleRecord, ok bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	p := l.st.byID[id]
	if p == nil {
		return "", SampleRecord{}, false
	}
	rec = *p.Samples[id]
	rec.Tags = slices.Clone(rec.Tags)
	return p.ID, rec, true
}

// AbsPath returns the sample's file path.
func (l *Library) AbsPath(id string) (string, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	p := l.st.byID[id]
	if p == nil {
		return "", false
	}
	return l.absPath(p, p.Samples[id].Path), true
}

// Details returns the info-popover data for the IDs that exist, in the
// order asked. SuggestedTags is left empty for the caller to fill.
func (l *Library) Details(ids []string) []model.SampleDetail {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]model.SampleDetail, 0, len(ids))
	collIDs := sortedKeys(l.st.collections)
	for _, id := range ids {
		p := l.st.byID[id]
		if p == nil {
			continue
		}
		rec := p.Samples[id]
		d := model.SampleDetail{
			Sample:        l.toSample(p, id, rec),
			AbsPath:       l.absPath(p, rec.Path),
			Collections:   []string{},
			Note:          rec.Note,
			SuggestedTags: []string{},
		}
		for _, cid := range collIDs {
			if slices.Contains(l.st.collections[cid].Items, id) {
				d.Collections = append(d.Collections, cid)
			}
		}
		out = append(out, d)
	}
	return out
}

// SamplesByID returns the rows for the IDs that exist, in the order asked.
func (l *Library) SamplesByID(ids []string) []model.Sample {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]model.Sample, 0, len(ids))
	for _, id := range ids {
		if p := l.st.byID[id]; p != nil {
			out = append(out, l.toSample(p, id, p.Samples[id]))
		}
	}
	return out
}

// Samples returns every sample, ordered by pack then ID.
func (l *Library) Samples() []model.Sample {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]model.Sample, 0, len(l.st.byID))
	for _, pid := range sortedKeys(l.st.packs) {
		p := l.st.packs[pid]
		for _, id := range sortedKeys(p.Samples) {
			if l.st.byID[id] == p {
				out = append(out, l.toSample(p, id, p.Samples[id]))
			}
		}
	}
	return out
}

// Count is the number of samples.
func (l *Library) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.st.byID)
}

// Packs lists the packs sorted by name.
func (l *Library) Packs() []model.PackInfo {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]model.PackInfo, 0, len(l.st.packs))
	for _, p := range l.st.packs {
		n := 0
		for id := range p.Samples {
			if l.st.byID[id] == p {
				n++
			}
		}
		out = append(out, packInfo(p, n))
	}
	slices.SortFunc(out, func(a, b model.PackInfo) int {
		return strings.Compare(strings.ToLower(a.Name)+a.ID, strings.ToLower(b.Name)+b.ID)
	})
	return out
}

// PackByDir finds the pack stored in dir (compared by PathKey).
func (l *Library) PackByDir(dir string) (model.PackInfo, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	key := PathKey(strings.Trim(filepath.ToSlash(dir), "/"))
	for _, id := range sortedKeys(l.st.packs) {
		if p := l.st.packs[id]; PathKey(p.Dir) == key {
			return packInfo(p, len(p.Samples)), true
		}
	}
	return model.PackInfo{}, false
}

// Collections lists the collections sorted by name. Count ignores members
// that are no longer in the library.
func (l *Library) Collections() []model.CollectionInfo {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]model.CollectionInfo, 0, len(l.st.collections))
	for _, c := range l.st.collections {
		n := 0
		for _, id := range c.Items {
			if l.st.byID[id] != nil {
				n++
			}
		}
		out = append(out, model.CollectionInfo{ID: c.ID, Name: c.Name, Count: n})
	}
	slices.SortFunc(out, func(a, b model.CollectionInfo) int {
		return strings.Compare(strings.ToLower(a.Name)+a.ID, strings.ToLower(b.Name)+b.ID)
	})
	return out
}

// CollectionItems returns the collection's sample IDs in their saved order,
// including IDs of samples that have since been removed.
func (l *Library) CollectionItems(id string) ([]string, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	c := l.st.collections[id]
	if c == nil {
		return nil, false
	}
	return slices.Clone(c.Items), true
}

// TagColors returns the colour of every decorated tag.
func (l *Library) TagColors() map[string]string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := map[string]string{}
	for tag, d := range l.st.tags.Tags {
		if d.Color != "" {
			out[tag] = d.Color
		}
	}
	return out
}

// Views lists the saved searches in the order they were saved.
func (l *Library) Views() []model.SavedView {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]model.SavedView, 0, len(l.st.views.Views))
	for _, v := range l.st.views.Views {
		out = append(out, model.SavedView{ID: v.ID, Name: v.Name, Query: v.Query})
	}
	return out
}

// PathIn returns where a file with the in-pack path rel belongs on disk:
// the pack's folder as the pack spells it, and rel in NFC. A file found
// under another spelling (decomposed characters from a Mac, a folder in
// different case) has to be moved there before a record can point at it,
// or the record would only resolve on file systems that fold the two.
func (l *Library) PathIn(packID, rel string) (string, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	p := l.st.packs[packID]
	if p == nil {
		return "", false
	}
	return l.absPath(p, norm.NFC.String(rel)), true
}

// FreeRel returns rel, or rel with a " (n)" suffix, such that neither a
// sample of the pack nor a file on disk has that path.
func (l *Library) FreeRel(packID, rel string) (string, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	p := l.st.packs[packID]
	if p == nil {
		return "", false
	}
	return l.freePath(p, sanitizeRelPath(rel, relLimit(p)), ""), true
}

// SampleFile is a sample's file as the library has it on record.
type SampleFile struct {
	ID    string
	Abs   string
	Size  int64
	MTime time.Time
}

// SampleFileForPath finds the sample recorded at a path inside the Samples
// folder. Paths are matched by PathKey, so the result's Abs can differ from
// abs in case or Unicode form; whether both name one file is for the file
// system to say.
func (l *Library) SampleFileForPath(abs string) (SampleFile, bool) {
	rel, err := filepath.Rel(l.SamplesDir(), abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return SampleFile{}, false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	id, ok := l.st.pathKeys[PathKey(filepath.ToSlash(rel))]
	if !ok {
		return SampleFile{}, false
	}
	p := l.st.byID[id]
	rec := p.Samples[id]
	return SampleFile{ID: id, Abs: l.absPath(p, rec.Path), Size: rec.Size, MTime: rec.MTime}, true
}

// SampleIDForPath maps a file path inside the Samples folder back to its
// sample, e.g. to recognise our own drag-out dropped on our own window.
func (l *Library) SampleIDForPath(abs string) (string, bool) {
	rel, err := filepath.Rel(l.SamplesDir(), abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	id, ok := l.st.pathKeys[PathKey(filepath.ToSlash(rel))]
	return id, ok
}
