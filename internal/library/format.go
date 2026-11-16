package library

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

// FormatVersion is the highest on-disk "v" this build writes and fully
// understands. Files with a higher value open the library read-only.
const FormatVersion = 1

const (
	metaDirName        = "_library"
	samplesDirName     = "Samples"
	packsDirName       = "packs"
	collectionsDirName = "collections"
	trashDirName       = "trash"
	libraryFileName    = "library.json"
	tagsFileName       = "tags.json"
	viewsFileName      = "views.json"
)

// Pack source values.
const (
	SourceSplice = "splice"
	SourceImport = "import"
	SourceScan   = "scan"
)

// extras holds the JSON object members this version does not know, so that
// rewriting a file written by a newer version loses nothing.
type extras map[string]json.RawMessage

var knownKeyCache sync.Map // reflect.Type -> map[string]struct{}

func knownKeys(t reflect.Type) map[string]struct{} {
	if cached, ok := knownKeyCache.Load(t); ok {
		return cached.(map[string]struct{})
	}
	keys := map[string]struct{}{}
	collectKeys(t, keys)
	knownKeyCache.Store(t, keys)
	return keys
}

func collectKeys(t reflect.Type, keys map[string]struct{}) {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			collectKeys(f.Type, keys)
			continue
		}
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		keys[name] = struct{}{}
	}
}

// decodeExtras unmarshals data into known (a pointer to a struct) and returns
// the object members that struct has no field for.
func decodeExtras(data []byte, known any) (extras, error) {
	if err := json.Unmarshal(data, known); err != nil {
		return nil, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	for k := range knownKeys(reflect.TypeOf(known).Elem()) {
		delete(all, k)
	}
	if len(all) == 0 {
		return nil, nil
	}
	return all, nil
}

// encodeExtras marshals known (a struct) and appends the preserved members.
func encodeExtras(known any, ex extras) ([]byte, error) {
	b, err := json.Marshal(known)
	if err != nil || len(ex) == 0 {
		return b, err
	}
	keys := make([]string, 0, len(ex))
	for k := range ex {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	b = b[:len(b)-1]
	for _, k := range keys {
		if len(b) > 1 {
			b = append(b, ',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		b = append(b, kb...)
		b = append(b, ':')
		b = append(b, ex[k]...)
	}
	return append(b, '}'), nil
}

// Detected is what analysis found, kept regardless of which source currently
// supplies the effective values.
type Detected struct {
	BPM     float64 `json:"bpm,omitempty"`
	BPMConf float64 `json:"bpmConf,omitempty"`
	Key     string  `json:"key,omitempty"`
	KeyConf float64 `json:"keyConf,omitempty"`
	KeyRoot bool    `json:"keyRoot,omitempty"` // Key is a root note, mode unknown
	Type    string  `json:"type,omitempty"`

	extra extras
}

type detectedJSON Detected

func (d *Detected) UnmarshalJSON(b []byte) error {
	var a detectedJSON
	ex, err := decodeExtras(b, &a)
	if err != nil {
		return err
	}
	*d = Detected(a)
	d.extra = ex
	return nil
}

func (d Detected) MarshalJSON() ([]byte, error) {
	return encodeExtras(detectedJSON(d), d.extra)
}

// SampleRecord is one sample as stored in its pack file. Path is relative to
// the pack directory, slash-separated and NFC.
type SampleRecord struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	MTime    time.Time `json:"mtime,omitzero"`
	Fmt      string    `json:"fmt,omitempty"`
	Dur      float64   `json:"dur,omitempty"`
	SR       int       `json:"sr,omitempty"`
	Ch       int       `json:"ch,omitempty"`
	Type     string    `json:"type,omitempty"`
	TypeSrc  string    `json:"typeSrc,omitempty"`
	BPM      float64   `json:"bpm,omitempty"`
	BPMSrc   string    `json:"bpmSrc,omitempty"`
	Key      string    `json:"key,omitempty"`
	KeySrc   string    `json:"keySrc,omitempty"`
	Detected *Detected `json:"detected,omitempty"`
	Analyzed bool      `json:"analyzed"`
	Mini     string    `json:"pk,omitempty"`
	Tags     []string  `json:"tags"`
	Fav      bool      `json:"fav"`
	Note     string    `json:"note"`
	Added    time.Time `json:"added,omitzero"`
	Rev      time.Time `json:"rev,omitzero"`

	extra extras
}

type sampleRecordJSON SampleRecord

func (r *SampleRecord) UnmarshalJSON(b []byte) error {
	var a sampleRecordJSON
	ex, err := decodeExtras(b, &a)
	if err != nil {
		return err
	}
	*r = SampleRecord(a)
	r.extra = ex
	return nil
}

func (r SampleRecord) MarshalJSON() ([]byte, error) {
	a := sampleRecordJSON(r)
	if a.Tags == nil {
		a.Tags = []string{}
	}
	return encodeExtras(a, r.extra)
}

type libraryFile struct {
	V       int       `json:"v"`
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`

	extra extras
}

type libraryFileJSON libraryFile

func (f *libraryFile) UnmarshalJSON(b []byte) error {
	var a libraryFileJSON
	ex, err := decodeExtras(b, &a)
	if err != nil {
		return err
	}
	*f = libraryFile(a)
	f.extra = ex
	return nil
}

func (f libraryFile) MarshalJSON() ([]byte, error) {
	return encodeExtras(libraryFileJSON(f), f.extra)
}

type tagDecor struct {
	Color string `json:"color,omitempty"`

	extra extras
}

type tagDecorJSON tagDecor

func (d *tagDecor) UnmarshalJSON(b []byte) error {
	var a tagDecorJSON
	ex, err := decodeExtras(b, &a)
	if err != nil {
		return err
	}
	*d = tagDecor(a)
	d.extra = ex
	return nil
}

func (d tagDecor) MarshalJSON() ([]byte, error) {
	return encodeExtras(tagDecorJSON(d), d.extra)
}

type tagsFile struct {
	V    int                 `json:"v"`
	Tags map[string]tagDecor `json:"tags"`

	extra extras
}

type tagsFileJSON tagsFile

func (f *tagsFile) UnmarshalJSON(b []byte) error {
	var a tagsFileJSON
	ex, err := decodeExtras(b, &a)
	if err != nil {
		return err
	}
	*f = tagsFile(a)
	f.extra = ex
	return nil
}

func (f tagsFile) MarshalJSON() ([]byte, error) {
	a := tagsFileJSON(f)
	if a.Tags == nil {
		a.Tags = map[string]tagDecor{}
	}
	return encodeExtras(a, f.extra)
}

type view struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Query string `json:"query"`

	extra extras
}

type viewJSON view

func (v *view) UnmarshalJSON(b []byte) error {
	var a viewJSON
	ex, err := decodeExtras(b, &a)
	if err != nil {
		return err
	}
	*v = view(a)
	v.extra = ex
	return nil
}

func (v view) MarshalJSON() ([]byte, error) {
	return encodeExtras(viewJSON(v), v.extra)
}

type viewsFile struct {
	V     int    `json:"v"`
	Views []view `json:"views"`

	extra extras
}

type viewsFileJSON viewsFile

func (f *viewsFile) UnmarshalJSON(b []byte) error {
	var a viewsFileJSON
	ex, err := decodeExtras(b, &a)
	if err != nil {
		return err
	}
	*f = viewsFile(a)
	f.extra = ex
	return nil
}

func (f viewsFile) MarshalJSON() ([]byte, error) {
	a := viewsFileJSON(f)
	if a.Views == nil {
		a.Views = []view{}
	}
	return encodeExtras(a, f.extra)
}

type collection struct {
	V     int       `json:"v"`
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	Items []string  `json:"items"`
	Rev   time.Time `json:"rev,omitzero"`

	extra extras
}

type collectionJSON collection

func (c *collection) UnmarshalJSON(b []byte) error {
	var a collectionJSON
	ex, err := decodeExtras(b, &a)
	if err != nil {
		return err
	}
	*c = collection(a)
	c.extra = ex
	return nil
}

func (c collection) MarshalJSON() ([]byte, error) {
	a := collectionJSON(c)
	if a.Items == nil {
		a.Items = []string{}
	}
	return encodeExtras(a, c.extra)
}

type packHeader struct {
	V      int       `json:"v"`
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	Dir    string    `json:"dir"`
	Source string    `json:"source"`
	Added  time.Time `json:"added,omitzero"`
}

type packWire struct {
	packHeader
	Samples map[string]*SampleRecord `json:"samples"`
	Removed map[string]time.Time     `json:"removed"`
}

// pack is one pack file in memory. file is the name it was loaded from,
// which is "<id>.json" unless someone renamed it by hand.
type pack struct {
	packHeader
	Samples map[string]*SampleRecord
	Removed map[string]time.Time

	extra extras
	file  string
}

func (p *pack) UnmarshalJSON(b []byte) error {
	var w packWire
	ex, err := decodeExtras(b, &w)
	if err != nil {
		return err
	}
	p.packHeader = w.packHeader
	p.Samples = w.Samples
	p.Removed = w.Removed
	p.extra = ex
	if p.Samples == nil {
		p.Samples = map[string]*SampleRecord{}
	}
	if p.Removed == nil {
		p.Removed = map[string]time.Time{}
	}
	for id, rec := range p.Samples {
		if rec == nil {
			delete(p.Samples, id)
		}
	}
	return nil
}

// MarshalJSON writes one sample per line in ID order: the output is stable
// between writes (no spurious sync churn) and stays diffable by hand.
func (p *pack) MarshalJSON() ([]byte, error) {
	head, err := encodeExtras(p.packHeader, p.extra)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(head[:len(head)-1])
	buf.WriteString(",\n\"samples\":{")
	ids := make([]string, 0, len(p.Samples))
	for id := range p.Samples {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for i, id := range ids {
		if i > 0 {
			buf.WriteByte(',')
		}
		rec, err := json.Marshal(p.Samples[id])
		if err != nil {
			return nil, err
		}
		buf.WriteString("\n\"" + id + "\":")
		buf.Write(rec)
	}
	buf.WriteString("\n},\n\"removed\":")
	removed := p.Removed
	if removed == nil {
		removed = map[string]time.Time{}
	}
	rb, err := json.Marshal(removed)
	if err != nil {
		return nil, err
	}
	buf.Write(rb)
	buf.WriteString("}")
	return buf.Bytes(), nil
}
