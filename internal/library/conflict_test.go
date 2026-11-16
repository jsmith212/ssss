package library

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func at(sec int) time.Time { return time.Date(2026, 11, 15, 10, 0, sec, 0, time.UTC) }

func TestMergePack(t *testing.T) {
	rec := func(note string, rev int) *SampleRecord {
		return &SampleRecord{Path: note + ".wav", Note: note, Rev: at(rev)}
	}
	tests := []struct {
		name         string
		dst, src     *SampleRecord
		dstRm, srcRm int    // tombstone second, 0 = none
		wantNote     string // "" = sample absent
		wantRm       int
	}{
		{"only in copy", nil, rec("src", 5), 0, 0, "src", 0},
		{"only here", rec("dst", 5), nil, 0, 0, "dst", 0},
		{"copy newer", rec("dst", 5), rec("src", 6), 0, 0, "src", 0},
		{"copy older", rec("dst", 6), rec("src", 5), 0, 0, "dst", 0},
		{"same rev keeps ours", rec("dst", 5), rec("src", 5), 0, 0, "dst", 0},
		{"removed there after our edit", rec("dst", 5), nil, 0, 6, "", 6},
		{"edited here after removal there", rec("dst", 7), nil, 0, 6, "dst", 0},
		{"removed here after their edit", nil, rec("src", 5), 6, 0, "", 6},
		{"restored there after removal here", nil, rec("src", 7), 6, 0, "src", 0},
		{"removed on both sides", nil, nil, 4, 6, "", 6},
		{"tombstone only there", nil, nil, 0, 6, "", 6},
	}
	const id = "id"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mk := func(r *SampleRecord, rm int) *pack {
				p := &pack{Samples: map[string]*SampleRecord{}, Removed: map[string]time.Time{}}
				if r != nil {
					p.Samples[id] = r
				}
				if rm != 0 {
					p.Removed[id] = at(rm)
				}
				return p
			}
			dst := mk(tt.dst, tt.dstRm)
			mergePack(dst, mk(tt.src, tt.srcRm))
			got := ""
			if r := dst.Samples[id]; r != nil {
				got = r.Note
			}
			if got != tt.wantNote {
				t.Errorf("sample = %q, want %q", got, tt.wantNote)
			}
			var wantRm time.Time
			if tt.wantRm != 0 {
				wantRm = at(tt.wantRm)
			}
			if !dst.Removed[id].Equal(wantRm) {
				t.Errorf("tombstone = %v, want %v", dst.Removed[id], wantRm)
			}
		})
	}
}

func hexID(n int) string { return fmt.Sprintf("%032x", n) }

func packJSON(id string, samples map[string]string, removed map[string]string) string {
	var recs, rms []string
	for sid, body := range samples {
		recs = append(recs, fmt.Sprintf("%q:%s", sid, body))
	}
	for sid, when := range removed {
		rms = append(rms, fmt.Sprintf("%q:%q", sid, when))
	}
	return fmt.Sprintf(`{"v":1,"id":%q,"name":"P","dir":%q,"source":"import","samples":{%s},"removed":{%s}}`,
		id, strings.TrimPrefix(id, "pk_"), strings.Join(recs, ","), strings.Join(rms, ","))
}

func TestConflictCopiesAreMergedOnOpen(t *testing.T) {
	root := writeHandLibrary(t)
	meta := filepath.Join(root, "_library")
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(meta, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a, b, c, d := hexID(1), hexID(2), hexID(3), hexID(4)
	write("packs/pk_P.json", packJSON("pk_P", map[string]string{
		a: `{"path":"a.wav","note":"ours-new","rev":"2026-11-15T10:00:09Z","tags":[]}`,
		b: `{"path":"b.wav","note":"ours-old","rev":"2026-11-15T10:00:01Z","tags":[]}`,
		c: `{"path":"c.wav","note":"removed-there","rev":"2026-11-15T10:00:01Z","tags":[]}`,
	}, nil))
	write("packs/pk_P (conflicted copy 2026-11-15 101500).json", packJSON("pk_P", map[string]string{
		a: `{"path":"a.wav","note":"theirs-old","rev":"2026-11-15T10:00:05Z","tags":[]}`,
		b: `{"path":"b.wav","note":"theirs-new","rev":"2026-11-15T10:00:05Z","tags":["x"],"futureField":1}`,
		d: `{"path":"d.wav","note":"only-theirs","rev":"2026-11-15T10:00:05Z","tags":[]}`,
	}, map[string]string{c: "2026-11-15T10:00:07Z"}))
	// Syncthing style, and a pack that only exists as a conflict copy.
	write("packs/pk_ORPHAN.sync-conflict-20261115-101500-ABCDEFG.json", packJSON("pk_ORPHAN", map[string]string{
		hexID(9): `{"path":"o.wav","rev":"2026-11-15T10:00:05Z","tags":[]}`,
	}, nil))
	write("collections/col_HAND (conflict 2).json",
		`{"v":1,"id":"col_HAND","name":"Their name","items":["`+a+`","`+handID+`"],"rev":"2026-11-15T10:07:00Z"}`)
	write("tags (conflicted copy).json", `{"v":1,"tags":{"drums/kick":{"color":"#000"},"synth":{"color":"#0f0"}}}`)
	write("views (conflicted copy).json", `{"v":1,"views":[{"id":"vw_1","name":"X","query":"x"},{"id":"vw_2","name":"Theirs","query":"is:fav"}]}`)
	write("library (conflicted copy).json", `{"v":1,"id":"lib_HAND","name":"Main"}`)
	write("packs/broken (conflicted copy).json", `{not json`)

	l := must(Open(root, Options{}))
	defer l.Close()

	notes := map[string]string{}
	for _, d := range l.Details([]string{a, b, c, d}) {
		notes[d.ID] = d.Note
	}
	want := map[string]string{a: "ours-new", b: "theirs-new", d: "only-theirs"}
	if fmt.Sprint(notes) != fmt.Sprint(want) {
		t.Errorf("merged notes = %v, want %v", notes, want)
	}
	if !l.Tombstoned(c) || !l.Has(hexID(9)) {
		t.Error("tombstone or orphan pack not merged")
	}
	if items, _ := l.CollectionItems("col_HAND"); !slices.Equal(items, []string{handID, a}) {
		t.Errorf("collection items = %v", items)
	}
	if got := l.Collections()[0].Name; got != "Their name" {
		t.Errorf("collection name = %q", got)
	}
	if got := l.TagColors(); got["drums/kick"] != "#e66" || got["synth"] != "#0f0" {
		t.Errorf("tag colours = %v", got)
	}
	if got := l.Views(); len(got) != 2 || got[0].Name != "Loops" || got[1].ID != "vw_2" {
		t.Errorf("views = %v", got)
	}

	// The merge is already on disk, the copies are in the trash, and the
	// one that could not be read stays put with a warning.
	var onDisk struct {
		Samples map[string]json.RawMessage `json:"samples"`
		Removed map[string]string          `json:"removed"`
	}
	if err := json.Unmarshal(must(os.ReadFile(filepath.Join(meta, "packs/pk_P.json"))), &onDisk); err != nil {
		t.Fatal(err)
	}
	if len(onDisk.Samples) != 3 || onDisk.Removed[c] == "" || !strings.Contains(string(onDisk.Samples[b]), `"futureField":1`) {
		t.Errorf("pk_P.json = %+v", onDisk)
	}
	if !exists(filepath.Join(meta, "packs/pk_ORPHAN.json")) {
		t.Error("orphan pack not written under its canonical name")
	}
	var left []string
	for _, dir := range []string{"", "packs", "collections"} {
		for _, e := range must(os.ReadDir(filepath.Join(meta, dir))) {
			if isConflictCopy(e.Name()) {
				left = append(left, e.Name())
			}
		}
	}
	if !slices.Equal(left, []string{"broken (conflicted copy).json"}) {
		t.Errorf("conflict copies left = %v", left)
	}
	if n := len(must(os.ReadDir(l.TrashDir()))); n != 6 {
		t.Errorf("%d files in trash, want 6", n)
	}
	if w := l.Info().Warnings; len(w) != 1 || !strings.Contains(w[0], "broken") {
		t.Errorf("warnings = %v", w)
	}
	if again, _, err := l.Reload(); err != nil || !again.Empty() {
		t.Errorf("Reload right after open = %+v, %v", again, err)
	}
}

func TestReload(t *testing.T) {
	l := newLib(t)
	pk := newPack(t, l, "Pack")
	a := addSample(t, l, pk, "a.wav")
	l.SetMissing([]string{a}, true)

	if ch, _, err := l.Reload(); err != nil || !ch.Empty() {
		t.Fatalf("no-op reload = %+v, %v", ch, err)
	}
	// Reload flushed, so the pack is on disk; edit it as another machine would.
	file := filepath.Join(l.Root(), "_library/packs", pk+".json")
	edited := strings.Replace(string(must(os.ReadFile(file))), `"fav":false`, `"fav":true`, 1)
	if err := os.WriteFile(file, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	ch, _, err := l.Reload()
	if err != nil || !ch.All || !ch.Structural {
		t.Fatalf("reload = %+v, %v", ch, err)
	}
	if s, _ := l.Sample(a); !s.Fav || !s.Missing {
		t.Errorf("after reload: %+v", s)
	}

	// A conflict copy appearing later is merged by Reload.
	b := hexID(7)
	copyName := filepath.Join(filepath.Dir(file), pk+" (conflicted copy).json")
	os.WriteFile(copyName, []byte(packJSON(pk, map[string]string{
		b: `{"path":"b.wav","rev":"2026-11-15T10:00:05Z","tags":[]}`,
	}, nil)), 0o644)
	if ch, _, err := l.Reload(); err != nil || !ch.All {
		t.Fatalf("reload with conflict = %+v, %v", ch, err)
	}
	if !l.Has(a) || !l.Has(b) || exists(copyName) {
		t.Error("conflict copy not merged by Reload")
	}

	// The folder going away keeps what is in memory.
	hidden := l.Root() + ".away"
	if err := os.Rename(l.Root(), hidden); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.Reload(); err == nil {
		t.Error("Reload of a vanished folder succeeded")
	}
	if !l.Has(a) {
		t.Error("state dropped when the folder vanished")
	}
	os.Rename(hidden, l.Root())
}
