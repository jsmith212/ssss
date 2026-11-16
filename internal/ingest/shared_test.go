package ingest

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"

	"ssss/internal/library"
	"ssss/internal/model"
	"ssss/internal/testaudio"
)

// These tests cover a library folder shared with sync clients, other
// machines and the user's file manager.

func (f *fixture) rescan(opts RescanOptions) RescanResult {
	f.t.Helper()
	res, err := f.eng.Rescan(opts)
	if err != nil {
		f.t.Fatalf("rescan: %v", err)
	}
	f.eng.Wait()
	return res
}

func (f *fixture) ids(query string) []string {
	f.t.Helper()
	ids, err := f.idx.SearchIDs(query, model.SortName, false)
	if err != nil {
		f.t.Fatal(err)
	}
	return ids
}

// forgive drops the background failures a test provoked on purpose.
func (f *fixture) forgive() []error {
	f.mu.Lock()
	defer f.mu.Unlock()
	errs := f.failures
	f.failures = nil
	return errs
}

func packFiles(t *testing.T, root string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "_library", "packs", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// importedKit returns a closed library holding pack "Kit" with three tagged
// samples, and the path of its pack file.
func importedKit(t *testing.T) (root, packFile string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "Library")
	f := open(t, root, t.TempDir(), 0)
	src := t.TempDir()
	tones(t, filepath.Join(src, "Kit"), "hit", 3)
	f.importPaths(model.ImportTarget{Tag: "kept"}, filepath.Join(src, "Kit"))
	f.close()
	files := packFiles(t, root)
	if len(files) != 1 {
		t.Fatalf("pack files = %v", files)
	}
	return root, files[0]
}

func TestRescanWaitsForPackFilesStillSyncing(t *testing.T) {
	check := func(t *testing.T, f *fixture) {
		t.Helper()
		packs := f.lib.Packs()
		if len(packs) != 1 || packs[0].Source != library.SourceImport || packs[0].Count != 3 {
			t.Fatalf("packs = %+v, want the one imported pack", packs)
		}
		if got := packFiles(t, f.root); len(got) != 1 {
			t.Errorf("pack files = %v", got)
		}
		f.expect("tag:kept", "hit_01", "hit_02", "hit_03")
		f.expect("", "hit_01", "hit_02", "hit_03")
		// A removal sticks: the file goes and the sample with it.
		id := f.ids("")[1]
		abs, _ := f.lib.AbsPath(id)
		ch, err := f.lib.RemoveSamples([]string{id})
		if err != nil {
			t.Fatal(err)
		}
		f.eng.Apply(ch)
		if _, err := os.Stat(abs); err == nil || f.lib.Has(id) {
			t.Error("removed sample is still there")
		}
		f.expect("", "hit_01", "hit_03")
	}

	t.Run("pack file arrives after the audio", func(t *testing.T) {
		root, packFile := importedKit(t)
		aside := packFile + ".aside"
		os.Rename(packFile, aside)
		f := open(t, root, t.TempDir(), 0)
		defer f.close()
		// The automatic rescan of a machine that has the audio but not yet
		// the pack file leaves the folder alone.
		if res := f.rescan(RescanOptions{Quiet: true}); res.Added != 0 || res.Waiting != 3 {
			t.Fatalf("first quiet rescan = %+v", res)
		}
		if len(f.lib.Packs()) != 0 || len(packFiles(t, root)) != 0 {
			t.Fatal("a pack was made up for a folder seen for the first time")
		}
		os.Rename(aside, packFile)
		if res := f.rescan(RescanOptions{Quiet: true}); !res.Reloaded || res.Added != 0 {
			t.Fatalf("rescan after the pack file arrived = %+v", res)
		}
		check(t, f)
	})

	t.Run("pack file arrives after the files were adopted", func(t *testing.T) {
		root, packFile := importedKit(t)
		aside := packFile + ".aside"
		os.Rename(packFile, aside)
		f := open(t, root, t.TempDir(), 0)
		defer f.close()
		f.rescan(RescanOptions{Quiet: true})
		// Still no pack file the next time round: now the files are taken.
		if res := f.rescan(RescanOptions{Quiet: true}); res.Added != 3 {
			t.Fatalf("second quiet rescan = %+v", res)
		}
		f.expect("tag:kept")
		os.Rename(aside, packFile)
		if res := f.rescan(RescanOptions{Quiet: true}); !res.Reloaded {
			t.Fatalf("rescan after the pack file arrived = %+v", res)
		}
		check(t, f)
	})

	t.Run("pack file arrives in two halves", func(t *testing.T) {
		root, packFile := importedKit(t)
		whole, _ := os.ReadFile(packFile)
		os.WriteFile(packFile, whole[:len(whole)/2], 0o644)
		f := open(t, root, t.TempDir(), 0)
		defer f.close()
		// Not even a rescan the user asks for adopts anything while a pack
		// file is unreadable.
		for range 2 {
			if res := f.rescan(RescanOptions{}); res.Added != 0 || res.Waiting != 3 {
				t.Fatalf("rescan with a truncated pack file = %+v", res)
			}
		}
		if got, _ := os.ReadFile(packFile); len(got) != len(whole)/2 {
			t.Fatal("truncated pack file was written over")
		}
		os.WriteFile(packFile, whole, 0o644)
		if res := f.rescan(RescanOptions{}); !res.Reloaded || res.Added != 0 {
			t.Fatalf("rescan after the rest arrived = %+v", res)
		}
		check(t, f)
	})
}

// caseSensitive reports whether dir's file system tells "a" from "A" and a
// composed from a decomposed character.
func caseSensitive(t *testing.T, dir string) bool {
	t.Helper()
	os.WriteFile(filepath.Join(dir, "probe-é"), nil, 0o644)
	_, err1 := os.Stat(filepath.Join(dir, "PROBE-é"))
	_, err2 := os.Stat(filepath.Join(dir, norm.NFD.String("probe-é")))
	os.Remove(filepath.Join(dir, "probe-é"))
	return err1 != nil && err2 != nil
}

func TestRescanAdoptsFilesUnderOtherSpellings(t *testing.T) {
	f := newFixture(t)
	if !caseSensitive(t, t.TempDir()) {
		t.Skip("file system folds case or Unicode forms; every spelling resolves")
	}
	src := t.TempDir()
	tones(t, filepath.Join(src, "Kit"), "hit", 2)
	f.importPaths(model.ImportTarget{}, filepath.Join(src, "Kit"))
	samples := filepath.Join(f.root, "Samples")

	// A file synced from a Mac with a decomposed name, one put into the
	// pack's folder spelled in another case, a whole new folder with a
	// decomposed name, and a known sample moved to the other-case folder.
	testaudio.Write(filepath.Join(samples, "Kit", norm.NFD.String("Café.wav")), testaudio.Tone(301, 0.1, 20))
	testaudio.Write(filepath.Join(samples, "kit", "lower.wav"), testaudio.Tone(302, 0.1, 20))
	testaudio.Write(filepath.Join(samples, norm.NFD.String("Été"), "new.wav"), testaudio.Tone(303, 0.1, 20))
	moved := f.ids("hit_01")[0]
	os.Rename(filepath.Join(samples, "Kit", "hit_01.wav"), filepath.Join(samples, "kit", "hit_01.wav"))

	res := f.rescan(RescanOptions{})
	if res.Added != 3 || res.Healed != 1 || res.Failed != 0 || res.Missing != 0 {
		t.Fatalf("rescan = %+v", res)
	}
	all := f.lib.Samples()
	if len(all) != 5 {
		t.Fatalf("%d samples, want 5", len(all))
	}
	for _, s := range all {
		abs, _ := f.lib.AbsPath(s.ID)
		if _, err := os.Stat(abs); err != nil {
			t.Errorf("%s points at a path that does not exist: %v", s.Name, err)
		}
		if !norm.NFC.IsNormalString(abs) {
			t.Errorf("%s: path %q is not NFC", s.Name, abs)
		}
	}
	if abs, _ := f.lib.AbsPath(moved); abs != filepath.Join(samples, "Kit", "hit_01.wav") {
		t.Errorf("healed sample is at %q", abs)
	}
	if _, err := os.Stat(filepath.Join(samples, "kit")); err == nil {
		t.Error("the emptied other-case folder was left behind")
	}
	// The library has settled: nothing to do, nothing missing.
	if res := f.rescan(RescanOptions{}); res != (RescanResult{}) {
		t.Errorf("second rescan = %+v", res)
	}
	f.expect("is:missing")

	// A record that points at a spelling that is not on disk is noticed
	// instead of being counted as present through its look-alike.
	gone := f.ids("lower")[0]
	os.MkdirAll(filepath.Join(samples, "KIT"), 0o755)
	os.Rename(filepath.Join(samples, "Kit", "lower.wav"), filepath.Join(samples, "KIT", "LOWER.wav"))
	if res := f.rescan(RescanOptions{}); res.Healed != 1 || res.Missing != 0 {
		t.Errorf("rescan after a case-only move = %+v", res)
	}
	if abs, _ := f.lib.AbsPath(gone); abs != filepath.Join(samples, "Kit", "LOWER.wav") {
		t.Errorf("sample moved by case only is at %q", abs)
	} else if _, err := os.Stat(abs); err != nil {
		t.Error(err)
	}
}

func TestRescanNoticesFileEditedInPlace(t *testing.T) {
	f := newFixture(t)
	src := t.TempDir()
	testaudio.Write(filepath.Join(src, "Kit", "pad.wav"), testaudio.Tone(220, 2, 20))
	testaudio.Write(filepath.Join(src, "Kit", "other.wav"), testaudio.Tone(330, 0.2, 20))
	f.importPaths(model.ImportTarget{Tag: "mine"}, filepath.Join(src, "Kit"))
	id := f.ids("pad")[0]
	ch, _ := f.lib.SetMeta([]string{id}, model.MetaPatch{BPM: new(97.0)})
	f.eng.Apply(ch)
	before, _ := f.lib.Sample(id)
	if res := f.rescan(RescanOptions{}); res != (RescanResult{}) {
		t.Fatalf("rescan of an untouched library = %+v", res)
	}

	// Trimmed in an audio editor and saved over the original.
	abs, _ := f.lib.AbsPath(id)
	if err := testaudio.Write(abs, testaudio.Tone(220, 0.5, 20)); err != nil {
		t.Fatal(err)
	}
	if res := f.rescan(RescanOptions{Quiet: true}); res.Changed != 1 || res.Added != 0 || res.Missing != 0 {
		t.Fatalf("rescan after the edit = %+v", res)
	}
	after, _ := f.lib.Sample(id)
	info, _ := os.Stat(abs)
	if after.Size != info.Size() || after.Size == before.Size {
		t.Errorf("size = %d, file is %d (was %d)", after.Size, info.Size(), before.Size)
	}
	if after.Dur < 0.45 || after.Dur > 0.55 || !after.Analyzed {
		t.Errorf("duration after re-analysis = %v (analysed %v), want 0.5", after.Dur, after.Analyzed)
	}
	if after.BPM != 97 || after.BPMSrc != model.SrcUser || !slices.Equal(after.Tags, []string{"mine"}) {
		t.Errorf("the user's values did not survive: %+v", after)
	}
	// Seen and dealt with: the next rescan reads nothing.
	if res := f.rescan(RescanOptions{Quiet: true}); res != (RescanResult{}) {
		t.Errorf("rescan after the change was taken in = %+v", res)
	}

	// A file that was only touched is read once and left as it is.
	later := time.Now().Add(time.Hour)
	otherAbs, _ := f.lib.AbsPath(f.ids("other")[0])
	os.Chtimes(otherAbs, later, later)
	if res := f.rescan(RescanOptions{Quiet: true}); res != (RescanResult{}) {
		t.Errorf("rescan after a touch = %+v", res)
	}

	// The verified versions are remembered across sessions.
	root, cache := f.root, f.cacheDir
	f.eng.Close()
	f.lib.Close()
	f.idx.Close()
	again := open(t, root, cache, 0)
	defer again.close()
	if res := again.rescan(RescanOptions{Quiet: true}); res != (RescanResult{}) {
		t.Errorf("rescan in the next session = %+v", res)
	}
	if len(again.progress) != 0 {
		t.Error("the next session read files it had already verified")
	}
}

func TestRescanLooseAndRemovedFiles(t *testing.T) {
	f := newFixture(t)
	src := t.TempDir()
	tones(t, filepath.Join(src, "Kit"), "hit", 2)
	f.importPaths(model.ImportTarget{}, filepath.Join(src, "Kit"))
	samples := filepath.Join(f.root, "Samples")

	// Files saved straight into Samples belong to the Unsorted pack.
	testaudio.Write(filepath.Join(samples, "loose.wav"), testaudio.Tone(410, 0.1, 20))
	if res := f.rescan(RescanOptions{Quiet: true}); res.Added != 1 || res.Failed != 0 {
		t.Fatalf("rescan with a loose file = %+v", res)
	}
	f.expect("pack:Unsorted", "loose")
	if _, err := os.Stat(filepath.Join(samples, "Unsorted", "loose.wav")); err != nil {
		t.Error("loose file was not moved into the Unsorted folder")
	}
	// A second one of the same name gets a free name next to it.
	testaudio.Write(filepath.Join(samples, "loose.wav"), testaudio.Tone(420, 0.1, 20))
	f.rescan(RescanOptions{Quiet: true})
	f.expect("pack:Unsorted", "loose", "loose (2)")

	// A removed sample put back by hand stays out, and the rescan says so.
	id := f.ids("hit_01")[0]
	abs, _ := f.lib.AbsPath(id)
	content, _ := os.ReadFile(abs)
	ch, _ := f.lib.RemoveSamples([]string{id})
	f.eng.Apply(ch)
	os.WriteFile(abs, content, 0o644)
	if res := f.rescan(RescanOptions{}); res.Ignored != 1 || res.Added != 0 {
		t.Fatalf("rescan with a removed file put back = %+v", res)
	}
	if p := f.progress[len(f.progress)-1]; p.Duplicates != 0 {
		t.Errorf("removed file reported as already in the library: %+v", p)
	}
}

func TestImportStopsWhenTheLibraryVanishes(t *testing.T) {
	f := newFixture(t)
	src := t.TempDir()
	tones(t, filepath.Join(src, "Kit"), "hit", 3)
	f.importPaths(model.ImportTarget{}, filepath.Join(src, "Kit"))
	if err := f.lib.Flush(); err != nil {
		t.Fatal(err)
	}

	// The drive is unplugged; its empty mount point stays.
	away := f.root + ".unmounted"
	os.Rename(f.root, away)
	os.Mkdir(f.root, 0o755)
	tones(t, filepath.Join(src, "More"), "late", 40)
	p := f.importPaths(model.ImportTarget{Tag: "x"}, filepath.Join(src, "More"))
	if p.Added != 0 || !p.Cancelled || len(p.Errors) != 1 {
		t.Errorf("import into a vanished library = %+v", p)
	}
	if entries, _ := os.ReadDir(f.root); len(entries) != 0 {
		t.Errorf("created under the empty mount point: %v", entries)
	}
	failures := f.forgive()
	if len(failures) == 0 || !errors.Is(failures[0], library.ErrUnavailable) {
		t.Errorf("failures = %v, want one saying the library is unavailable", failures)
	}
	if _, err := f.eng.Rescan(RescanOptions{Quiet: true}); !errors.Is(err, library.ErrUnavailable) {
		t.Errorf("rescan = %v", err)
	}
	if entries, _ := os.ReadDir(f.root); len(entries) != 0 {
		t.Errorf("rescan created files under the empty mount point: %v", entries)
	}
	os.Remove(f.root)
	os.Rename(away, f.root)
	f.forgive()
}

func TestImportFollowsLinkedFolder(t *testing.T) {
	f := newFixture(t)
	src := t.TempDir()
	tones(t, filepath.Join(src, "Real"), "hit", 3)
	tones(t, filepath.Join(src, "Elsewhere"), "far", 1)
	if err := os.Symlink(filepath.Join(src, "Real"), filepath.Join(src, "Link")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	// Inside the folder: a link to a file is imported, one to a folder is
	// reported instead of followed.
	os.Symlink(filepath.Join(src, "Elsewhere", "far_01.wav"), filepath.Join(src, "Real", "linked.wav"))
	os.Symlink(filepath.Join(src, "Elsewhere"), filepath.Join(src, "Real", "Sub"))
	os.Mkdir(filepath.Join(src, "Hollow"), 0o755)

	p := f.importPaths(model.ImportTarget{}, filepath.Join(src, "Link"), filepath.Join(src, "Hollow"))
	if p.Added != 4 || p.Failed != 2 || len(p.Errors) != 2 {
		t.Fatalf("import through a link = %+v", p)
	}
	f.expect("pack:Link", "hit_01", "hit_02", "hit_03", "linked")
	if !strings.Contains(strings.Join(p.Errors, "\n"), "Hollow: no audio files found") {
		t.Errorf("errors = %v", p.Errors)
	}
}

func TestStaleImportTempsAreSwept(t *testing.T) {
	f := newFixture(t)
	samples := filepath.Join(f.root, "Samples")
	old := time.Now().Add(-2 * staleTempAge)
	write := func(name string, when time.Time) string {
		p := filepath.Join(samples, name)
		os.WriteFile(p, []byte("partial copy"), 0o644)
		os.Chtimes(p, when, when)
		return p
	}
	stale := write("~$job_01ABC-7.part", old)
	fresh := write("~$job_01XYZ-1.part", time.Now())
	users := write("take.part", old)
	office := write("~$notes.docx", old)
	f.rescan(RescanOptions{Quiet: true})
	if _, err := os.Stat(stale); err == nil {
		t.Error("stale import temp file was not removed")
	}
	for _, keep := range []string{fresh, users, office} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("%s was removed", filepath.Base(keep))
		}
	}
}

func TestMergeDuringWriteReachesTheIndex(t *testing.T) {
	root, cache := filepath.Join(t.TempDir(), "Library"), t.TempDir()
	f := open(t, root, cache, time.Hour)
	src := t.TempDir()
	tones(t, filepath.Join(src, "Kit"), "hit", 3)
	f.importPaths(model.ImportTarget{}, filepath.Join(src, "Kit"))
	if err := f.lib.Flush(); err != nil {
		t.Fatal(err)
	}
	ids := f.ids("")

	// Another machine tags a sample; this one, not having rescanned,
	// favourites another in the same pack and writes.
	other, err := library.Open(root, library.Options{Debounce: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	other.AddTags(ids[:1], []string{"remote"})
	other.Close()
	ch, _ := f.lib.SetFavorite(ids[1:2], true)
	f.eng.Apply(ch)
	if err := f.lib.Flush(); err != nil {
		t.Fatal(err)
	}
	f.expect("tag:remote", "hit_01")
	f.expect("is:fav", "hit_02")

	// And the same when the write is the one at shutdown.
	other, _ = library.Open(root, library.Options{Debounce: time.Hour})
	other.AddTags(ids[2:], []string{"late"})
	other.Close()
	ch, _ = f.lib.SetFavorite(ids[1:2], false)
	f.eng.Apply(ch)
	f.close()
	again := open(t, root, cache, 0)
	defer again.close()
	again.expect("tag:late", "hit_03")
	again.expect("tag:remote", "hit_01")
	again.expect("is:fav")
}

func TestFailedIndexWriteIsRepaired(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the index's lock timeout")
	}
	root, cache := filepath.Join(t.TempDir(), "Library"), t.TempDir()
	f := open(t, root, cache, 0)
	src := t.TempDir()
	tones(t, filepath.Join(src, "Kit"), "hit", 3)
	f.importPaths(model.ImportTarget{}, filepath.Join(src, "Kit"))
	ids := f.ids("")

	// Another process holds the database: the write gives up.
	db, err := sql.Open("sqlite", IndexPath(cache))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	ch, _ := f.lib.AddTags(ids[:2], []string{"lost"})
	if err := f.eng.Apply(ch); err == nil {
		t.Fatal("index write succeeded against a locked database")
	}
	if _, err := db.Exec("ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	f.expect("tag:lost")

	// The session ends; the next one must find the tag.
	f.close()
	again := open(t, root, cache, 0)
	defer again.close()
	again.expect("tag:lost", "hit_01", "hit_02")
	if _, err := os.Stat(dirtyMarker(cache)); err == nil {
		t.Error("crash marker left after the repair")
	}
}
