package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"ssss/internal/model"
)

func newLib(t *testing.T) *Library {
	t.Helper()
	l, err := Create(filepath.Join(t.TempDir(), "lib"), "Main", Options{Debounce: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func reopen(t *testing.T, l *Library) *Library {
	t.Helper()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l2, err := Open(l.Root(), Options{Debounce: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l2.Close() })
	return l2
}

func sampleID(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:16])
}

// must unwraps a (value, error) pair; a panic fails the test with a trace.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newPack(t *testing.T, l *Library, name string) string {
	t.Helper()
	info, _, err := l.CreatePack(name, "", SourceImport)
	if err != nil {
		t.Fatal(err)
	}
	return info.ID
}

// addSample ingests a file whose content (and so ID) derives from rel.
func addSample(t *testing.T, l *Library, packID, rel string) string {
	t.Helper()
	return addContent(t, l, packID, rel, "content of "+rel)
}

func addContent(t *testing.T, l *Library, packID, rel, content string) string {
	t.Helper()
	id := sampleID(content)
	_, err := l.AddSample(packID, id, SampleRecord{Path: rel, Size: int64(len(content)), Fmt: "wav"}, func(abs string) error {
		return os.WriteFile(abs, []byte(content), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func tagsOf(t *testing.T, l *Library, id string) []string {
	t.Helper()
	s, ok := l.Sample(id)
	if !ok {
		t.Fatalf("sample %s missing", id)
	}
	return s.Tags
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// tempFiles lists leftover in-progress files anywhere under root.
func tempFiles(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && isTempName(d.Name()) {
			found = append(found, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(must(os.ReadFile(path)), &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

func TestCreateAndOpenRules(t *testing.T) {
	dir := t.TempDir()

	if _, err := Open(filepath.Join(dir, "nope"), Options{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("missing root: got %v, want ErrUnavailable", err)
	}
	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o644)
	if _, err := Open(file, Options{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("root is a file: got %v, want ErrUnavailable", err)
	}

	// An existing non-empty folder is never initialised by Open.
	existing := filepath.Join(dir, "existing")
	os.MkdirAll(existing, 0o755)
	os.WriteFile(filepath.Join(existing, "song.wav"), []byte("x"), 0o644)
	if _, err := Open(existing, Options{}); !errors.Is(err, ErrNotLibrary) {
		t.Errorf("no library.json: got %v, want ErrNotLibrary", err)
	}
	if exists(filepath.Join(existing, metaDirName)) {
		t.Error("Open created _library")
	}

	// The explicit Create adopts it and leaves the files alone.
	l, err := Create(existing, " Main ", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	info := l.Info()
	if info.Name != "Main" || !strings.HasPrefix(info.ID, "lib_") || info.ReadOnly || info.Created.IsZero() {
		t.Errorf("info = %+v", info)
	}
	if !exists(filepath.Join(existing, "song.wav")) || !IsLibrary(existing) {
		t.Error("existing file lost or library not initialised")
	}
	for _, sub := range []string{"Samples", "_library/packs", "_library/collections", "_library/trash"} {
		if !exists(filepath.Join(existing, sub)) {
			t.Errorf("%s not created", sub)
		}
	}
	lib := readJSON(t, filepath.Join(existing, "_library/library.json"))
	if lib["v"] != float64(1) || lib["id"] != info.ID || lib["name"] != "Main" {
		t.Errorf("library.json = %v", lib)
	}

	if _, err := Create(existing, "Again", Options{}); !errors.Is(err, ErrExists) {
		t.Errorf("second Create: got %v, want ErrExists", err)
	}
	if _, err := Create(filepath.Join(dir, "x"), "  ", Options{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty name: got %v, want ErrInvalid", err)
	}

	// Pack files without library.json look like a half-synced library.
	partial := filepath.Join(dir, "partial")
	os.MkdirAll(filepath.Join(partial, "_library/packs"), 0o755)
	os.WriteFile(filepath.Join(partial, "_library/packs/pk_1.json"), []byte("{}"), 0o644)
	if _, err := Create(partial, "Main", Options{}); !errors.Is(err, ErrPartial) {
		t.Errorf("partial: got %v, want ErrPartial", err)
	}
	if _, err := Open(partial, Options{}); !errors.Is(err, ErrNotLibrary) {
		t.Errorf("partial open: got %v, want ErrNotLibrary", err)
	}

	corrupt := filepath.Join(dir, "corrupt")
	os.MkdirAll(filepath.Join(corrupt, "_library"), 0o755)
	os.WriteFile(filepath.Join(corrupt, "_library/library.json"), []byte("{not json"), 0o644)
	if _, err := Open(corrupt, Options{}); !errors.Is(err, ErrCorrupt) {
		t.Errorf("corrupt: got %v, want ErrCorrupt", err)
	}
}

func TestOpenSameIdentity(t *testing.T) {
	l := newLib(t)
	id := l.Info().ID
	l2 := reopen(t, l)
	if l2.Info().ID != id {
		t.Errorf("id changed: %s -> %s", id, l2.Info().ID)
	}
	if _, err := l.AddTags(nil, []string{"x"}); !errors.Is(err, ErrClosed) {
		t.Errorf("mutation after Close: %v", err)
	}
}

const handWrittenPack = `{"v":1,"id":"pk_HAND","name":"Hand","dir":"Hand","source":"import","added":"2026-11-15T10:01:00Z",
 "futurePack":{"a":[1,2,3]},
 "samples":{"0123456789abcdef0123456789abcdef":{
   "path":"Drums/Kick_01.wav","size":182044,"mtime":"2026-11-15T10:01:00Z","fmt":"wav","dur":0.412,"sr":44100,"ch":2,
   "type":"oneshot","typeSrc":"detected","bpm":128,"bpmSrc":"filename","key":"Am","keySrc":"splice",
   "detected":{"bpm":127.9,"bpmConf":0.8,"key":"Am","keyConf":0.7,"type":"loop","from":"future"},
   "analyzed":true,"pk":"AAEC","tags":["drums/kick"],"fav":false,"note":"n","added":"2026-11-15T10:01:00Z",
   "rev":"2026-11-15T10:05:00.123456789Z","futureSample":"keep me","rating":5}},
 "removed":{"ffffffffffffffffffffffffffffffff":"2026-11-14T09:00:00Z"}}`

// writeHandLibrary builds a library folder as another (newer) client might
// have written it.
func writeHandLibrary(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"_library/library.json":              `{"v":1,"id":"lib_HAND","name":"Main","created":"2026-11-15T10:00:00Z","futureLib":true}`,
		"_library/tags.json":                 `{"v":1,"tags":{"drums/kick":{"color":"#e66","icon":"drum"},"bass":{}},"futureTags":[1]}`,
		"_library/views.json":                `{"v":1,"views":[{"id":"vw_1","name":"Loops","query":"type:loop","pinned":true}],"futureViews":"x"}`,
		"_library/packs/pk_HAND.json":        handWrittenPack,
		"_library/collections/col_HAND.json": `{"v":1,"id":"col_HAND","name":"Set","parent":null,"items":["0123456789abcdef0123456789abcdef"],"rev":"2026-11-15T10:06:00Z"}`,
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const handID = "0123456789abcdef0123456789abcdef"

func TestLoadAndConvert(t *testing.T) {
	root := writeHandLibrary(t)
	l := must(Open(root, Options{}))
	defer l.Close()
	if w := l.Info().Warnings; len(w) != 0 {
		t.Fatalf("warnings: %v", w)
	}
	s, ok := l.Sample(handID)
	if !ok {
		t.Fatal("sample not found")
	}
	want := model.Sample{
		ID: handID, PackID: "pk_HAND", PackName: "Hand", Name: "Kick_01", Ext: "wav", RelPath: "Drums/Kick_01.wav",
		Size: 182044, Dur: 0.412, SR: 44100, Ch: 2, Type: "oneshot", BPM: 128, BPMSrc: "filename", Key: "Am", KeySrc: "splice",
		Tags: []string{"drums/kick"}, Added: time.Date(2026, 11, 15, 10, 1, 0, 0, time.UTC).UnixMilli(), Analyzed: true, Mini: "AAEC",
	}
	if got, _ := json.Marshal(s); string(got) != string(must(json.Marshal(want))) {
		t.Errorf("sample = %s", got)
	}
	d := l.Details([]string{"unknown", handID})
	if len(d) != 1 || d[0].Note != "n" || !slices.Equal(d[0].Collections, []string{"col_HAND"}) ||
		d[0].AbsPath != filepath.Join(root, "Samples", "Hand", "Drums", "Kick_01.wav") || d[0].SuggestedTags == nil {
		t.Errorf("details = %+v", d)
	}
	if id, ok := l.SampleIDForPath(filepath.Join(root, "Samples", "hand", "drums", "KICK_01.WAV")); !ok || id != handID {
		t.Errorf("SampleIDForPath = %q, %v", id, ok)
	}
	if _, ok := l.SampleIDForPath(filepath.Join(root, "elsewhere.wav")); ok {
		t.Error("path outside Samples resolved")
	}
	if !l.Tombstoned(strings.Repeat("f", 32)) || l.Tombstoned(handID) {
		t.Error("tombstone lookup wrong")
	}
	if got := l.TagColors(); len(got) != 1 || got["drums/kick"] != "#e66" {
		t.Errorf("tag colours = %v", got)
	}
	if got := l.Views(); len(got) != 1 || got[0] != (model.SavedView{ID: "vw_1", Name: "Loops", Query: "type:loop"}) {
		t.Errorf("views = %v", got)
	}
	if got := l.Packs(); len(got) != 1 || got[0] != (model.PackInfo{ID: "pk_HAND", Name: "Hand", Dir: "Hand", Source: "import", Count: 1}) {
		t.Errorf("packs = %v", got)
	}
	if got := l.Collections(); len(got) != 1 || got[0] != (model.CollectionInfo{ID: "col_HAND", Name: "Set", Count: 1}) {
		t.Errorf("collections = %v", got)
	}
	if l.Count() != 1 || len(l.Samples()) != 1 || len(l.SamplesByID(nil)) != 0 {
		t.Error("counts wrong")
	}
}

func TestUnknownFieldsSurviveRewrite(t *testing.T) {
	root := writeHandLibrary(t)
	l := must(Open(root, Options{}))

	// Touch every file kind so each is rewritten.
	must(l.AddTags([]string{handID}, []string{"New Tag"}))
	must(l.SetTagColor("bass", "#00f"))
	must(l.RenameCollection("col_HAND", "Renamed"))
	if _, _, err := l.SaveView("Another", "is:fav"); err != nil {
		t.Fatal(err)
	}
	must(l.ApplyAnalysis(handID, Analysis{Detected: &Detected{BPM: 100}}))
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if left := tempFiles(t, root); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}

	meta := filepath.Join(root, "_library")
	pk := readJSON(t, filepath.Join(meta, "packs/pk_HAND.json"))
	if pk["futurePack"] == nil || pk["name"] != "Hand" {
		t.Errorf("pack extras lost: %v", pk)
	}
	rec := pk["samples"].(map[string]any)[handID].(map[string]any)
	if rec["futureSample"] != "keep me" || rec["rating"] != float64(5) {
		t.Errorf("sample extras lost: %v", rec)
	}
	if det := rec["detected"].(map[string]any); det["from"] != "future" || det["bpm"] != float64(100) {
		t.Errorf("detected = %v", det)
	}
	if got := rec["tags"].([]any); len(got) != 2 || got[1] != "new tag" {
		t.Errorf("tags = %v", got)
	}
	if rec["rev"] == "2026-11-15T10:05:00.123456789Z" {
		t.Error("rev not bumped")
	}
	if rm := pk["removed"].(map[string]any); rm[strings.Repeat("f", 32)] != "2026-11-14T09:00:00Z" {
		t.Errorf("tombstones = %v", rm)
	}
	tags := readJSON(t, filepath.Join(meta, "tags.json"))
	tm := tags["tags"].(map[string]any)
	if tags["futureTags"] == nil || tm["drums/kick"].(map[string]any)["icon"] != "drum" || tm["bass"].(map[string]any)["color"] != "#00f" {
		t.Errorf("tags.json = %v", tags)
	}
	views := readJSON(t, filepath.Join(meta, "views.json"))
	vs := views["views"].([]any)
	if views["futureViews"] != "x" || len(vs) != 2 || vs[0].(map[string]any)["pinned"] != true {
		t.Errorf("views.json = %v", views)
	}
	col := readJSON(t, filepath.Join(meta, "collections/col_HAND.json"))
	if _, has := col["parent"]; !has || col["name"] != "Renamed" {
		t.Errorf("collection = %v", col)
	}
	if lib := readJSON(t, filepath.Join(meta, "library.json")); lib["futureLib"] != true {
		t.Errorf("library.json = %v", lib)
	}

	// And the rewritten files load back to the same state.
	l2 := must(Open(root, Options{}))
	defer l2.Close()
	if got := tagsOf(t, l2, handID); !slices.Equal(got, []string{"drums/kick", "new tag"}) {
		t.Errorf("tags after reopen = %v", got)
	}
}

func TestNewerVersionOpensReadOnly(t *testing.T) {
	root := writeHandLibrary(t)
	packFile := filepath.Join(root, "_library/packs/pk_HAND.json")
	newer := strings.Replace(handWrittenPack, `{"v":1,`, `{"v":2,`, 1)
	os.WriteFile(packFile, []byte(newer), 0o644)
	// A conflict copy must not be merged or moved either.
	copyFile := filepath.Join(root, "_library/packs/pk_HAND (conflicted copy).json")
	os.WriteFile(copyFile, []byte(handWrittenPack), 0o644)

	l := must(Open(root, Options{}))
	info := l.Info()
	if !info.ReadOnly || !l.ReadOnly() || info.Message == "" {
		t.Fatalf("info = %+v, want read-only with a message", info)
	}
	if _, ok := l.Sample(handID); !ok {
		t.Error("samples not readable in read-only mode")
	}
	checks := map[string]error{}
	_, checks["AddTags"] = l.AddTags([]string{handID}, []string{"x"})
	_, checks["RemoveSamples"] = l.RemoveSamples([]string{handID})
	_, _, checks["CreatePack"] = l.CreatePack("p", "", SourceImport)
	_, _, checks["CreateCollection"] = l.CreateCollection("c")
	_, checks["ApplyAnalysis"] = l.ApplyAnalysis(handID, Analysis{BPM: 1, BPMSrc: model.SrcDetected})
	for name, err := range checks {
		if !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s: got %v, want ErrReadOnly", name, err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if got := must(os.ReadFile(packFile)); string(got) != newer {
		t.Error("read-only library rewrote a pack file")
	}
	if !exists(copyFile) {
		t.Error("read-only library moved a conflict copy")
	}
}

func TestCorruptPackIsSkippedNotOverwritten(t *testing.T) {
	root := writeHandLibrary(t)
	bad := filepath.Join(root, "_library/packs/pk_BAD.json")
	os.WriteFile(bad, []byte(`{"v":1,"id":"pk_BAD","samples":{`), 0o644)
	l := must(Open(root, Options{}))
	if w := l.Info().Warnings; len(w) != 1 || !strings.Contains(w[0], "pk_BAD.json") {
		t.Errorf("warnings = %v", w)
	}
	if l.ReadOnly() || len(l.Packs()) != 1 {
		t.Error("good pack should still load writable")
	}
	must(l.SetFavorite([]string{handID}, true))
	l.Close()
	if got := must(os.ReadFile(bad)); string(got) != `{"v":1,"id":"pk_BAD","samples":{` {
		t.Error("corrupt pack file was modified")
	}
}

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	for _, content := range []string{"first", "second, longer", "3"} {
		if err := writeFileAtomic(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
		if got := must(os.ReadFile(path)); string(got) != content {
			t.Errorf("content = %q, want %q", got, content)
		}
	}
	// A stale temp file from a crash is replaced, not tripped over.
	os.WriteFile(filepath.Join(dir, tempName("x.json")), []byte("stale"), 0o644)
	if err := writeFileAtomic(path, []byte("4")); err != nil {
		t.Fatal(err)
	}
	entries := must(os.ReadDir(dir))
	if len(entries) != 1 || entries[0].Name() != "x.json" {
		t.Errorf("dir = %v, want only x.json", entries)
	}
	if tempName("pk.json") != "~$pk.json.part" {
		t.Errorf("temp name = %q", tempName("pk.json"))
	}
	// Failure leaves neither the target nor a temp file behind.
	if err := writeFileAtomic(filepath.Join(dir, "missing", "y.json"), []byte("x")); err == nil {
		t.Error("write into a missing folder succeeded")
	}
	if left := tempFiles(t, dir); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestDebouncedWriteAndFlush(t *testing.T) {
	root := filepath.Join(t.TempDir(), "lib")
	l := must(Create(root, "Main", Options{Debounce: time.Hour}))
	defer l.Close()
	pk := newPack(t, l, "Pack")
	packFile := filepath.Join(root, "_library/packs", pk+".json")
	if exists(packFile) {
		t.Fatal("pack written before the debounce elapsed")
	}
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	if !exists(packFile) {
		t.Fatal("Flush did not write the pack")
	}

	// With a short debounce the write happens on its own.
	fast := must(Create(filepath.Join(t.TempDir(), "lib"), "Main", Options{Debounce: 10 * time.Millisecond}))
	defer fast.Close()
	pk2 := newPack(t, fast, "Pack")
	fastFile := filepath.Join(fast.Root(), "_library/packs", pk2+".json")
	deadline := time.Now().Add(5 * time.Second)
	for !exists(fastFile) {
		if time.Now().After(deadline) {
			t.Fatal("debounced write never happened")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if left := tempFiles(t, fast.Root()); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

func TestBackgroundWriteErrorIsReportedAndRetried(t *testing.T) {
	root := filepath.Join(t.TempDir(), "lib")
	errs := make(chan error, 8)
	l := must(Create(root, "Main", Options{Debounce: 5 * time.Millisecond, OnWriteError: func(err error) { errs <- err }}))
	defer l.Close()
	// A folder squatting on the file name makes the rename fail.
	if _, _, err := l.SaveView("v", "q"); err != nil {
		t.Fatal(err)
	}
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	viewsFile := filepath.Join(root, "_library/views.json")
	os.Remove(viewsFile)
	os.MkdirAll(filepath.Join(viewsFile, "blocker"), 0o755)
	if _, _, err := l.SaveView("w", "q"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errs:
		if err == nil {
			t.Error("nil error reported")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("background write error not reported")
	}
	os.RemoveAll(viewsFile)
	if err := l.Flush(); err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if got := readJSON(t, viewsFile)["views"].([]any); len(got) != 2 {
		t.Errorf("views after retry = %v", got)
	}
}

func TestAddSamplePathHandling(t *testing.T) {
	l := newLib(t)
	pk := newPack(t, l, "My: Pack?")
	if got := l.Packs()[0]; got.Dir != "My_ Pack_" || got.Name != "My: Pack?" {
		t.Errorf("pack = %+v", got)
	}

	a := addContent(t, l, pk, "Drums/Kick.wav", "one")
	// Same key (case and normalisation differ), different content.
	b := addContent(t, l, pk, "drums/KICK.wav", "two")
	c := addContent(t, l, pk, "Drums/Kick.wav", "three")
	nfd := addContent(t, l, pk, norm.NFD.String("Café/Señor.wav"), "four")
	bad := addContent(t, l, pk, `../CON/a<b>.wav`, "five")

	want := map[string]string{a: "Drums/Kick.wav", b: "drums/KICK (2).wav", c: "Drums/Kick (3).wav", nfd: "Café/Señor.wav", bad: "CON_/a_b_.wav"}
	for id, rel := range want {
		s, ok := l.Sample(id)
		if !ok || s.RelPath != rel {
			t.Errorf("sample %s path = %q, want %q", id, s.RelPath, rel)
		}
		abs, _ := l.AbsPath(id)
		if !exists(abs) {
			t.Errorf("file not placed at %s", abs)
		}
	}

	// Same content again is a duplicate wherever it is headed.
	other := newPack(t, l, "Other")
	_, err := l.AddSample(other, a, SampleRecord{Path: "x.wav"}, func(string) error { t.Error("place called for duplicate"); return nil })
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate: got %v", err)
	}
	if _, err := l.AddSample(pk, "nothex", SampleRecord{Path: "x.wav"}, func(string) error { return nil }); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad id: got %v", err)
	}
	if _, err := l.AddSample("pk_missing", sampleID("z"), SampleRecord{Path: "x.wav"}, func(string) error { return nil }); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing pack: got %v", err)
	}

	// A file on disk that no record knows about is never overwritten.
	stray := filepath.Join(l.SamplesDir(), "Other", "stray.wav")
	os.MkdirAll(filepath.Dir(stray), 0o755)
	os.WriteFile(stray, []byte("stray"), 0o644)
	d := addContent(t, l, other, "stray.wav", "six")
	if s, _ := l.Sample(d); s.RelPath != "stray (2).wav" {
		t.Errorf("path = %q, want stray (2).wav", s.RelPath)
	}
	if got, _ := os.ReadFile(stray); string(got) != "stray" {
		t.Error("stray file overwritten")
	}

	// A failing place leaves no record and no empty folders.
	failID := sampleID("fail")
	_, err = l.AddSample(other, failID, SampleRecord{Path: "deep/er/f.wav"}, func(string) error { return errors.New("disk full") })
	if err == nil || l.Has(failID) || exists(filepath.Join(l.SamplesDir(), "Other", "deep")) {
		t.Errorf("failed add left traces (err=%v)", err)
	}

	// Long paths stay within the Windows budget including the pack dir.
	long := addContent(t, l, other, strings.Repeat("d", 150)+"/"+strings.Repeat("k", 150)+".wav", "seven")
	s, _ := l.Sample(long)
	if n := len([]rune("Other/" + s.RelPath)); n > maxRelPathChars || !strings.HasSuffix(s.RelPath, ".wav") {
		t.Errorf("long path %d chars: %q", n, s.RelPath)
	}

	// Two packs wanting the same folder get distinct ones.
	info, _, _ := l.CreatePack("Other", "", SourceImport)
	if info.Dir != "Other (2)" {
		t.Errorf("second pack dir = %q", info.Dir)
	}
	if got, ok := l.PackByDir("other"); !ok || got.ID != other {
		t.Errorf("PackByDir = %+v, %v", got, ok)
	}
}

func TestAdoptAndHeal(t *testing.T) {
	l := newLib(t)
	pk := newPack(t, l, "Pack")
	id := sampleID("adopt")
	nfdRel := norm.NFD.String("Café/a.wav")
	ch := must(l.AdoptSample(pk, id, SampleRecord{Path: nfdRel, Tags: []string{"Drums", "drums"}}))
	if !slices.Equal(ch.IDs, []string{id}) || !ch.Structural {
		t.Errorf("change = %+v", ch)
	}
	s, _ := l.Sample(id)
	if s.RelPath != "Café/a.wav" || !slices.Equal(s.Tags, []string{"drums"}) || s.Added == 0 {
		t.Errorf("sample = %+v", s)
	}
	if _, err := l.AdoptSample(pk, sampleID("other"), SampleRecord{Path: "CAFÉ/A.wav"}); !errors.Is(err, ErrPathTaken) {
		t.Errorf("colliding adopt: got %v", err)
	}

	l.SetMissing([]string{id, "unknown"}, true)
	if s, _ := l.Sample(id); !s.Missing {
		t.Error("missing flag not set")
	}
	ch = must(l.HealPath(id, "Moved/a.wav"))
	if ch.Undo != nil {
		t.Error("HealPath should not be undoable")
	}
	if s, _ := l.Sample(id); s.RelPath != "Moved/a.wav" || s.Missing {
		t.Errorf("after heal: %+v", s)
	}
	if _, ok := l.SampleIDForPath(filepath.Join(l.SamplesDir(), "Pack", "Café", "a.wav")); ok {
		t.Error("old path still resolves")
	}
}
