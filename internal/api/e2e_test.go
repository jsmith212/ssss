package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ssss/internal/audio"
	"ssss/internal/ingest"
	"ssss/internal/library"
	"ssss/internal/model"
	"ssss/internal/testaudio"
)

// machine is one API instance with its own config and cache folders, i.e.
// one computer using the library.
type machine struct {
	t *testing.T
	*API
	mu     sync.Mutex
	events []event
}

type event struct {
	name string
	data any
}

// newMachine starts an API whose per-machine folders live under dir. Reusing
// a dir restarts the same machine; a new dir is another computer.
func newMachine(t *testing.T, dir string) *machine {
	t.Helper()
	t.Setenv(library.EnvConfigDir, filepath.Join(dir, "config"))
	t.Setenv(library.EnvCacheDir, filepath.Join(dir, "cache"))
	m := &machine{t: t, API: New()}
	m.sink = func(name string, data any) {
		m.mu.Lock()
		m.events = append(m.events, event{name, data})
		m.mu.Unlock()
	}
	m.start()
	return m
}

// settle waits until all background work has finished.
func (m *machine) settle() {
	m.t.Helper()
	for range 3 {
		m.bg.Wait()
		if s := m.current(); s != nil && s.eng != nil {
			s.eng.Wait()
		}
	}
}

func (m *machine) eventsNamed(name string) []any {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []any
	for _, e := range m.events {
		if e.name == name {
			out = append(out, e.data)
		}
	}
	return out
}

func (m *machine) clearEvents() {
	m.mu.Lock()
	m.events = nil
	m.mu.Unlock()
}

// lastProgress returns the final progress event of the given job kind.
func (m *machine) lastProgress(kind string) model.Progress {
	m.t.Helper()
	var last model.Progress
	found := false
	for _, name := range []string{model.EventIngestProgress, model.EventAnalysisProgress} {
		for _, data := range m.eventsNamed(name) {
			if p := data.(model.Progress); p.Kind == kind && p.Finished {
				last, found = p, true
			}
		}
	}
	if !found {
		m.t.Fatalf("no finished %s progress event", kind)
	}
	return last
}

func (m *machine) toasts(level string) []string {
	var out []string
	for _, data := range m.eventsNamed(model.EventToast) {
		if toast := data.(model.Toast); toast.Level == level {
			out = append(out, toast.Message)
		}
	}
	return out
}

// names runs a query and returns the matching file names, sorted.
func (m *machine) names(query string) []string {
	m.t.Helper()
	res := m.Search(model.SearchRequest{Query: query, Sort: model.SortName, Limit: 500})
	if res.Error != "" {
		m.t.Fatalf("query %q: %s", query, res.Error)
	}
	out := make([]string, len(res.Rows))
	for i, row := range res.Rows {
		out[i] = row.Name
	}
	slices.Sort(out)
	if res.Total != len(out) {
		m.t.Fatalf("query %q: total %d but %d rows", query, res.Total, len(out))
	}
	return out
}

func (m *machine) expect(query string, want ...string) {
	m.t.Helper()
	slices.Sort(want)
	if got := m.names(query); !slices.Equal(got, want) {
		m.t.Errorf("query %q = %v, want %v", query, got, want)
	}
}

// id returns the ID of the one sample with the given name.
func (m *machine) id(name string) string {
	m.t.Helper()
	res := m.Search(model.SearchRequest{Limit: 500})
	for _, row := range res.Rows {
		if row.Name == name {
			return row.ID
		}
	}
	m.t.Fatalf("no sample named %q", name)
	return ""
}

func (m *machine) detail(id string) model.SampleDetail {
	m.t.Helper()
	details := m.GetSamples([]string{id})
	if len(details) != 1 {
		m.t.Fatalf("GetSamples(%s) returned %d samples", id, len(details))
	}
	return details[0]
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// writeSources lays out what the user is about to drop on the window and
// returns the paths to drop.
func writeSources(t *testing.T, dir string) []string {
	t.Helper()
	kick := testaudio.Tone(55, 0.3, 18)
	snare := testaudio.Tone(190, 0.2, 25)
	kit := filepath.Join(dir, "Drum Kit")
	files := map[string][]float32{
		filepath.Join(kit, "Kicks", "Kick_01.wav"):              kick,
		filepath.Join(kit, "Snares", "Snare_01.wav"):            snare,
		filepath.Join(kit, "Loops", "Drum_Loop_120bpm.wav"):     testaudio.Loop(120, 8, 80),
		filepath.Join(kit, "Loops", "Bass_Loop_140_Am.wav"):     testaudio.Loop(140, 8, 110),
		filepath.Join(kit, "Z Copies", "Kick_Copy.wav"):         kick, // same content as Kick_01
		filepath.Join(dir, "loose", "Pad_C.wav"):                testaudio.Tone(261.63, 2.3, 1),
		filepath.Join(dir, "loose", "Snare_From_Elsewhere.wav"): snare, // duplicate across packs
	}
	for path, samples := range files {
		must(t, testaudio.Write(path, samples))
	}
	notAudio := filepath.Join(dir, "loose", "Session Notes (final).wav")
	must(t, os.WriteFile(notAudio, []byte("this is text with an audio extension"), 0o644))
	must(t, os.WriteFile(filepath.Join(kit, "readme.txt"), []byte("hello"), 0o644))
	return []string{
		kit,
		filepath.Join(dir, "loose", "Pad_C.wav"),
		filepath.Join(dir, "loose", "Snare_From_Elsewhere.wav"),
		notAudio,
	}
}

// snapshot is everything a second machine must agree on.
type snapshot struct {
	Rows    map[string][]model.Sample
	Sidebar model.Sidebar
}

var snapshotQueries = []string{
	"", "kick", "type:loop", "type:oneshot", "bpm:120", "bpm:>130", "key:Am", "#kick", "tag:drums",
	"is:fav", "is:untagged", "in:Techno", `@"Drum Kit"`, "pack:Unsorted", "len:<1s", "fmt:wav", "is:unanalyzed",
}

func (m *machine) snapshot() snapshot {
	m.t.Helper()
	snap := snapshot{Rows: map[string][]model.Sample{}, Sidebar: m.GetSidebar()}
	// "Recent" is the last import on this machine, by design not shared.
	snap.Sidebar.Recent = 0
	for _, q := range snapshotQueries {
		res := m.Search(model.SearchRequest{Query: q, Sort: model.SortName, Limit: 500})
		if res.Error != "" {
			m.t.Fatalf("query %q: %s", q, res.Error)
		}
		snap.Rows[q] = res.Rows
	}
	return snap
}

func TestEndToEnd(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Library")
	sources := writeSources(t, t.TempDir())
	homeA, homeB := t.TempDir(), t.TempDir()

	// --- first run: create a library and import -------------------------
	a := newMachine(t, homeA)
	if st := a.GetState(); st.Status != model.StatusNone {
		t.Fatalf("first run status = %q, want none", st.Status)
	}
	if _, err := a.OpenLibrary(root); err == nil {
		t.Fatal("OpenLibrary of a missing folder succeeded")
	}
	st, err := a.CreateLibrary(root, "Main")
	must(t, err)
	if st.Status != model.StatusOK || st.LibraryName != "Main" || st.LibraryPath != root {
		t.Fatalf("after create: %+v", st)
	}
	if _, err := a.CreateLibrary(root, "Again"); err == nil {
		t.Fatal("creating a library over an existing one succeeded")
	}
	a.settle()
	a.clearEvents()

	jobID, err := a.Import(sources, model.ImportTarget{})
	must(t, err)
	a.settle()

	imp := a.lastProgress(model.JobImport)
	if imp.JobID != jobID || imp.Added != 5 || imp.Duplicates != 2 || imp.Failed != 1 || imp.Done != imp.Total || imp.Total != 8 {
		t.Errorf("import progress = %+v, want 5 added, 2 duplicates, 1 failed of 8", imp)
	}
	if got := a.lastProgress(model.JobAnalysis); got.Done != 5 || got.Failed != 0 {
		t.Errorf("analysis progress = %+v, want 5 done", got)
	}
	if len(a.eventsNamed(model.EventSamplesChanged)) == 0 {
		t.Error("import emitted no samples:changed event")
	}

	// --- what was imported and analysed ---------------------------------
	a.expect("", "Bass_Loop_140_Am", "Drum_Loop_120bpm", "Kick_01", "Pad_C", "Snare_01")
	a.expect("is:unanalyzed")
	a.expect("is:recent", "Bass_Loop_140_Am", "Drum_Loop_120bpm", "Kick_01", "Pad_C", "Snare_01")
	a.expect(`@"Drum Kit"`, "Bass_Loop_140_Am", "Drum_Loop_120bpm", "Kick_01", "Snare_01")
	a.expect("pack:Unsorted", "Pad_C")
	a.expect("type:loop", "Bass_Loop_140_Am", "Drum_Loop_120bpm")
	a.expect("bpm:120", "Drum_Loop_120bpm")
	a.expect("bpm:130-150", "Bass_Loop_140_Am")
	a.expect("key:Am", "Bass_Loop_140_Am")
	a.expect("kick", "Kick_01")
	a.expect("len:<1s", "Kick_01", "Snare_01")
	a.expect("-type:loop len:>1s", "Pad_C")
	a.expect("type:oneshot", "Kick_01", "Pad_C", "Snare_01")

	kick, snare, drumLoop := a.id("Kick_01"), a.id("Snare_01"), a.id("Drum_Loop_120bpm")
	loop := a.detail(drumLoop)
	if loop.BPM != 120 || loop.BPMSrc != model.SrcFilename || loop.Type != model.TypeLoop ||
		!loop.Analyzed || loop.Mini == "" || loop.Dur < 3.9 || loop.Dur > 4.1 || loop.SR != testaudio.Rate || loop.Ch != 1 {
		t.Errorf("analysed loop = %+v", loop.Sample)
	}
	if want := filepath.Join(root, "Samples", "Drum Kit", "Loops", "Drum_Loop_120bpm.wav"); loop.AbsPath != want || !exists(want) {
		t.Errorf("loop path = %q, want existing %q", loop.AbsPath, want)
	}
	if k := a.detail(kick); k.Type != model.TypeOneShot || !slices.Contains(k.SuggestedTags, "drums/kick") {
		t.Errorf("kick type %q, suggested tags %v", k.Type, k.SuggestedTags)
	}
	// The source's modification time survives the copy.
	srcInfo, err := os.Stat(sources[1])
	must(t, err)
	padInfo, err := os.Stat(a.detail(a.id("Pad_C")).AbsPath)
	must(t, err)
	if !padInfo.ModTime().Equal(srcInfo.ModTime()) {
		t.Errorf("copied mtime %v, source %v", padInfo.ModTime(), srcInfo.ModTime())
	}
	// No partial copies are left behind.
	leftovers, _ := filepath.Glob(filepath.Join(root, "Samples", "~$*"))
	if len(leftovers) > 0 {
		t.Errorf("temp files left in Samples: %v", leftovers)
	}

	// --- audio and peaks over HTTP --------------------------------------
	handler := AssetHandler(a.API)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audio/"+kick+".f32", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("X-Rate") != "44100" || rec.Body.Len() == 0 {
		t.Errorf("GET /audio: status %d, X-Rate %q, %d bytes", rec.Code, rec.Header().Get("X-Rate"), rec.Body.Len())
	}
	peaksFile := audio.PeaksPath(ingest.PeaksDir(a.current().cacheDir), kick)
	if !exists(peaksFile) {
		t.Errorf("analysis did not cache peaks at %s", peaksFile)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/peaks/"+kick, nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != audio.FullSize {
		t.Errorf("GET /peaks: status %d, %d bytes", rec.Code, rec.Body.Len())
	}

	// --- re-importing is all duplicates; a tag target still applies -----
	a.clearEvents()
	_, err = a.Import(sources[:1], model.ImportTarget{Tag: "Imported/Twice"})
	must(t, err)
	a.settle()
	if again := a.lastProgress(model.JobImport); again.Added != 0 || again.Duplicates != 5 {
		t.Errorf("re-import progress = %+v, want 0 added, 5 duplicates", again)
	}
	a.expect("tag:imported/twice", "Bass_Loop_140_Am", "Drum_Loop_120bpm", "Kick_01", "Snare_01")
	must(t, a.DeleteTag("imported/twice"))
	a.expect("tag:imported")

	// --- tags, with undo and redo ---------------------------------------
	must(t, a.AddTags([]string{kick, snare}, []string{"Drums/Kick"}))
	a.expect("#kick", "Kick_01", "Snare_01")
	a.expect("tag:drums", "Kick_01", "Snare_01")
	a.expect("is:untagged", "Bass_Loop_140_Am", "Drum_Loop_120bpm", "Pad_C")
	must(t, a.RemoveTags([]string{snare}, []string{"drums/kick"}))
	a.expect("#kick", "Kick_01")
	what, err := a.Undo()
	must(t, err)
	if what != "untag 1 sample" {
		t.Errorf("Undo() = %q", what)
	}
	a.expect("#kick", "Kick_01", "Snare_01")
	_, err = a.Undo()
	must(t, err)
	a.expect("#kick")
	_, err = a.Redo()
	must(t, err)
	_, err = a.Redo()
	must(t, err)
	a.expect("#kick", "Kick_01")
	if what, err := a.Redo(); err != nil || what != "" {
		t.Errorf("Redo() with nothing to redo = %q, %v", what, err)
	}
	must(t, a.RenameTag("drums/kick", "drums/bd"))
	a.expect("tag:drums/bd", "Kick_01")
	must(t, a.SetTagColor("drums", "#e66"))
	sb := a.GetSidebar()
	if i := slices.IndexFunc(sb.Tags, func(tc model.TagCount) bool { return tc.Tag == "drums" }); i < 0 || sb.Tags[i].Count != 1 || sb.Tags[i].Color != "#e66" {
		t.Errorf("sidebar tags = %+v", sb.Tags)
	}
	if sb.All != 5 || sb.Recent != 5 || len(sb.Packs) != 2 {
		t.Errorf("sidebar = %+v", sb)
	}

	// --- collections, favourites, metadata, views -----------------------
	coll, err := a.CreateCollection("Techno")
	must(t, err)
	must(t, a.AddToCollection(coll.ID, []string{kick, drumLoop}))
	a.expect("in:Techno", "Drum_Loop_120bpm", "Kick_01")
	must(t, a.RemoveFromCollection(coll.ID, []string{kick}))
	a.expect("in:Techno", "Drum_Loop_120bpm")
	_, err = a.Undo()
	must(t, err)
	a.expect("in:Techno", "Drum_Loop_120bpm", "Kick_01")
	if d := a.detail(kick); !slices.Equal(d.Collections, []string{coll.ID}) {
		t.Errorf("kick collections = %v", d.Collections)
	}

	must(t, a.SetFavorite([]string{snare}, true))
	a.expect("is:fav", "Snare_01")

	bpm, key := 99.0, "Bb minor"
	must(t, a.SetMeta([]string{drumLoop}, model.MetaPatch{BPM: &bpm, Key: &key}))
	a.expect("bpm:99", "Drum_Loop_120bpm")
	a.expect("key:A#m", "Drum_Loop_120bpm")
	bad := "H#"
	if err := a.SetMeta([]string{drumLoop}, model.MetaPatch{Key: &bad}); err == nil {
		t.Error("SetMeta accepted key H#")
	}
	// A user value survives re-analysis.
	must(t, a.Reanalyze([]string{drumLoop}))
	a.settle()
	if d := a.detail(drumLoop); d.BPM != 99 || d.BPMSrc != model.SrcUser || d.Key != "A#m" {
		t.Errorf("after reanalysis: bpm %v (%s), key %q", d.BPM, d.BPMSrc, d.Key)
	}
	_, err = a.Undo() // the SetMeta
	must(t, err)
	a.expect("bpm:120", "Drum_Loop_120bpm")

	view, err := a.SaveView("Loops", "type:loop")
	must(t, err)
	if views := a.GetSidebar().Views; len(views) != 1 || views[0] != view {
		t.Errorf("views = %+v", views)
	}
	must(t, a.RenamePack(a.detail(kick).PackID, "Drum Kit Deluxe"))
	a.expect(`@"Drum Kit Deluxe"`, "Bass_Loop_140_Am", "Drum_Loop_120bpm", "Kick_01", "Snare_01")
	_, err = a.Undo()
	must(t, err)
	a.expect(`@"Drum Kit Deluxe"`)

	// --- remove and restore ---------------------------------------------
	kickPath := a.detail(kick).AbsPath
	a.clearEvents()
	must(t, a.RemoveSamples([]string{kick}))
	a.expect("kick")
	if exists(kickPath) || !exists(filepath.Join(root, "_library", "trash", kick+".wav")) {
		t.Error("removed file was not moved to the library trash")
	}
	changed := a.eventsNamed(model.EventSamplesChanged)
	if len(changed) != 1 || !slices.Contains(changed[0].(model.SamplesChanged).IDs, kick) || !changed[0].(model.SamplesChanged).Structural {
		t.Errorf("remove emitted %+v", changed)
	}
	what, err = a.Undo()
	must(t, err)
	if what != "remove 1 sample" || !exists(kickPath) {
		t.Errorf("undo remove = %q, file restored: %v", what, exists(kickPath))
	}
	a.expect("kick", "Kick_01")
	a.expect("tag:drums/bd", "Kick_01")
	a.expect("in:Techno", "Drum_Loop_120bpm", "Kick_01")
	_, err = a.Redo()
	must(t, err)
	a.expect("kick")
	_, err = a.Undo()
	must(t, err)

	// --- rename ----------------------------------------------------------
	snarePath := a.detail(snare).AbsPath
	must(t, a.RenameSample(snare, "Snare Tight"))
	renamed := a.detail(snare)
	if renamed.Name != "Snare Tight" || renamed.RelPath != "Snares/Snare Tight.wav" || !exists(renamed.AbsPath) || exists(snarePath) {
		t.Errorf("after rename: %+v", renamed)
	}
	a.expect("tight", "Snare Tight")
	if err := a.RenameSample(snare, "  "); err == nil {
		t.Error("RenameSample accepted an empty name")
	}
	_, err = a.Undo()
	must(t, err)
	if !exists(snarePath) {
		t.Error("undo of rename did not move the file back")
	}
	a.expect("snare", "Snare_01")

	// --- a second machine sees the same library -------------------------
	want := a.snapshot()
	a.shutdown()

	b := newMachine(t, homeB)
	if st := b.GetState(); st.Status != model.StatusNone {
		t.Fatalf("second machine starts with status %q", st.Status)
	}
	st, err = b.OpenLibrary(root)
	must(t, err)
	if st.Status != model.StatusOK || st.LibraryName != "Main" {
		t.Fatalf("second machine state = %+v", st)
	}
	b.settle()
	if got := b.snapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("second machine disagrees with the first:\n got %+v\nwant %+v", got, want)
	}
	if errs := b.toasts(model.ToastError); len(errs) > 0 {
		t.Errorf("second machine error toasts: %v", errs)
	}

	// --- a sync conflict copy is merged on rescan -----------------------
	must(t, b.current().lib.Flush())
	packFile := filepath.Join(root, "_library", "packs", b.detail(kick).PackID+".json")
	conflict := strings.TrimSuffix(packFile, ".json") + " (conflicted copy 2026-11-15).json"
	writeConflictCopy(t, packFile, conflict, snare, "from/laptop")
	b.clearEvents()
	must(t, b.Rescan())
	b.settle()
	b.expect("tag:from/laptop", "Snare_01")
	b.expect("is:fav", "Snare_01") // the rest of the record is intact
	if exists(conflict) {
		t.Error("conflict copy was not moved away after merging")
	}
	if trashed, _ := filepath.Glob(filepath.Join(root, "_library", "trash", "*conflicted copy*")); len(trashed) != 1 {
		t.Errorf("conflict copies in trash: %v", trashed)
	}

	// --- files moved, added and deleted outside the app -----------------
	moved := filepath.Join(root, "Samples", "Drum Kit", "Moved", "Renamed Loop.wav")
	must(t, os.MkdirAll(filepath.Dir(moved), 0o755))
	must(t, os.Rename(b.detail(drumLoop).AbsPath, moved))
	must(t, testaudio.Write(filepath.Join(root, "Samples", "Field Recordings", "Rain", "Rain_90bpm.wav"), testaudio.Loop(90, 4, 300)))
	must(t, os.Remove(b.detail(b.id("Pad_C")).AbsPath))
	b.clearEvents()
	must(t, b.Rescan())
	b.settle()

	healed := b.detail(drumLoop)
	if healed.RelPath != "Moved/Renamed Loop.wav" || healed.AbsPath != moved || healed.Missing {
		t.Errorf("moved sample not healed: %+v", healed)
	}
	b.expect("renamed", "Renamed Loop")
	b.expect("in:Techno", "Kick_01", "Renamed Loop") // identity survived the move
	b.expect(`@"Field Recordings"`, "Rain_90bpm")
	b.expect("bpm:90", "Rain_90bpm")
	b.expect("is:missing", "Pad_C")
	b.expect("is:unanalyzed")
	if scan := b.lastProgress(model.JobRescan); scan.Added != 1 || scan.Failed != 0 {
		t.Errorf("rescan progress = %+v", scan)
	}
	if info := b.toasts(model.ToastInfo); len(info) != 1 || info[0] != "Rescan: 1 added, 1 relocated, 1 missing" {
		t.Errorf("rescan toast = %v", info)
	}

	// --- the first machine catches up from its own cache ----------------
	want = b.snapshot()
	b.shutdown()
	a2 := newMachine(t, homeA)
	if st := a2.GetState(); st.Status != model.StatusOK || st.LibraryPath != root {
		t.Fatalf("restart state = %+v", st)
	}
	a2.settle()
	a2.expect("is:missing", "Pad_C") // found by its own startup rescan
	if got := a2.snapshot(); !reflect.DeepEqual(got, want) {
		t.Errorf("first machine after restart disagrees:\n got %+v\nwant %+v", got, want)
	}
	if what, err := a2.Undo(); err != nil || what != "" {
		t.Errorf("undo history survived a restart: %q, %v", what, err)
	}
	a2.shutdown()
}

// writeConflictCopy writes what a sync client leaves behind when another
// machine edited the pack at the same time: a copy of the pack file in which
// one sample carries an extra tag and a newer revision.
func writeConflictCopy(t *testing.T, packFile, conflict, sampleID, tag string) {
	t.Helper()
	data, err := os.ReadFile(packFile)
	must(t, err)
	var pack map[string]any
	must(t, json.Unmarshal(data, &pack))
	rec := pack["samples"].(map[string]any)[sampleID].(map[string]any)
	rec["tags"] = append(rec["tags"].([]any), tag)
	rec["rev"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	out, err := json.Marshal(pack)
	must(t, err)
	must(t, os.WriteFile(conflict, out, 0o644))
}

func TestUnavailableLibrary(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Library")
	away := root + " (unmounted)"
	home := t.TempDir()

	a := newMachine(t, home)
	_, err := a.CreateLibrary(root, "Main")
	must(t, err)
	_, err = a.Import(writeSources(t, t.TempDir())[:1], model.ImportTarget{})
	must(t, err)
	a.settle()
	a.shutdown()

	must(t, os.Rename(root, away))
	b := newMachine(t, home)
	t.Cleanup(b.shutdown)
	st := b.GetState()
	if st.Status != model.StatusUnavailable || st.LibraryPath != root || st.LibraryName != "Main" || st.Message == "" {
		t.Fatalf("state with the folder gone = %+v", st)
	}
	// The cache still answers.
	b.expect("type:loop", "Bass_Loop_140_Am", "Drum_Loop_120bpm")
	if d := b.GetSamples([]string{b.id("Kick_01")}); len(d) != 1 || d[0].Name != "Kick_01" {
		t.Errorf("cached details = %+v", d)
	}
	if err := b.AddTags([]string{b.id("Kick_01")}, []string{"x"}); err == nil {
		t.Error("mutation succeeded without the library folder")
	}
	if _, err := b.Import([]string{away}, model.ImportTarget{}); err == nil {
		t.Error("import succeeded without the library folder")
	}
	if exists(root) {
		t.Error("the missing library folder was recreated")
	}
	if st := b.RetryLibrary(); st.Status != model.StatusUnavailable {
		t.Errorf("retry with the folder still gone = %+v", st)
	}

	must(t, os.Rename(away, root))
	if st := b.RetryLibrary(); st.Status != model.StatusOK {
		t.Fatalf("retry with the folder back = %+v", st)
	}
	b.settle()
	must(t, b.AddTags([]string{b.id("Kick_01")}, []string{"x"}))
	b.expect("tag:x", "Kick_01")

	// "Locate…": the same library at a new path.
	b.shutdown()
	must(t, os.Rename(root, away))
	c := newMachine(t, home)
	t.Cleanup(c.shutdown)
	st, err = c.OpenLibrary(away)
	must(t, err)
	if st.Status != model.StatusOK || st.LibraryPath != away {
		t.Fatalf("after locate: %+v", st)
	}
	c.settle()
	c.expect("tag:x", "Kick_01")
}

func TestFileDrop(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Library")
	sources := writeSources(t, t.TempDir())
	a := newMachine(t, t.TempDir())
	t.Cleanup(a.shutdown)
	_, err := a.CreateLibrary(root, "Main")
	must(t, err)
	coll, err := a.CreateCollection("Set")
	must(t, err)
	a.settle()

	// Files from outside, dropped on a collection: imported and added.
	a.clearEvents()
	a.handleFileDrop(sources[:1], DropTarget{Kind: "collection", ID: coll.ID})
	a.settle()
	drops := a.eventsNamed(model.EventDrop)
	if len(drops) != 1 || drops[0].(model.DropEvent).Kind != model.DropImport || drops[0].(model.DropEvent).JobID == "" {
		t.Fatalf("drop events = %+v", drops)
	}
	a.expect("in:Set", "Bass_Loop_140_Am", "Drum_Loop_120bpm", "Kick_01", "Snare_01")

	// Our own files dropped on a tag: assigned, not imported, undoable.
	own := []string{a.detail(a.id("Kick_01")).AbsPath, a.detail(a.id("Snare_01")).AbsPath}
	a.clearEvents()
	a.handleFileDrop(own, DropTarget{Kind: "tag", ID: "drums"})
	a.settle()
	drops = a.eventsNamed(model.EventDrop)
	if len(drops) != 1 || drops[0].(model.DropEvent) != (model.DropEvent{Kind: model.DropAssign, Count: 2, Target: model.ImportTarget{Tag: "drums"}}) {
		t.Fatalf("drop events = %+v", drops)
	}
	a.expect("tag:drums", "Kick_01", "Snare_01")
	_, err = a.Undo()
	must(t, err)
	a.expect("tag:drums")

	// Our own files dropped on the list or a pack: nothing happens.
	a.clearEvents()
	a.handleFileDrop(own, DropTarget{Kind: "list"})
	a.handleFileDrop(own, DropTarget{Kind: "pack", ID: a.detail(a.id("Kick_01")).PackID})
	a.settle()
	if n := len(a.eventsNamed(model.EventDrop)) + len(a.eventsNamed(model.EventIngestProgress)); n != 0 {
		t.Errorf("self-drop outside the sidebar caused %d events", n)
	}
	a.expect("", "Bass_Loop_140_Am", "Drum_Loop_120bpm", "Kick_01", "Snare_01")

	// Loose files dropped on a pack join that pack.
	a.handleFileDrop(sources[1:2], DropTarget{Kind: "pack", ID: a.detail(a.id("Kick_01")).PackID})
	a.settle()
	a.expect(`@"Drum Kit"`, "Bass_Loop_140_Am", "Drum_Loop_120bpm", "Kick_01", "Pad_C", "Snare_01")
}

func TestSettingsPersist(t *testing.T) {
	home := t.TempDir()
	a := newMachine(t, home)
	s := a.GetState().Settings
	s.Theme, s.Volume, s.ProjectBPM, s.ProjectKey = "dark", 3, 128, "eb minor"
	s.PinnedTags = []string{"Drums/Kick"}
	got, err := a.UpdateSettings(s)
	must(t, err)
	if got.Theme != "dark" || got.Volume != 1 || got.ProjectBPM != 128 || got.ProjectKey != "D#m" || !slices.Equal(got.PinnedTags, []string{"drums/kick"}) {
		t.Errorf("stored settings = %+v", got)
	}
	a.shutdown()
	b := newMachine(t, home)
	t.Cleanup(b.shutdown)
	if again := b.GetState().Settings; !reflect.DeepEqual(again, got) {
		t.Errorf("settings after restart = %+v, want %+v", again, got)
	}
}
