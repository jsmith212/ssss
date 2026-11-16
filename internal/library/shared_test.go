package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ssss/internal/model"
)

// These tests cover a library folder that is not ours alone: a sync client
// delivers another machine's files, takes files away, or the whole folder
// goes.

func errOf3(_ model.SavedView, _ Change, err error) error { return err }

// otherMachine opens the same folder a second time, edits and closes, which
// leaves the files exactly as a sync client delivering that machine's work
// would.
func otherMachine(t *testing.T, root string, edit func(l *Library)) {
	t.Helper()
	other := must(Open(root, Options{Debounce: time.Hour}))
	edit(other)
	noErr(t, other.Close())
}

func TestWriteMergesAnotherMachinesEdits(t *testing.T) {
	for _, via := range []string{"Flush", "Reload"} {
		t.Run(via, func(t *testing.T) {
			l := must(Create(filepath.Join(t.TempDir(), "lib"), "Main", Options{Debounce: time.Hour}))
			defer l.Close()
			var told atomic.Int32
			l.OnExternalChange(func(ch Change) {
				if ch.All {
					told.Add(1)
				}
			})
			pk := newPack(t, l, "Pack")
			a, b := addSample(t, l, pk, "a.wav"), addSample(t, l, pk, "b.wav")
			col, _, _ := l.CreateCollection("Set")
			noErr(t, l.Flush())

			var theirs string
			otherMachine(t, l.Root(), func(o *Library) {
				must(o.AddTags([]string{a}, []string{"from-a"}))
				must(o.AddToCollection(col.ID, []string{a}))
				must(o.SetTagColor("from-a", "#abc"))
				theirs = addSample(t, o, pk, "theirs.wav")
			})

			// No rescan has run: this machine still holds the old pack, and
			// now changes another sample in it.
			must(l.SetFavorite([]string{b}, true))
			must(l.AddToCollection(col.ID, []string{b}))
			must(l.SetTagColor("mine", "#123"))
			if via == "Flush" {
				noErr(t, l.Flush())
				if told.Load() != 1 {
					t.Errorf("listener told %d times, want 1", told.Load())
				}
			} else {
				ch, warnings, err := l.Reload()
				if err != nil || !ch.All || len(warnings) != 0 {
					t.Fatalf("Reload = %+v, %v, %v", ch, warnings, err)
				}
			}

			check := func(l *Library, where string) {
				t.Helper()
				if got := tagsOf(t, l, a); !slices.Equal(got, []string{"from-a"}) {
					t.Errorf("%s: other machine's tag = %v", where, got)
				}
				if s, _ := l.Sample(b); !s.Fav {
					t.Errorf("%s: local favourite lost", where)
				}
				if !l.Has(theirs) {
					t.Errorf("%s: sample imported on the other machine lost", where)
				}
				if items, _ := l.CollectionItems(col.ID); !slices.Contains(items, a) || !slices.Contains(items, b) {
					t.Errorf("%s: collection = %v", where, items)
				}
				if got := l.TagColors(); got["from-a"] != "#abc" || got["mine"] != "#123" {
					t.Errorf("%s: tag colours = %v", where, got)
				}
			}
			check(l, "memory")
			check(reopen(t, l), "disk")
		})
	}
}

func TestReloadKeepsMutationsMadeMeanwhile(t *testing.T) {
	l := must(Create(filepath.Join(t.TempDir(), "lib"), "Main", Options{Debounce: time.Millisecond}))
	defer l.Close()
	var ids []string
	for p := range 6 {
		pk := newPack(t, l, "Pack "+string(rune('A'+p)))
		for i := range 150 {
			ids = append(ids, addSample(t, l, pk, "s"+string(rune('a'+p))+"-"+strings.Repeat("x", i)+".wav"))
		}
	}
	noErr(t, l.Flush())

	// A steady stream of small mutations, as an import or analysis makes,
	// while again and again another machine's collection arrives and the
	// library is reloaded.
	done := make(chan struct{})
	var acked []string
	go func() {
		defer close(done)
		for _, id := range ids {
			if _, err := l.SetFavorite([]string{id}, true); err == nil {
				acked = append(acked, id)
			}
		}
	}()
	rounds := 0
	for running := true; running || rounds < 8; rounds++ {
		select {
		case <-done:
			running = false
		default:
		}
		id := fmt.Sprintf("col_EXT%04d", rounds)
		name := filepath.Join(l.Root(), "_library/collections", id+".json")
		noErr(t, os.WriteFile(name, []byte(`{"v":1,"id":"`+id+`","name":"Theirs","items":[]}`), 0o644))
		if _, _, err := l.Reload(); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	if len(acked) != len(ids) {
		t.Fatalf("%d of %d mutations accepted", len(acked), len(ids))
	}
	lost := func(l *Library) (n int) {
		for _, id := range acked {
			if s, _ := l.Sample(id); !s.Fav {
				n++
			}
		}
		return n
	}
	if n := lost(l); n > 0 {
		t.Errorf("%d of %d acknowledged edits lost from memory", n, len(acked))
	}
	if n := lost(reopen(t, l)); n > 0 {
		t.Errorf("%d of %d acknowledged edits lost from disk", n, len(acked))
	}
	if got := len(l.Collections()); got != rounds {
		t.Errorf("collections = %d, want the %d that arrived", got, rounds)
	}
}

func TestUnreadableFilesAreNeverOverwritten(t *testing.T) {
	l := newLib(t)
	pk := newPack(t, l, "Pack")
	a := addSample(t, l, pk, "a.wav")
	noErr(t, errOf3(l.SaveView("one", "x")))
	noErr(t, errOf3(l.SaveView("two", "y")))
	noErr(t, l.Flush())
	meta := filepath.Join(l.Root(), "_library")
	packFile := filepath.Join(meta, "packs", pk+".json")
	viewsFile := filepath.Join(meta, "views.json")
	goodPack, goodViews := must(os.ReadFile(packFile)), must(os.ReadFile(viewsFile))

	// Both files arrive half written.
	noErr(t, os.WriteFile(packFile, goodPack[:len(goodPack)/2], 0o644))
	noErr(t, os.WriteFile(viewsFile, goodViews[:len(goodViews)-10], 0o644))
	_, warnings, err := l.Reload()
	if err != nil || len(warnings) != 2 {
		t.Fatalf("Reload = %v, %v", warnings, err)
	}
	if got := l.Unreadable(); !slices.Equal(got, []string{"packs/" + pk + ".json", "views.json"}) {
		t.Errorf("unreadable = %v", got)
	}
	if !l.Has(a) {
		t.Error("records dropped because their pack file is unreadable")
	}
	// A change to the pack stays in memory; one to the views is refused.
	must(l.SetFavorite([]string{a}, true))
	if err := l.Flush(); err == nil {
		t.Error("flush over an unreadable pack file reported success")
	}
	if _, _, err := l.SaveView("three", "z"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("SaveView = %v", err)
	}
	if got := must(os.ReadFile(packFile)); len(got) != len(goodPack)/2 {
		t.Error("unreadable pack file was written over")
	}
	if got := must(os.ReadFile(viewsFile)); len(got) != len(goodViews)-10 {
		t.Error("unreadable views file was written over")
	}

	// The rest arrives: everything is merged and writable again.
	noErr(t, os.WriteFile(packFile, goodPack, 0o644))
	noErr(t, os.WriteFile(viewsFile, goodViews, 0o644))
	if _, warnings, err := l.Reload(); err != nil || len(warnings) != 0 {
		t.Fatalf("Reload = %v, %v", warnings, err)
	}
	noErr(t, errOf3(l.SaveView("three", "z")))
	l = reopen(t, l)
	if s, _ := l.Sample(a); !s.Fav {
		t.Error("edit made while the file was unreadable lost")
	}
	if len(l.Views()) != 3 {
		t.Errorf("views = %v", l.Views())
	}

	// The same at startup: an unparsable views.json is not replaced.
	noErr(t, l.Close())
	noErr(t, os.WriteFile(viewsFile, goodViews[:len(goodViews)-10], 0o644))
	l = must(Open(l.Root(), Options{}))
	defer l.Close()
	if _, _, err := l.SaveView("four", "q"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("SaveView after reopen = %v", err)
	}
	if _, err := l.DeleteView("vw_x"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("DeleteView after reopen = %v", err)
	}
	noErr(t, l.Flush())
	if got := must(os.ReadFile(viewsFile)); len(got) != len(goodViews)-10 {
		t.Error("unparsable views.json was written over after reopen")
	}
}

func TestVanishedRootIsNotRecreated(t *testing.T) {
	var reported atomic.Pointer[error]
	l := must(Create(filepath.Join(t.TempDir(), "lib"), "Main", Options{
		Debounce: time.Millisecond, RetryDelay: time.Hour,
		OnWriteError: func(err error) { reported.Store(&err) },
	}))
	defer l.Close()
	pk := newPack(t, l, "Pack")
	a := addSample(t, l, pk, "a.wav")
	noErr(t, l.Flush())

	// The drive is unplugged: the folder is gone and an empty mount point
	// is left in its place.
	root := l.Root()
	noErr(t, os.Rename(root, root+".unmounted"))
	noErr(t, os.Mkdir(root, 0o755))
	if err := l.Available(); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Available = %v", err)
	}

	must(l.AddTags([]string{a}, []string{"late"}))
	deadline := time.Now().Add(5 * time.Second)
	for reported.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := reported.Load(); err == nil || !errors.Is(*err, ErrUnavailable) {
		t.Errorf("background write reported %v, want ErrUnavailable", err)
	}
	if err := l.Flush(); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Flush = %v", err)
	}
	if _, _, err := l.Reload(); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Reload = %v", err)
	}
	_, err := l.AddSample(pk, sampleID("new"), SampleRecord{Path: "new.wav"}, func(abs string) error {
		return os.WriteFile(abs, []byte("new"), 0o644)
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("AddSample = %v", err)
	}
	if _, err := l.RemoveSamples([]string{a}); err != nil {
		t.Errorf("removing a sample whose file is out of reach: %v", err)
	}
	if entries := must(os.ReadDir(root)); len(entries) != 0 {
		t.Errorf("created under the empty mount point: %v", entries)
	}

	// Another library moving into the path is not ours to write to either.
	noErr(t, os.Remove(root))
	stranger := must(Create(root, "Stranger", Options{}))
	noErr(t, stranger.Close())
	before := must(os.ReadFile(filepath.Join(root, "_library/library.json")))
	if err := l.Flush(); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Flush into another library = %v", err)
	}
	if entries := must(os.ReadDir(filepath.Join(root, "_library/packs"))); len(entries) != 0 {
		t.Errorf("wrote into another library: %v", entries)
	}
	if got := must(os.ReadFile(filepath.Join(root, "_library/library.json"))); string(got) != string(before) {
		t.Error("another library's library.json was rewritten")
	}

	// A sync client removing just the packs folder is still survivable.
	noErr(t, os.RemoveAll(root))
	noErr(t, os.Rename(root+".unmounted", root))
	noErr(t, os.RemoveAll(filepath.Join(root, "_library/packs")))
	noErr(t, l.Flush())
	if !exists(filepath.Join(root, "_library/packs", pk+".json")) {
		t.Error("pack file not rewritten after its folder vanished")
	}
}

func TestFailedWriteIsRetriedOnItsOwn(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	var reports atomic.Int32
	l := must(Create(filepath.Join(t.TempDir(), "lib"), "Main", Options{
		Debounce: time.Millisecond, RetryDelay: 5 * time.Millisecond,
		OnWriteError: func(error) { reports.Add(1) },
	}))
	defer l.Close()
	pk := newPack(t, l, "Pack")
	a := addSample(t, l, pk, "a.wav")
	noErr(t, l.Flush())

	packs := filepath.Join(l.Root(), "_library/packs")
	noErr(t, os.Chmod(packs, 0o555))
	defer os.Chmod(packs, 0o755)
	must(l.SetFavorite([]string{a}, true))
	deadline := time.Now().Add(5 * time.Second)
	for reports.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	// Several retries fail before the folder becomes writable again.
	time.Sleep(60 * time.Millisecond)
	noErr(t, os.Chmod(packs, 0o755))

	// No further mutation, no Flush: the retry alone must get it to disk.
	onDisk := func() bool {
		data, err := os.ReadFile(filepath.Join(packs, pk+".json"))
		return err == nil && strings.Contains(string(data), `"fav":true`)
	}
	for !onDisk() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !onDisk() {
		t.Error("the change never reached the disk after the folder became writable")
	}
	if n := reports.Load(); n != 1 {
		t.Errorf("failure reported %d times, want once per streak", n)
	}
}

func TestPacksSharingAFolderAreFolded(t *testing.T) {
	l := newLib(t)
	real := newPack(t, l, "Kit")
	a, b := addSample(t, l, real, "a.wav"), addSample(t, l, real, "b.wav")
	must(l.AddTags([]string{a}, []string{"kept"}))
	noErr(t, l.Close())
	meta := filepath.Join(l.Root(), "_library")
	realFile := filepath.Join(meta, "packs", real+".json")
	aside := must(os.ReadFile(realFile))
	noErr(t, os.Remove(realFile))

	// Without its pack file the folder looks unclaimed; a rescan adopts the
	// audio into a pack of its own, and the user tags one of the samples.
	l = must(Open(l.Root(), Options{Debounce: 5 * time.Millisecond}))
	t.Cleanup(func() { l.Close() })
	ghost, _, err := l.CreatePack("Kit", "Kit", SourceScan)
	noErr(t, err)
	must(l.AdoptSample(ghost.ID, a, SampleRecord{Path: "a.wav"}))
	must(l.AdoptSample(ghost.ID, b, SampleRecord{Path: "b.wav"}))
	must(l.ApplyAnalysis(a, Analysis{Dur: 1}))
	extra := sampleID("extra")
	noErr(t, os.WriteFile(filepath.Join(l.SamplesDir(), "Kit", "extra.wav"), []byte("extra"), 0o644))
	must(l.AdoptSample(ghost.ID, extra, SampleRecord{Path: "extra.wav"}))
	must(l.AddTags([]string{b}, []string{"tagged-meanwhile"}))
	noErr(t, l.Flush())

	// The real pack file arrives.
	noErr(t, os.WriteFile(realFile, aside, 0o644))
	if ch, warnings, err := l.Reload(); err != nil || !ch.All || len(warnings) != 0 {
		t.Fatalf("Reload = %+v, %v, %v", ch, warnings, err)
	}
	check := func(l *Library, where string) {
		t.Helper()
		packs := l.Packs()
		if len(packs) != 1 || packs[0].ID != real || packs[0].Count != 3 {
			t.Fatalf("%s: packs = %+v", where, packs)
		}
		if got := tagsOf(t, l, a); !slices.Equal(got, []string{"kept"}) {
			t.Errorf("%s: the real record's tags = %v", where, got)
		}
		if got := tagsOf(t, l, b); !slices.Equal(got, []string{"tagged-meanwhile"}) {
			t.Errorf("%s: tags given to the adopted record = %v", where, got)
		}
		if !l.Has(extra) {
			t.Errorf("%s: sample only the adopted pack knew is gone", where)
		}
	}
	check(l, "memory")
	if entries := must(os.ReadDir(filepath.Join(meta, "packs"))); len(entries) != 1 {
		t.Errorf("pack files after the fold: %v", entries)
	}

	// A removal sticks, and undo brings the sample back.
	abs, _ := l.AbsPath(a)
	ch := must(l.RemoveSamples([]string{a}))
	if l.Has(a) || exists(abs) || !l.Tombstoned(a) {
		t.Error("removed sample is still in the library")
	}
	must(ch.Undo())
	if !l.Has(a) || !exists(abs) {
		t.Error("undo did not restore the removed sample")
	}
	check(reopen(t, l), "disk")
}

func TestRemoveReachesEveryPackListingTheSample(t *testing.T) {
	root := writeHandLibrary(t)
	// A second pack file, in another folder, lists the same content.
	dup := strings.NewReplacer(`"pk_HAND"`, `"pk_ZDUP"`, `"dir":"Hand"`, `"dir":"Elsewhere"`).Replace(handWrittenPack)
	if dup == handWrittenPack || !strings.Contains(dup, "Elsewhere") {
		t.Fatal("fixture: could not derive the second pack")
	}
	noErr(t, os.WriteFile(filepath.Join(root, "_library/packs/pk_ZDUP.json"), []byte(dup), 0o644))
	l := must(Open(root, Options{}))
	defer l.Close()
	if len(l.Packs()) != 2 {
		t.Fatalf("packs = %+v", l.Packs())
	}
	must(l.RemoveSamples([]string{handID}))
	if l.Has(handID) || !l.Tombstoned(handID) {
		t.Error("sample survived its removal through the second pack")
	}
}

func TestUndoRevertsOnlyItsOwnChange(t *testing.T) {
	l := newLib(t)
	pk := newPack(t, l, "Pack")
	a, b := addSample(t, l, pk, "a.wav"), addSample(t, l, pk, "b.wav")
	col, _, _ := l.CreateCollection("Set")
	noErr(t, l.Flush())

	addItem := must(l.AddToCollection(col.ID, []string{a}))
	addTag := must(l.AddTags([]string{a}, []string{"mine"}))
	rename := must(l.RenameCollection(col.ID, "Mine"))
	saved, saveView, err := l.SaveView("mine", "q")
	noErr(t, err)
	bpm := must(l.SetMeta([]string{a}, model.MetaPatch{BPM: ptr(100.0)}))
	fav := must(l.SetFavorite([]string{b}, true))
	untag := must(l.RemoveTags([]string{a}, []string{"mine"}))
	must(untag.Undo())

	// Meanwhile: an import's drop target, analysis, and another machine.
	must(l.AddToCollection(col.ID, []string{b}))
	must(l.AddTags([]string{a}, []string{"theirs"}))
	must(l.ApplyAnalysis(a, Analysis{Detected: &Detected{BPM: 128}, BPM: 128, BPMSrc: model.SrcDetected}))
	noErr(t, l.Flush())
	otherMachine(t, l.Root(), func(o *Library) {
		must(o.RenameCollection(col.ID, "Theirs"))
		noErr(t, errOf3(o.SaveView("theirs", "t")))
		must(o.SetFavorite([]string{b}, false))
	})
	if _, _, err := l.Reload(); err != nil {
		t.Fatal(err)
	}

	for _, ch := range []Change{fav, bpm, saveView, rename, addTag, addItem} {
		must(ch.Undo())
	}
	if items, _ := l.CollectionItems(col.ID); !slices.Equal(items, []string{b}) {
		t.Errorf("collection after undo = %v, want only the sample added since", items)
	}
	if got := tagsOf(t, l, a); !slices.Equal(got, []string{"theirs"}) {
		t.Errorf("tags after undo = %v", got)
	}
	if got := l.Collections()[0].Name; got != "Theirs" {
		t.Errorf("collection name after undo = %q, want the other machine's rename", got)
	}
	if views := l.Views(); len(views) != 1 || views[0].Name != "theirs" || views[0].ID == saved.ID {
		t.Errorf("views after undo = %v", views)
	}
	// Taking the user's tempo away leaves what analysis found, not nothing.
	if s, _ := l.Sample(a); s.BPM != 128 || s.BPMSrc != model.SrcDetected {
		t.Errorf("tempo after undo = %v (%s), want the detected 128", s.BPM, s.BPMSrc)
	}
	// The favourite was already taken off elsewhere; undo has nothing to do.
	if s, _ := l.Sample(b); s.Fav {
		t.Error("undo of a favourite turned it back on")
	}
}

func TestAnalysisDoesNotOutrankAnEdit(t *testing.T) {
	older, newer := at(1), at(5)
	edited := &SampleRecord{Path: "a.wav", Tags: []string{"kick"}, Rev: newer}
	analysed := &SampleRecord{Path: "a.wav", Analyzed: true, Rev: older}
	dst := &pack{Samples: map[string]*SampleRecord{"a": analysed}, Removed: map[string]time.Time{}}
	mergePack(dst, &pack{Samples: map[string]*SampleRecord{"a": edited}})
	if dst.Samples["a"] != edited {
		t.Error("the newer edit lost against an analysed record")
	}
	// Same revision: the analysed one is the more complete.
	bare := &SampleRecord{Path: "a.wav", Rev: older}
	dst = &pack{Samples: map[string]*SampleRecord{"a": bare}, Removed: map[string]time.Time{}}
	mergePack(dst, &pack{Samples: map[string]*SampleRecord{"a": analysed}})
	if dst.Samples["a"] != analysed {
		t.Error("analysis results dropped in a merge of equal revisions")
	}
	// A record without a revision (adopted by a rescan) is still a record.
	dst = &pack{Samples: map[string]*SampleRecord{}, Removed: map[string]time.Time{}}
	mergePack(dst, &pack{Samples: map[string]*SampleRecord{"a": {Path: "a.wav"}}})
	if dst.Samples["a"] == nil {
		t.Error("record without a revision dropped by the merge")
	}

	l := newLib(t)
	pk := newPack(t, l, "Pack")
	id := addSample(t, l, pk, "a.wav")
	_, before, _ := l.Record(id)
	must(l.ApplyAnalysis(id, Analysis{Dur: 2}))
	if _, after, _ := l.Record(id); !after.Rev.Equal(before.Rev) || !after.Analyzed {
		t.Errorf("analysis changed the revision: %v -> %v", before.Rev, after.Rev)
	}
}
