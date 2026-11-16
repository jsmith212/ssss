package ingest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"ssss/internal/index"
	"ssss/internal/library"
	"ssss/internal/model"
	"ssss/internal/testaudio"
)

type fixture struct {
	t        *testing.T
	root     string
	cacheDir string
	lib      *library.Library
	idx      *index.Index
	eng      *Engine

	mu       sync.Mutex
	progress []model.Progress
	failures []error
}

// open opens (or reopens) the library at root with the cache in cacheDir.
func open(t *testing.T, root, cacheDir string, debounce time.Duration) *fixture {
	t.Helper()
	f := &fixture{t: t, root: root, cacheDir: cacheDir}
	var err error
	opts := library.Options{Debounce: debounce}
	if library.IsLibrary(root) {
		f.lib, err = library.Open(root, opts)
	} else {
		f.lib, err = library.Create(root, "Test", opts)
	}
	if err != nil {
		t.Fatal(err)
	}
	if f.idx, err = index.Open(IndexPath(cacheDir)); err != nil {
		t.Fatal(err)
	}
	f.eng = New(f.lib, f.idx, cacheDir, Notifier{
		Progress: func(_ string, p model.Progress) {
			f.mu.Lock()
			f.progress = append(f.progress, p)
			f.mu.Unlock()
		},
		Failed: func(err error) {
			f.mu.Lock()
			f.failures = append(f.failures, err)
			f.mu.Unlock()
		},
	})
	if err := f.eng.SyncIndex(false); err != nil {
		t.Fatal(err)
	}
	return f
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := open(t, filepath.Join(t.TempDir(), "Library"), t.TempDir(), 0)
	t.Cleanup(f.close)
	return f
}

func (f *fixture) close() {
	if err := f.eng.Close(); err != nil {
		f.t.Error(err)
	}
	f.lib.Close()
	f.idx.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, err := range f.failures {
		f.t.Errorf("background failure: %v", err)
	}
}

// finished returns the final progress of the job.
func (f *fixture) finished(jobID string) model.Progress {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.progress {
		if p.JobID == jobID && p.Finished {
			return p
		}
	}
	f.t.Fatalf("job %s did not finish", jobID)
	return model.Progress{}
}

func (f *fixture) search(query string) []string {
	f.t.Helper()
	res, err := f.idx.Search(model.SearchRequest{Query: query, Sort: model.SortName, Limit: 500})
	if err != nil || res.Error != "" {
		f.t.Fatalf("search %q: %v %s", query, err, res.Error)
	}
	names := make([]string, len(res.Rows))
	for i, row := range res.Rows {
		names[i] = row.Name
	}
	return names
}

func (f *fixture) expect(query string, want ...string) {
	f.t.Helper()
	if want == nil {
		want = []string{}
	}
	if got := f.search(query); !slices.Equal(got, want) {
		f.t.Errorf("search %q = %v, want %v", query, got, want)
	}
}

// importPaths imports and waits, returning the final progress.
func (f *fixture) importPaths(target model.ImportTarget, paths ...string) model.Progress {
	f.t.Helper()
	id, err := f.eng.Import(model.JobImport, target, PlanPaths(paths))
	if err != nil {
		f.t.Fatal(err)
	}
	f.eng.Wait()
	return f.finished(id)
}

// tones writes n distinct one-shots named <prefix>_NN.wav into dir.
func tones(t *testing.T, dir, prefix string, n int) {
	t.Helper()
	// The prefix sets the pitch, so files of different sets never share
	// content (and with it an ID).
	base := 200.0
	for _, c := range prefix {
		base += float64(c % 97)
	}
	for i := range n {
		path := filepath.Join(dir, fmt.Sprintf("%s_%02d.wav", prefix, i+1))
		if err := testaudio.Write(path, testaudio.Tone(base+37*float64(i), 0.1, 20)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlanPaths(t *testing.T) {
	dir := t.TempDir()
	tones(t, filepath.Join(dir, "Pack A", "Sub"), "tone", 2)
	tones(t, filepath.Join(dir, "Pack A", ".hidden"), "secret", 1)
	tones(t, filepath.Join(dir, "loose"), "single", 1)
	text := filepath.Join(dir, "loose", "notes.wav")
	os.WriteFile(text, []byte("not audio at all, whatever the name says"), 0o644)
	os.WriteFile(filepath.Join(dir, "Pack A", "cover.jpg"), []byte{0xff, 0xd8, 0xff}, 0o644)
	os.MkdirAll(filepath.Join(dir, "Empty"), 0o755)

	plan, err := PlanPaths([]string{
		filepath.Join(dir, "Pack A"),
		filepath.Join(dir, "Empty"),
		filepath.Join(dir, "loose", "single_01.wav"),
		text,
		filepath.Join(dir, "gone.wav"),
	})(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Groups) != 2 {
		t.Fatalf("groups = %+v", plan.Groups)
	}
	pack, loose := plan.Groups[0], plan.Groups[1]
	if pack.Name != "Pack A" || pack.Loose || len(pack.Files) != 2 || pack.Files[0].Rel != "Sub/tone_01.wav" {
		t.Errorf("folder group = %+v", pack)
	}
	if loose.Name != UnsortedPack || !loose.Loose || len(loose.Files) != 1 || loose.Files[0].Rel != "single_01.wav" {
		t.Errorf("loose group = %+v", loose)
	}
	if len(plan.Skipped) != 3 {
		t.Errorf("skipped = %v, want the empty folder, the text file and the missing file", plan.Skipped)
	}
}

func TestImportTargets(t *testing.T) {
	f := newFixture(t)
	src := t.TempDir()
	tones(t, filepath.Join(src, "Kit"), "hit", 3)
	tones(t, filepath.Join(src, "More"), "extra", 2)

	if p := f.importPaths(model.ImportTarget{}, filepath.Join(src, "Kit")); p.Added != 3 || p.Kind != model.JobImport {
		t.Fatalf("first import = %+v", p)
	}
	kit, ok := f.lib.PackByDir("Kit")
	if !ok || kit.Source != library.SourceImport {
		t.Fatalf("pack for the dropped folder = %+v, %v", kit, ok)
	}
	_, ch, err := f.lib.CreateCollection("Set")
	if err != nil {
		t.Fatal(err)
	}
	f.eng.Apply(ch)
	coll := f.lib.Collections()[0]

	// A folder dropped on a pack keeps its name as a subfolder there, and
	// the tag and collection reach new files and duplicates alike.
	p := f.importPaths(model.ImportTarget{PackID: kit.ID, Tag: "Perc/Hits", CollectionID: coll.ID},
		filepath.Join(src, "More"), filepath.Join(src, "Kit", "hit_01.wav"))
	if p.Added != 2 || p.Duplicates != 1 || p.Failed != 0 {
		t.Errorf("targeted import = %+v", p)
	}
	f.expect("pack:Kit", "extra_01", "extra_02", "hit_01", "hit_02", "hit_03")
	f.expect("tag:perc/hits", "extra_01", "extra_02", "hit_01")
	f.expect("in:Set", "extra_01", "extra_02", "hit_01")
	f.expect("is:recent", "extra_01", "extra_02")
	if _, err := os.Stat(filepath.Join(f.root, "Samples", "Kit", "More", "extra_01.wav")); err != nil {
		t.Error(err)
	}
	f.expect("is:unanalyzed")

	if _, err := f.eng.Import(model.JobImport, model.ImportTarget{PackID: "pk_nope"}, PlanPaths(nil)); err == nil {
		t.Error("import into an unknown pack was accepted")
	}
}

func TestImportCollidingNames(t *testing.T) {
	f := newFixture(t)
	src := t.TempDir()
	// Same name, different content, both loose: the second gets a suffix.
	a, b := filepath.Join(src, "a", "Clap.wav"), filepath.Join(src, "b", "Clap.wav")
	testaudio.Write(a, testaudio.Tone(300, 0.1, 10))
	testaudio.Write(b, testaudio.Tone(400, 0.1, 10))
	if p := f.importPaths(model.ImportTarget{}, a, b); p.Added != 2 {
		t.Fatalf("import = %+v", p)
	}
	f.expect("pack:Unsorted", "Clap", "Clap (2)")
}

func TestImportCancel(t *testing.T) {
	f := newFixture(t)
	started := make(chan struct{})
	id, err := f.eng.Import(model.JobImport, model.ImportTarget{}, func(ctx context.Context) (Plan, error) {
		close(started)
		<-ctx.Done()
		return Plan{}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if !f.eng.Cancel(id) {
		t.Fatal("Cancel did not find the job")
	}
	f.eng.Wait()
	if p := f.finished(id); !p.Cancelled || p.Failed != 0 {
		t.Errorf("cancelled import = %+v", p)
	}
	if f.eng.Cancel(id) {
		t.Error("Cancel found a finished job")
	}
}

func TestRescanSafetyStop(t *testing.T) {
	f := newFixture(t)
	src := t.TempDir()
	tones(t, filepath.Join(src, "Kit"), "hit", 30)
	f.importPaths(model.ImportTarget{}, filepath.Join(src, "Kit"))

	kitDir := filepath.Join(f.root, "Samples", "Kit")
	hidden := kitDir + ".away"
	// More than a fifth of the library vanishing looks like a folder that
	// is still syncing, not like the user deleting things.
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("hit_%02d.wav", i)
		os.MkdirAll(hidden, 0o755)
		if err := os.Rename(filepath.Join(kitDir, name), filepath.Join(hidden, name)); err != nil {
			t.Fatal(err)
		}
	}
	away := filepath.Join(filepath.Dir(f.root), "away")
	if err := os.Rename(hidden, away); err != nil {
		t.Fatal(err)
	}

	res, err := f.eng.Rescan(RescanOptions{Quiet: true})
	if err != nil || res.MissingStopped != 12 || res.Missing != 0 {
		t.Fatalf("rescan = %+v, %v; want the safety stop", res, err)
	}
	f.expect("is:missing")

	res, err = f.eng.Rescan(RescanOptions{AllowMassMissing: true})
	if err != nil || res.Missing != 12 || res.MissingStopped != 0 {
		t.Fatalf("confirmed rescan = %+v, %v", res, err)
	}
	if got := f.search("is:missing"); len(got) != 12 || got[0] != "hit_01" || got[11] != "hit_12" {
		t.Errorf("flagged missing: %v", got)
	}

	// Already flagged files do not count towards the stop again.
	res, err = f.eng.Rescan(RescanOptions{Quiet: true})
	if err != nil || res.Missing != 0 || res.MissingStopped != 0 {
		t.Fatalf("repeat rescan = %+v, %v", res, err)
	}

	// The files come back (sync finished): the flags clear.
	for i := 1; i <= 12; i++ {
		name := fmt.Sprintf("hit_%02d.wav", i)
		if err := os.Rename(filepath.Join(away, name), filepath.Join(kitDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	res, err = f.eng.Rescan(RescanOptions{Quiet: true})
	if err != nil || res.Found != 12 {
		t.Fatalf("rescan after the files returned = %+v, %v", res, err)
	}
	f.expect("is:missing")
}

func TestRescanRenamedPackFolder(t *testing.T) {
	f := newFixture(t)
	src := t.TempDir()
	tones(t, filepath.Join(src, "Kit"), "hit", 4)
	f.importPaths(model.ImportTarget{}, filepath.Join(src, "Kit"))
	ids, _ := f.idx.SearchIDs("", model.SortName, false)
	ch, _ := f.lib.AddTags(ids[:1], []string{"keep"})
	f.eng.Apply(ch)

	samples := filepath.Join(f.root, "Samples")
	if err := os.Rename(filepath.Join(samples, "Kit"), filepath.Join(samples, "Renamed Kit")); err != nil {
		t.Fatal(err)
	}
	// A file the library has never seen, not in any pack's folder.
	testaudio.Write(filepath.Join(samples, "Splice", "New Pack", "Loops", "new_100bpm.wav"), testaudio.Loop(100, 4, 150))
	os.WriteFile(filepath.Join(samples, "Renamed Kit", "hit_01.wav.asd"), []byte("ableton analysis"), 0o644)

	res, err := f.eng.Rescan(RescanOptions{})
	f.eng.Wait()
	if err != nil || res.Healed != 4 || res.Added != 1 || res.Missing != 0 {
		t.Fatalf("rescan = %+v, %v", res, err)
	}
	f.expect(`pack:"Renamed Kit"`, "hit_01", "hit_02", "hit_03", "hit_04")
	f.expect("tag:keep", "hit_01")
	f.expect("is:missing")
	f.expect("bpm:100", "new_100bpm")
	if p, ok := f.lib.PackByDir("Renamed Kit"); !ok || p.Source != library.SourceScan || p.Count != 4 {
		t.Errorf("pack adopted for the renamed folder = %+v, %v", p, ok)
	}
	if abs, _ := f.lib.AbsPath(ids[0]); abs != filepath.Join(samples, "Renamed Kit", "hit_01.wav") {
		t.Errorf("healed path = %q", abs)
	}

	// Nothing left to look at: the next scan hashes nothing.
	f.mu.Lock()
	f.progress = nil
	f.mu.Unlock()
	if res, err := f.eng.Rescan(RescanOptions{Quiet: true}); err != nil || res != (RescanResult{}) {
		t.Errorf("second rescan = %+v, %v", res, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.progress) != 0 {
		t.Errorf("quiet rescan with nothing to do emitted %d progress events", len(f.progress))
	}
}

func TestPackDirsLocate(t *testing.T) {
	f := newFixture(t)
	splice, _, err := f.lib.CreatePack("Vol 1", "Splice/Vol 1", library.SourceSplice)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		rel      string
		wantDir  string // "" = error
		wantName string
		wantRel  string
	}{
		{"Splice/Vol 1/Kicks/a.wav", "Splice/Vol 1", "Vol 1", "Kicks/a.wav"},
		{"splice/VOL 1/b.wav", "Splice/Vol 1", "Vol 1", "b.wav"},
		{"Splice/Vol 2/Deep/c.wav", "Splice/Vol 2", "Vol 2", "Deep/c.wav"},
		{"Mine/Sub/d.wav", "Mine", "Mine", "Sub/d.wav"},
		{"Mine/e.wav", "Mine", "Mine", "e.wav"},
		{"Splice/f.wav", "", "", ""},
		{"g.wav", "Unsorted", "Unsorted", "g.wav"},
		{"Bad:Name/h.wav", "", "", ""},
	}
	dirs := newPackDirs(f.lib.Packs())
	for _, tt := range tests {
		packID, rel, err := dirs.locate(f.eng, tt.rel)
		if tt.wantDir == "" {
			if err == nil {
				t.Errorf("locate(%q) = %s, %q; want an error", tt.rel, packID, rel)
			}
			continue
		}
		if err != nil {
			t.Errorf("locate(%q): %v", tt.rel, err)
			continue
		}
		pack, _ := f.lib.PackByDir(tt.wantDir)
		if pack.ID != packID || pack.Name != tt.wantName || rel != tt.wantRel {
			t.Errorf("locate(%q) = pack %s (%+v), rel %q; want dir %q, rel %q", tt.rel, packID, pack, rel, tt.wantDir, tt.wantRel)
		}
	}
	if got, _ := f.lib.PackByDir("Splice/Vol 1"); got.ID != splice.ID {
		t.Error("existing pack was not reused")
	}
	if n := len(f.lib.Packs()); n != 4 {
		t.Errorf("%d packs after locating, want 4 (no pack for rejected folders)", n)
	}
}

// A session that dies before its debounced write leaves the index ahead of
// the pack files. The next session must notice and rebuild from the files.
func TestSyncAfterCrash(t *testing.T) {
	root, cache := filepath.Join(t.TempDir(), "Library"), t.TempDir()
	first := open(t, root, cache, 0)
	src := t.TempDir()
	tones(t, filepath.Join(src, "Kit"), "hit", 2)
	first.importPaths(model.ImportTarget{}, filepath.Join(src, "Kit"))
	first.close()

	crashed := open(t, root, cache, time.Hour)
	ids, _ := crashed.idx.SearchIDs("", model.SortName, false)
	ch, err := crashed.lib.AddTags(ids, []string{"unsaved"})
	if err != nil {
		t.Fatal(err)
	}
	crashed.eng.Apply(ch)
	crashed.expect("tag:unsaved", "hit_01", "hit_02")
	crashed.idx.Close() // no engine or library close: the tag never reaches disk

	after := open(t, root, cache, 0)
	defer after.close()
	after.expect("tag:unsaved")
	after.expect("", "hit_01", "hit_02")
}

// A clean close stamps the index, so the next start has nothing to re-read;
// a pack file changed by another machine in between is picked up.
func TestSyncIncremental(t *testing.T) {
	root, cache := filepath.Join(t.TempDir(), "Library"), t.TempDir()
	first := open(t, root, cache, 0)
	src := t.TempDir()
	tones(t, filepath.Join(src, "Kit"), "hit", 2)
	tones(t, filepath.Join(src, "Other"), "tone", 2)
	first.importPaths(model.ImportTarget{}, filepath.Join(src, "Kit"), filepath.Join(src, "Other"))
	first.close()
	if _, err := os.Stat(dirtyMarker(cache)); err == nil {
		t.Error("crash marker survived a clean close")
	}

	// "Another machine": a second cache, same folder.
	other := open(t, root, t.TempDir(), 0)
	ids, _ := other.idx.SearchIDs("pack:Kit", model.SortName, false)
	ch, _ := other.lib.AddTags(ids, []string{"remote"})
	other.eng.Apply(ch)
	other.close()

	second := open(t, root, cache, 0)
	defer second.close()
	second.expect("tag:remote", "hit_01", "hit_02")
	packs, _ := second.idx.Packs()
	for _, p := range packs {
		if p.FileMTime == 0 || p.FileSize == 0 {
			t.Errorf("pack %s has no file stamp after sync: %+v", p.Name, p)
		}
	}
}
