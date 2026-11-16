package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"ssss/internal/model"
)

func ptr[T any](v T) *T { return &v }

// undoRedo runs ch.Undo, checks with afterUndo, redoes, and returns the
// redo's Change so the caller can check the redone state.
func undoRedo(t *testing.T, ch Change, afterUndo func()) Change {
	t.Helper()
	if ch.Undo == nil {
		t.Fatal("change is not undoable")
	}
	undone := must(ch.Undo())
	afterUndo()
	if undone.Undo == nil {
		t.Fatal("undo is not redoable")
	}
	return must(undone.Undo())
}

func TestTagMutations(t *testing.T) {
	l := newLib(t)
	pk := newPack(t, l, "Pack")
	a, b := addSample(t, l, pk, "a.wav"), addSample(t, l, pk, "b.wav")

	ch := must(l.AddTags([]string{a, b, "unknown"}, []string{"Drums/Kick", "drums/kick", " Loud "}))
	if !slices.Equal(ch.IDs, slices.Sorted(slices.Values([]string{a, b}))) || !ch.Structural {
		t.Errorf("change = %+v", ch)
	}
	if got := tagsOf(t, l, a); !slices.Equal(got, []string{"drums/kick", "loud"}) {
		t.Errorf("tags = %v", got)
	}
	// Adding what is already there changes nothing and offers no undo.
	if again := must(l.AddTags([]string{a}, []string{"loud"})); !again.Empty() || again.Undo != nil {
		t.Errorf("no-op change = %+v", again)
	}
	if _, err := l.AddTags([]string{a}, []string{" / "}); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty tag: %v", err)
	}
	undoRedo(t, ch, func() {
		if got := tagsOf(t, l, a); len(got) != 0 {
			t.Errorf("after undo: %v", got)
		}
	})
	if got := tagsOf(t, l, b); !slices.Equal(got, []string{"drums/kick", "loud"}) {
		t.Errorf("after redo: %v", got)
	}

	ch = must(l.RemoveTags([]string{a}, []string{"DRUMS/KICK", "absent"}))
	if got := tagsOf(t, l, a); !slices.Equal(got, []string{"loud"}) {
		t.Errorf("after remove: %v", got)
	}
	must(ch.Undo())

	// Rename moves descendants, merges with an existing target and takes
	// the decoration along.
	must(l.AddTags([]string{a}, []string{"drums", "perc/kick"}))
	must(l.SetTagColor("drums/kick", "#e66"))
	must(l.SetTagColor("drums", "#111"))
	ch = must(l.RenameTag("Drums", "perc"))
	if got := tagsOf(t, l, a); !slices.Equal(got, []string{"perc/kick", "loud", "perc"}) {
		t.Errorf("after rename a: %v", got)
	}
	if got := tagsOf(t, l, b); !slices.Equal(got, []string{"perc/kick", "loud"}) {
		t.Errorf("after rename b: %v", got)
	}
	wantColors := map[string]string{"perc/kick": "#e66", "perc": "#111"}
	if got := l.TagColors(); fmt.Sprint(got) != fmt.Sprint(wantColors) {
		t.Errorf("colours = %v", got)
	}
	undoRedo(t, ch, func() {
		if got := tagsOf(t, l, a); !slices.Equal(got, []string{"drums/kick", "loud", "drums", "perc/kick"}) {
			t.Errorf("rename undone a: %v", got)
		}
		if got := l.TagColors(); got["drums/kick"] != "#e66" || got["drums"] != "#111" || len(got) != 2 {
			t.Errorf("rename undone colours: %v", got)
		}
	})
	if same := must(l.RenameTag("perc", "PERC")); !same.Empty() {
		t.Errorf("rename to itself = %+v", same)
	}
	// A tag that merely shares a prefix is not a descendant.
	must(l.AddTags([]string{b}, []string{"percussion"}))

	ch = must(l.DeleteTag("perc"))
	if got := tagsOf(t, l, a); !slices.Equal(got, []string{"loud"}) {
		t.Errorf("after delete: %v", got)
	}
	if got := tagsOf(t, l, b); !slices.Equal(got, []string{"loud", "percussion"}) {
		t.Errorf("after delete b: %v", got)
	}
	if len(l.TagColors()) != 0 {
		t.Errorf("colours after delete: %v", l.TagColors())
	}
	undoRedo(t, ch, func() {
		if got := tagsOf(t, l, a); !slices.Equal(got, []string{"perc/kick", "loud", "perc"}) {
			t.Errorf("delete undone: %v", got)
		}
		if len(l.TagColors()) != 2 {
			t.Errorf("delete undone colours: %v", l.TagColors())
		}
	})

	ch = must(l.SetTagColor("Loud", "#abc"))
	if !ch.Structural || l.TagColors()["loud"] != "#abc" {
		t.Errorf("colour not set: %+v", ch)
	}
	must(l.SetTagColor("loud", ""))
	if len(l.TagColors()) != 0 {
		t.Error("colour not cleared")
	}

	// Everything survives a reopen.
	l2 := reopen(t, l)
	if got := tagsOf(t, l2, b); !slices.Equal(got, []string{"loud", "percussion"}) {
		t.Errorf("after reopen: %v", got)
	}
}

func TestFavoriteAndMeta(t *testing.T) {
	l := newLib(t)
	pk := newPack(t, l, "Pack")
	a, b := addSample(t, l, pk, "a.wav"), addSample(t, l, pk, "b.wav")

	ch := must(l.SetFavorite([]string{a, b}, true))
	if len(ch.IDs) != 2 || !ch.Structural {
		t.Errorf("change = %+v", ch)
	}
	undoRedo(t, ch, func() {
		if s, _ := l.Sample(a); s.Fav {
			t.Error("fav not undone")
		}
	})
	if s, _ := l.Sample(a); !s.Fav {
		t.Error("fav not redone")
	}

	ch = must(l.SetMeta([]string{a}, model.MetaPatch{BPM: ptr(124.0), Key: ptr(" F#m "), Type: ptr("loop"), Note: ptr("nice")}))
	if ch.Structural || !slices.Equal(ch.IDs, []string{a}) {
		t.Errorf("change = %+v", ch)
	}
	s, _ := l.Sample(a)
	if s.BPM != 124 || s.BPMSrc != "user" || s.Key != "F#m" || s.KeySrc != "user" || s.Type != "loop" {
		t.Errorf("sample = %+v", s)
	}
	if d := l.Details([]string{a}); d[0].Note != "nice" {
		t.Errorf("note = %q", d[0].Note)
	}
	// A partial patch leaves the other fields alone.
	must(l.SetMeta([]string{a}, model.MetaPatch{Note: ptr("")}))
	if s, _ := l.Sample(a); s.BPM != 124 || s.Key != "F#m" {
		t.Errorf("partial patch clobbered: %+v", s)
	}
	undoRedo(t, ch, func() {
		s, _ := l.Sample(a)
		if s.BPM != 0 || s.BPMSrc != "" || s.Key != "" || s.Type != "" {
			t.Errorf("meta not undone: %+v", s)
		}
	})

	for name, patch := range map[string]model.MetaPatch{
		"negative bpm": {BPM: ptr(-1.0)},
		"huge bpm":     {BPM: ptr(5000.0)},
		"bad type":     {Type: ptr("drone")},
	} {
		if _, err := l.SetMeta([]string{a}, patch); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestApplyAnalysisSourcePriority(t *testing.T) {
	tests := []struct {
		name    string
		curBPM  float64
		curSrc  string
		newBPM  float64
		newSrc  string
		wantBPM float64
		wantSrc string
	}{
		{"fills empty", 0, "", 120, model.SrcDetected, 120, model.SrcDetected},
		{"detected refreshes detected", 100, model.SrcDetected, 120, model.SrcDetected, 120, model.SrcDetected},
		{"filename beats detected", 100, model.SrcDetected, 120, model.SrcFilename, 120, model.SrcFilename},
		{"detected loses to filename", 100, model.SrcFilename, 120, model.SrcDetected, 100, model.SrcFilename},
		{"detected loses to splice", 100, model.SrcSplice, 120, model.SrcDetected, 100, model.SrcSplice},
		{"splice beats filename", 100, model.SrcFilename, 120, model.SrcSplice, 120, model.SrcSplice},
		{"nothing beats user", 100, model.SrcUser, 120, model.SrcSplice, 100, model.SrcUser},
		{"user cleared stays cleared", 0, model.SrcUser, 120, model.SrcDetected, 0, model.SrcUser},
		{"unknown result keeps value", 100, model.SrcDetected, 0, model.SrcDetected, 100, model.SrcDetected},
	}
	l := newLib(t)
	pk := newPack(t, l, "Pack")
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := sampleID(fmt.Sprint("prio", i))
			must(l.AdoptSample(pk, id, SampleRecord{Path: fmt.Sprintf("%d.wav", i), BPM: tt.curBPM, BPMSrc: tt.curSrc}))
			ch := must(l.ApplyAnalysis(id, Analysis{BPM: tt.newBPM, BPMSrc: tt.newSrc}))
			if ch.Undo != nil || !slices.Equal(ch.IDs, []string{id}) {
				t.Errorf("change = %+v", ch)
			}
			s, _ := l.Sample(id)
			if s.BPM != tt.wantBPM || s.BPMSrc != tt.wantSrc || !s.Analyzed {
				t.Errorf("bpm = %v (%s), want %v (%s)", s.BPM, s.BPMSrc, tt.wantBPM, tt.wantSrc)
			}
		})
	}

	id := addSample(t, l, pk, "full.wav")
	must(l.SetMeta([]string{id}, model.MetaPatch{Key: ptr("Am")}))
	must(l.ApplyAnalysis(id, Analysis{
		Fmt: "flac", Dur: 1.5, SR: 48000, Ch: 1, Mini: "AAAA",
		Detected: &Detected{BPM: 99.7, BPMConf: 0.5, Key: "C", KeyConf: 0.4, Type: "loop"},
		BPM:      100, BPMSrc: model.SrcDetected, Key: "C", KeySrc: model.SrcDetected, Type: "loop", TypeSrc: model.SrcDetected,
	}))
	s, _ := l.Sample(id)
	if s.Dur != 1.5 || s.SR != 48000 || s.Ch != 1 || s.Mini != "AAAA" || s.BPM != 100 || s.Key != "Am" || s.KeySrc != "user" || s.Type != "loop" {
		t.Errorf("sample = %+v", s)
	}
	_, rec, _ := l.Record(id)
	if rec.Fmt != "flac" || rec.Detected == nil || rec.Detected.Key != "C" || rec.TypeSrc != model.SrcDetected {
		t.Errorf("record = %+v", rec)
	}
	if _, err := l.ApplyAnalysis("missing", Analysis{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing sample: %v", err)
	}
}

func TestRenameSample(t *testing.T) {
	l := newLib(t)
	pk := newPack(t, l, "Pack")
	a := addSample(t, l, pk, "Drums/a.wav")
	b := addSample(t, l, pk, "Drums/b.wav")
	oldAbs, _ := l.AbsPath(a)

	ch := must(l.RenameSample(a, " New: Name.WAV "))
	s, _ := l.Sample(a)
	if s.RelPath != "Drums/New_ Name.wav" || s.Name != "New_ Name" || s.Ext != "wav" {
		t.Errorf("sample = %+v", s)
	}
	newAbs, _ := l.AbsPath(a)
	if exists(oldAbs) || !exists(newAbs) {
		t.Error("file not renamed on disk")
	}
	if id, ok := l.SampleIDForPath(newAbs); !ok || id != a {
		t.Error("new path does not resolve")
	}
	undoRedo(t, ch, func() {
		if !exists(oldAbs) || exists(newAbs) {
			t.Error("rename not undone on disk")
		}
	})
	if !exists(newAbs) {
		t.Error("rename not redone on disk")
	}

	if _, err := l.RenameSample(b, "NEW_ name"); !errors.Is(err, ErrPathTaken) {
		t.Errorf("collision: got %v", err)
	}
	// A file no record knows about also blocks the rename.
	os.WriteFile(filepath.Join(filepath.Dir(newAbs), "stray.wav"), []byte("x"), 0o644)
	if _, err := l.RenameSample(b, "stray"); !errors.Is(err, ErrPathTaken) {
		t.Errorf("stray collision: got %v", err)
	}
	if s, _ := l.Sample(b); s.RelPath != "Drums/b.wav" {
		t.Errorf("failed rename changed the record: %q", s.RelPath)
	}
	// Changing only the case is a rename, not a collision with itself.
	must(l.RenameSample(b, "B"))
	if s, _ := l.Sample(b); s.RelPath != "Drums/B.wav" {
		t.Errorf("case rename: %q", s.RelPath)
	}
	if _, err := l.RenameSample(a, "  "); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty name: %v", err)
	}
	if _, err := l.RenameSample("missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing sample: %v", err)
	}
	// The file vanished: the rename fails and the record is unchanged.
	os.Remove(newAbs)
	if _, err := l.RenameSample(a, "gone"); err == nil {
		t.Error("rename of a missing file succeeded")
	}
	if s, _ := l.Sample(a); s.RelPath != "Drums/New_ Name.wav" {
		t.Errorf("record changed: %q", s.RelPath)
	}
}

func TestRemoveAndRestore(t *testing.T) {
	l := newLib(t)
	pk := newPack(t, l, "Pack")
	a := addSample(t, l, pk, "Drums/Kicks/a.wav")
	b := addSample(t, l, pk, "b.FLAC")
	must(l.AddTags([]string{a}, []string{"kick"}))
	col, _, _ := l.CreateCollection("Set")
	must(l.AddToCollection(col.ID, []string{a, b}))
	absA, _ := l.AbsPath(a)
	trashA := filepath.Join(l.TrashDir(), a+".wav")
	trashB := filepath.Join(l.TrashDir(), b+".flac")

	ch := must(l.RemoveSamples([]string{a, b, "unknown"}))
	if !slices.Equal(ch.Removed, slices.Sorted(slices.Values([]string{a, b}))) || len(ch.IDs) != 0 || !ch.Structural {
		t.Errorf("change = %+v", ch)
	}
	if l.Has(a) || !l.Tombstoned(a) || exists(absA) || !exists(trashA) || !exists(trashB) {
		t.Error("sample not moved to trash")
	}
	if exists(filepath.Join(l.SamplesDir(), "Pack", "Drums")) {
		t.Error("empty folders left behind")
	}
	if got := l.Collections()[0].Count; got != 0 {
		t.Errorf("collection count = %d, want 0", got)
	}

	// The tombstone is on disk.
	noErr(t, l.Flush())
	pkFile := readJSON(t, filepath.Join(l.Root(), "_library/packs", pk+".json"))
	if rm := pkFile["removed"].(map[string]any); rm[a] == nil || rm[b] == nil || len(pkFile["samples"].(map[string]any)) != 0 {
		t.Errorf("pack file = %v", pkFile)
	}

	redo := undoRedo(t, ch, func() {
		if !l.Has(a) || l.Tombstoned(a) || !exists(absA) || exists(trashA) {
			t.Error("sample not restored")
		}
		if got := tagsOf(t, l, a); !slices.Equal(got, []string{"kick"}) {
			t.Errorf("restored tags = %v", got)
		}
		if got := l.Collections()[0].Count; got != 2 {
			t.Errorf("collection count after restore = %d", got)
		}
	})
	if len(redo.Removed) != 2 || l.Has(a) || !exists(trashA) {
		t.Errorf("redo = %+v", redo)
	}

	// Restore when the old name has been taken in the meantime.
	c := addContent(t, l, pk, "Drums/Kicks/a.wav", "usurper")
	must(redo.Undo())
	sa, _ := l.Sample(a)
	sc, _ := l.Sample(c)
	if sa.RelPath != "Drums/Kicks/a (2).wav" || sc.RelPath != "Drums/Kicks/a.wav" {
		t.Errorf("paths after restore: %q, %q", sa.RelPath, sc.RelPath)
	}
	if abs, _ := l.AbsPath(a); !exists(abs) {
		t.Error("restored file missing")
	}

	// Removing a sample whose file is already gone still works and restores.
	absB, _ := l.AbsPath(b)
	os.Remove(absB)
	ch = must(l.RemoveSamples([]string{b}))
	if l.Has(b) {
		t.Error("missing-file sample not removed")
	}
	must(ch.Undo())
	if !l.Has(b) {
		t.Error("missing-file sample not restored")
	}

	// Re-importing removed content clears its tombstone.
	must(l.RemoveSamples([]string{c}))
	c2 := addContent(t, l, pk, "again.wav", "usurper")
	if c2 != c || l.Tombstoned(c) || !l.Has(c) {
		t.Error("re-import after removal failed")
	}

	l2 := reopen(t, l)
	if !l2.Has(a) || !l2.Has(b) || !l2.Has(c) || l2.Count() != 3 {
		t.Errorf("after reopen: count %d", l2.Count())
	}
}

func TestRemoveRollsBackOnFailure(t *testing.T) {
	l := newLib(t)
	pk := newPack(t, l, "Pack")
	a := addSample(t, l, pk, "a.wav")
	b := addSample(t, l, pk, "b.wav")
	absA, _ := l.AbsPath(a)
	// A non-empty folder squatting on b's trash name makes its move fail.
	os.MkdirAll(filepath.Join(l.TrashDir(), b+".wav", "x"), 0o755)

	ch, err := l.RemoveSamples([]string{a, b})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !ch.Empty() || ch.Undo != nil {
		t.Errorf("change on failure = %+v", ch)
	}
	if !l.Has(a) || !l.Has(b) || !exists(absA) || l.Tombstoned(a) {
		t.Error("failed removal was not rolled back")
	}
}

func TestCollections(t *testing.T) {
	l := newLib(t)
	pk := newPack(t, l, "Pack")
	a, b, c := addSample(t, l, pk, "a.wav"), addSample(t, l, pk, "b.wav"), addSample(t, l, pk, "c.wav")

	info, ch, err := l.CreateCollection("  Techno set ")
	if err != nil || info.Name != "Techno set" || info.Count != 0 || !ch.Structural || info.ID[:4] != "col_" {
		t.Fatalf("create = %+v, %+v, %v", info, ch, err)
	}
	id := info.ID
	file := filepath.Join(l.Root(), "_library/collections", id+".json")

	ch = must(l.AddToCollection(id, []string{b, a, b, "unknown"}))
	if !slices.Equal(ch.IDs, slices.Sorted(slices.Values([]string{a, b}))) || !ch.Structural {
		t.Errorf("change = %+v", ch)
	}
	if items, _ := l.CollectionItems(id); !slices.Equal(items, []string{b, a}) {
		t.Errorf("items = %v", items)
	}
	ch = must(l.AddToCollection(id, []string{a, c}))
	if !slices.Equal(ch.IDs, []string{c}) {
		t.Errorf("only the new member should be reported: %+v", ch)
	}
	if again := must(l.AddToCollection(id, []string{a})); !again.Empty() {
		t.Errorf("no-op = %+v", again)
	}
	if d := l.Details([]string{a}); !slices.Equal(d[0].Collections, []string{id}) {
		t.Errorf("membership = %v", d[0].Collections)
	}

	ch = must(l.RemoveFromCollection(id, []string{a}))
	undoRedo(t, ch, func() {
		if items, _ := l.CollectionItems(id); !slices.Equal(items, []string{b, a, c}) {
			t.Errorf("remove undone: %v", items)
		}
	})
	if items, _ := l.CollectionItems(id); !slices.Equal(items, []string{b, c}) {
		t.Errorf("remove redone: %v", items)
	}

	ch = must(l.RenameCollection(id, "House"))
	must(ch.Undo())
	if got := l.Collections()[0].Name; got != "Techno set" {
		t.Errorf("rename undone: %q", got)
	}

	noErr(t, l.Flush())
	if !exists(file) {
		t.Fatal("collection file not written")
	}
	ch = must(l.DeleteCollection(id))
	if !slices.Equal(ch.IDs, slices.Sorted(slices.Values([]string{b, c}))) {
		t.Errorf("delete change = %+v", ch)
	}
	noErr(t, l.Flush())
	if exists(file) || len(l.Collections()) != 0 {
		t.Error("collection not deleted")
	}
	redo := undoRedo(t, ch, func() {
		noErr(t, l.Flush())
		if items, ok := l.CollectionItems(id); !ok || !slices.Equal(items, []string{b, c}) || !exists(file) {
			t.Errorf("delete undone: %v", items)
		}
	})
	// Delete then undo before any write must not delete the restored file.
	must(redo.Undo())
	noErr(t, l.Flush())
	if !exists(file) {
		t.Error("restored collection file missing")
	}

	for name, err := range map[string]error{
		"rename missing": errOf(l.RenameCollection("col_x", "n")),
		"delete missing": errOf(l.DeleteCollection("col_x")),
		"add missing":    errOf(l.AddToCollection("col_x", []string{a})),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if _, _, err := l.CreateCollection(" "); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty name: %v", err)
	}
}

func errOf(_ Change, err error) error { return err }

func TestViewsAndPacks(t *testing.T) {
	l := newLib(t)
	v1, ch, err := l.SaveView("Loops", "type:loop")
	if err != nil || v1.ID == "" || !ch.Structural {
		t.Fatalf("save = %+v, %+v, %v", v1, ch, err)
	}
	v2, _, _ := l.SaveView("Favs", "is:fav")
	// Saving under an existing name replaces the query, keeping the ID.
	v1b, ch2, _ := l.SaveView("loops", "type:loop bpm:120")
	if v1b.ID != v1.ID || len(l.Views()) != 2 || l.Views()[0].Query != "type:loop bpm:120" {
		t.Errorf("views = %v", l.Views())
	}
	must(ch2.Undo())
	if l.Views()[0].Query != "type:loop" || l.Views()[0].Name != "Loops" {
		t.Errorf("save undone: %v", l.Views())
	}
	ch = must(l.DeleteView(v1.ID))
	if got := l.Views(); len(got) != 1 || got[0].ID != v2.ID {
		t.Errorf("after delete: %v", got)
	}
	must(ch.Undo())
	if got := l.Views(); len(got) != 2 || got[0].ID != v1.ID {
		t.Errorf("delete undone: %v", got)
	}
	if _, err := l.DeleteView("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing view: %v", err)
	}

	info, ch, err := l.CreatePack("KSHMR Vol 4", "Splice/KSHMR Vol 4", SourceSplice)
	if err != nil || info.Dir != "Splice/KSHMR Vol 4" || info.Source != "splice" || info.ID[:3] != "pk_" {
		t.Fatalf("pack = %+v, %v", info, err)
	}
	id := addSample(t, l, info.ID, "a.wav")
	rn := must(l.RenamePack(info.ID, "Renamed"))
	if !slices.Equal(rn.IDs, []string{id}) || !rn.Structural {
		t.Errorf("rename change = %+v", rn)
	}
	if s, _ := l.Sample(id); s.PackName != "Renamed" || l.Packs()[0].Dir != "Splice/KSHMR Vol 4" {
		t.Errorf("pack name not applied / dir changed")
	}
	must(rn.Undo())
	if s, _ := l.Sample(id); s.PackName != "KSHMR Vol 4" {
		t.Errorf("rename not undone: %q", s.PackName)
	}
	if _, err := l.RenamePack("pk_x", "n"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing pack: %v", err)
	}
	// Undoing the creation of a pack that has since been filled is refused.
	if _, err := ch.Undo(); !errors.Is(err, ErrNotEmpty) {
		t.Errorf("undo create of non-empty pack: %v", err)
	}
	empty, ch, _ := l.CreatePack("Empty", "", SourceImport)
	noErr(t, l.Flush())
	file := filepath.Join(l.Root(), "_library/packs", empty.ID+".json")
	undone := must(ch.Undo())
	noErr(t, l.Flush())
	if exists(file) || len(l.Packs()) != 1 {
		t.Error("pack creation not undone")
	}
	must(undone.Undo())
	noErr(t, l.Flush())
	if !exists(file) || len(l.Packs()) != 2 {
		t.Error("pack creation not redone")
	}
}

func TestConcurrentMutations(t *testing.T) {
	l := newLib(t)
	pk := newPack(t, l, "Pack")
	col, _, _ := l.CreateCollection("Set")
	const workers, perWorker = 8, 40
	ids := make([][]string, workers)
	for w := range workers {
		for i := range perWorker {
			ids[w] = append(ids[w], addSample(t, l, pk, fmt.Sprintf("w%d/%d.wav", w, i)))
		}
	}
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for i, id := range ids[w] {
				tag := fmt.Sprintf("t%d", w)
				ch := must(l.AddTags([]string{id}, []string{tag, "shared"}))
				must(l.SetFavorite([]string{id}, i%2 == 0))
				must(l.AddToCollection(col.ID, []string{id}))
				must(l.ApplyAnalysis(id, Analysis{BPM: 120, BPMSrc: model.SrcDetected}))
				if i%4 == 0 {
					must(ch.Undo())
				}
				if i%10 == 0 {
					must(l.RenameSample(id, fmt.Sprintf("renamed-%d", i)))
					noErr(t, l.Flush())
				}
				l.Sample(id)
				l.Details([]string{id})
				l.Packs()
				l.Collections()
			}
		})
		// Concurrent ingest into the same folder exercises collision handling.
		wg.Go(func() {
			for i := range perWorker {
				content := fmt.Sprintf("dup %d %d", w, i)
				must(l.AddSample(pk, sampleID(content), SampleRecord{Path: "same/name.wav"}, func(abs string) error {
					return os.WriteFile(abs, []byte(content), 0o644)
				}))
			}
		})
	}
	wg.Go(func() {
		for range 20 {
			if _, _, err := l.Reload(); err != nil {
				t.Error(err)
			}
			l.Samples()
			time.Sleep(time.Millisecond)
		}
	})
	wg.Wait()

	check := func(l *Library) {
		t.Helper()
		if got, want := l.Count(), workers*perWorker*2; got != want {
			t.Errorf("count = %d, want %d", got, want)
		}
		if items, _ := l.CollectionItems(col.ID); len(items) != workers*perWorker {
			t.Errorf("collection has %d items", len(items))
		}
		paths := map[string]bool{}
		for _, s := range l.Samples() {
			key := PathKey(s.RelPath)
			if paths[key] {
				t.Errorf("duplicate path %s", s.RelPath)
			}
			paths[key] = true
			if abs, _ := l.AbsPath(s.ID); !exists(abs) {
				t.Errorf("file missing for %s", s.RelPath)
			}
		}
		for w := range workers {
			for i, id := range ids[w] {
				s, _ := l.Sample(id)
				wantTags := 2
				if i%4 == 0 {
					wantTags = 0
				}
				if len(s.Tags) != wantTags || s.Fav != (i%2 == 0) || s.BPM != 120 {
					t.Errorf("sample w%d/%d = %+v", w, i, s)
				}
			}
		}
	}
	check(l)
	l2 := reopen(t, l)
	check(l2)
	if left := tempFiles(t, l2.Root()); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
	if w := l2.Info().Warnings; len(w) != 0 {
		t.Errorf("warnings: %v", w)
	}
}
