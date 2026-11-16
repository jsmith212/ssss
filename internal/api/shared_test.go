package api

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ssss/internal/ingest"
	"ssss/internal/library"
	"ssss/internal/model"
	"ssss/internal/testaudio"
)

// librarySetUp returns a machine with a library holding the test sources.
func librarySetUp(t *testing.T, home string) (*machine, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Library")
	a := newMachine(t, home)
	t.Cleanup(a.shutdown)
	_, err := a.CreateLibrary(root, "Main")
	must(t, err)
	_, err = a.Import(writeSources(t, t.TempDir())[:1], model.ImportTarget{})
	must(t, err)
	a.settle()
	return a, root
}

func (m *machine) waitFor(what string, cond func() bool) {
	m.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			m.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestSecondCopyOfTheAppIsKeptOut(t *testing.T) {
	home := t.TempDir()
	a, root := librarySetUp(t, home)

	// A second process on the same machine starts with the same settings.
	b := newMachine(t, home)
	t.Cleanup(b.shutdown)
	st := b.GetState()
	if st.Status != model.StatusUnavailable || !strings.Contains(st.Message, "already open") {
		t.Fatalf("second copy's state = %+v", st)
	}
	if err := b.AddTags([]string{a.id("Kick_01")}, []string{"x"}); err == nil {
		t.Error("the second copy accepted a change")
	}
	if _, err := b.OpenLibrary(root); err == nil {
		t.Error("the second copy opened the library by hand")
	}
	// The first is untouched, and the second gets in once it has quit.
	must(t, a.AddTags([]string{a.id("Kick_01")}, []string{"mine"}))
	a.expect("tag:mine", "Kick_01")
	a.shutdown()
	if st := b.RetryLibrary(); st.Status != model.StatusOK {
		t.Fatalf("after the first copy quit: %+v", st)
	}
	b.settle()
	b.expect("tag:mine", "Kick_01")
}

func TestDamagedIndexIsRebuilt(t *testing.T) {
	home := t.TempDir()
	a, _ := librarySetUp(t, home)
	must(t, a.AddTags([]string{a.id("Kick_01")}, []string{"keep"}))
	all := a.names("")
	cache := a.current().cacheDir
	a.shutdown()

	// Everything after the first pages is junk: the header still reads.
	db := ingest.IndexPath(cache)
	data, err := os.ReadFile(db)
	must(t, err)
	if len(data) <= 8192 {
		t.Fatalf("index is only %d bytes", len(data))
	}
	for i := 8192; i < len(data); i++ {
		data[i] = 0x5a
	}
	must(t, os.WriteFile(db, data, 0o644))

	b := newMachine(t, home)
	t.Cleanup(b.shutdown)
	b.settle()
	if st := b.GetState(); st.Status != model.StatusOK {
		t.Fatalf("state = %+v", st)
	}
	b.expect("", all...)
	b.expect("tag:keep", "Kick_01")
	if errs := b.toasts(model.ToastError); len(errs) != 0 {
		t.Errorf("error toasts: %v", errs)
	}

	// Rebuild Cache works on a damaged file too, while the app is running.
	b.current().idx.Close()
	must(t, os.WriteFile(db, data, 0o644))
	b.clearEvents()
	must(t, b.RebuildCache())
	b.waitFor("the rebuild", func() bool { return len(b.toasts(model.ToastInfo)) > 0 })
	b.settle()
	b.expect("", all...)
	b.expect("tag:keep", "Kick_01")
	if got := b.toasts(model.ToastInfo); len(got) != 1 || got[0] != "Cache rebuilt" {
		t.Errorf("info toasts = %v", got)
	}
}

func TestUnwritableCacheDoesNotHideTheLibrary(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	home := t.TempDir()
	a, _ := librarySetUp(t, home)
	all := a.names("")
	cache := a.current().cacheDir
	a.shutdown()
	must(t, os.Chmod(cache, 0o555))
	defer os.Chmod(cache, 0o755)

	b := newMachine(t, home)
	t.Cleanup(b.shutdown)
	b.settle()
	if st := b.GetState(); st.Status != model.StatusOK {
		t.Fatalf("state with a read-only cache folder = %+v", st)
	}
	b.expect("", all...)
	must(t, b.AddTags([]string{b.id("Kick_01")}, []string{"works"}))
	b.expect("tag:works", "Kick_01")
	if errs := b.toasts(model.ToastError); len(errs) != 1 || !strings.Contains(errs[0], "temporary") {
		t.Errorf("error toasts = %v, want the one about the temporary cache", errs)
	}
	temp := b.current().cacheDir
	b.shutdown()
	if exists(temp) {
		t.Error("temporary cache folder left behind")
	}
}

func TestStartupToastsWaitForTheFrontend(t *testing.T) {
	home := t.TempDir()
	a, root := librarySetUp(t, home)
	a.shutdown()
	must(t, os.WriteFile(filepath.Join(root, "_library", "packs", "pk_HALF.json"), []byte(`{"v":1,"id":"pk_HALF","samples":{`), 0o644))

	t.Setenv(library.EnvConfigDir, filepath.Join(home, "config"))
	t.Setenv(library.EnvCacheDir, filepath.Join(home, "cache"))
	b := &machine{t: t, API: New()}
	b.sink = func(name string, data any) {
		b.mu.Lock()
		b.events = append(b.events, event{name, data})
		b.mu.Unlock()
	}
	t.Cleanup(b.shutdown)
	// As main does: before the window exists.
	Start(b.API)
	b.settle()
	if got := b.toasts(model.ToastError); len(got) != 0 {
		t.Fatalf("toasts emitted into the void: %v", got)
	}
	b.GetState()
	got := b.toasts(model.ToastError)
	if len(got) != 1 || !strings.Contains(got[0], "pk_HALF.json") {
		t.Errorf("toasts after the first GetState = %v", got)
	}
	// From here on toasts go straight out.
	b.toast(model.ToastInfo, "now")
	if got := b.toasts(model.ToastInfo); len(got) != 1 {
		t.Errorf("toast after startup was held back: %v", got)
	}
}

func TestLibraryFolderVanishingMidSession(t *testing.T) {
	a, root := librarySetUp(t, t.TempDir())
	must(t, a.current().lib.Flush())
	id := a.id("Kick_01")

	// Unplugged: the mount point stays, empty.
	away := root + ".unmounted"
	must(t, os.Rename(root, away))
	must(t, os.Mkdir(root, 0o755))
	// The edit is accepted (nothing has noticed yet), cannot be saved, and
	// that failure is what switches the session over.
	must(t, a.AddTags([]string{id}, []string{"late"}))
	a.waitFor("the session to become unavailable", func() bool {
		return a.GetState().Status == model.StatusUnavailable
	})
	a.settle()
	if err := a.AddTags([]string{id}, []string{"later"}); err == nil {
		t.Error("change accepted with the library folder gone")
	}
	if _, err := a.Import(writeSources(t, t.TempDir())[1:2], model.ImportTarget{}); err == nil {
		t.Error("import accepted with the library folder gone")
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("created under the empty mount point: %v", entries)
	}
	if len(a.toasts(model.ToastError)) == 0 {
		t.Error("the user was not told")
	}
	must(t, os.Remove(root))
	must(t, os.Rename(away, root))
	if st := a.RetryLibrary(); st.Status != model.StatusOK {
		t.Fatalf("after the folder came back: %+v", st)
	}
}

func TestOpeningTheOpenLibraryAgainIsSerialised(t *testing.T) {
	a, root := librarySetUp(t, t.TempDir())
	a.clearEvents()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	// What the focus timer and the Retry button do while the folder dialog
	// of "Locate library" closes.
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				a.RetryLibrary()
			}
		}
	})
	for range 15 {
		if _, err := a.OpenLibrary(root); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	a.settle()
	if st := a.GetState(); st.Status != model.StatusOK {
		t.Fatalf("state = %+v", st)
	}
	// A second open of the folder would have run into the cache lock or
	// into the other's index.
	if errs := a.toasts(model.ToastError); len(errs) != 0 {
		t.Errorf("error toasts: %v", errs)
	}
	a.expect("", "Bass_Loop_140_Am", "Drum_Loop_120bpm", "Kick_01", "Snare_01")
}

func TestSelfDropOnReadOnlyLibraryIsSilent(t *testing.T) {
	home := t.TempDir()
	a, root := librarySetUp(t, home)
	own := []string{a.detail(a.id("Kick_01")).AbsPath}
	a.shutdown()
	// A file from a newer version makes the library read-only.
	must(t, os.WriteFile(filepath.Join(root, "_library", "packs", "pk_NEW.json"),
		[]byte(`{"v":99,"id":"pk_NEW","name":"New","dir":"New","samples":{}}`), 0o644))
	b := newMachine(t, home)
	t.Cleanup(b.shutdown)
	b.settle()
	if st := b.GetState(); st.Status != model.StatusReadOnly {
		t.Fatalf("state = %+v", st)
	}
	b.clearEvents()
	// Letting go of a drag over the list is how a drag is abandoned.
	b.handleFileDrop(own, DropTarget{Kind: "list"})
	b.handleFileDrop(nil, DropTarget{Kind: "list"})
	if n := len(b.eventsNamed(model.EventToast)) + len(b.eventsNamed(model.EventDrop)); n != 0 {
		t.Errorf("abandoned drag on a read-only library caused %d events", n)
	}
	// Foreign files, and assigning a tag, are still refused out loud.
	b.handleFileDrop(writeSources(t, t.TempDir())[1:2], DropTarget{Kind: "list"})
	b.handleFileDrop(own, DropTarget{Kind: "tag", ID: "x"})
	if got := b.toasts(model.ToastError); len(got) != 2 {
		t.Errorf("toasts = %v, want two refusals", got)
	}
}

func TestRescanSummary(t *testing.T) {
	tests := []struct {
		res  ingest.RescanResult
		want string
	}{
		{ingest.RescanResult{}, "Library is up to date"},
		{ingest.RescanResult{Added: 2, Changed: 1}, "Rescan: 2 added, 1 changed on disk"},
		{ingest.RescanResult{Failed: 1}, "Rescan: 1 could not be added"},
		{ingest.RescanResult{Ignored: 1}, "Rescan: 1 previously removed, ignored"},
		{ingest.RescanResult{Waiting: 3}, "Rescan: 3 left until their pack file has synced"},
	}
	for _, tt := range tests {
		if got := rescanSummary(tt.res); got != tt.want {
			t.Errorf("rescanSummary(%+v) = %q, want %q", tt.res, got, tt.want)
		}
	}
}

// What analysis finds for a one-shot is a root note; the row says so, key
// search treats it as fitting either mode, and a key someone states takes
// the flag away again.
func TestDetectedRootNoteIsMarked(t *testing.T) {
	a := newMachine(t, t.TempDir())
	t.Cleanup(a.shutdown)
	_, err := a.CreateLibrary(filepath.Join(t.TempDir(), "Library"), "Main")
	must(t, err)
	src := t.TempDir()
	must(t, testaudio.Write(filepath.Join(src, "Sound_07.wav"), testaudio.Tone(220, 1.2, 3)))
	must(t, testaudio.Write(filepath.Join(src, "Stab_Am.wav"), testaudio.Tone(220, 1.2, 3.5)))
	_, err = a.Import([]string{src}, model.ImportTarget{})
	must(t, err)
	a.settle()

	row := func(name string) model.Sample {
		t.Helper()
		for _, s := range a.Search(model.SearchRequest{Limit: 50}).Rows {
			if s.Name == name {
				return s
			}
		}
		t.Fatalf("no row %s", name)
		return model.Sample{}
	}
	if s := row("Sound_07"); s.Key != "A" || s.KeySrc != model.SrcDetected || !s.KeyRoot || s.Type != model.TypeOneShot {
		t.Fatalf("detected one-shot: key=%q src=%q root=%v type=%q", s.Key, s.KeySrc, s.KeyRoot, s.Type)
	}
	if s := row("Stab_Am"); s.Key != "Am" || s.KeySrc != model.SrcFilename || s.KeyRoot {
		t.Fatalf("named key: key=%q src=%q root=%v", s.Key, s.KeySrc, s.KeyRoot)
	}
	a.expect("key:Am", "Sound_07", "Stab_Am")
	a.expect("key:A", "Sound_07")
	a.expect("key:C")
	if d := a.detail(a.id("Sound_07")); !d.KeyRoot {
		t.Error("detail lost the root flag")
	}

	// A stated key is a key, and undoing it brings the root back.
	key := "A"
	must(t, a.SetMeta([]string{a.id("Sound_07")}, model.MetaPatch{Key: &key}))
	if s := row("Sound_07"); s.Key != "A" || s.KeySrc != model.SrcUser || s.KeyRoot {
		t.Fatalf("user key: key=%q src=%q root=%v", s.Key, s.KeySrc, s.KeyRoot)
	}
	a.expect("key:Am", "Stab_Am")
	a.Undo()
	if s := row("Sound_07"); !s.KeyRoot || s.KeySrc != model.SrcDetected {
		t.Fatalf("after undo: key=%q src=%q root=%v", s.Key, s.KeySrc, s.KeyRoot)
	}

	// The flag is in the library, not only in this machine's cache.
	a.clearEvents()
	must(t, a.RebuildCache())
	a.waitFor("the rebuild", func() bool { return len(a.toasts(model.ToastInfo)) > 0 })
	a.settle()
	if s := row("Sound_07"); !s.KeyRoot {
		t.Error("root flag lost with the cache")
	}
	// A rebuilt cache has no import batch; Recent falls back to the
	// newest run of additions.
	if sb := a.GetSidebar(); sb.Recent != 2 {
		t.Errorf("recent after a rebuild = %d, want 2", sb.Recent)
	}
	a.expect("is:recent", "Sound_07", "Stab_Am")
}
