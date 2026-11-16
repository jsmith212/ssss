package api

import (
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"ssss/internal/model"
	"ssss/internal/testaudio"
)

func TestSpliceImport(t *testing.T) {
	spliceDir := filepath.Join(t.TempDir(), "Splice")
	packs := filepath.Join(spliceDir, "sounds", "packs")
	loop := filepath.Join(packs, "Deep House Vol 2", "Loops", "DH2_Chord_Loop_124_Fmin.wav")
	kick := filepath.Join(packs, "Deep House Vol 2", "Drums", "DH2_Kick_03.wav")
	other := filepath.Join(packs, "Other Pack", "tone.wav")
	must(t, testaudio.Write(loop, testaudio.Loop(124, 8, 174.6)))
	must(t, testaudio.Write(kick, testaudio.Tone(50, 0.3, 15)))
	must(t, testaudio.Write(other, testaudio.Tone(440, 0.5, 4)))

	db, err := sql.Open("sqlite", filepath.Join(spliceDir, "sounds.db"))
	must(t, err)
	for _, stmt := range []string{
		`CREATE TABLE packs(uuid TEXT, name TEXT, genre TEXT)`,
		`CREATE TABLE samples(local_path TEXT, audio_key TEXT, chord_type TEXT, bpm INTEGER, sample_type TEXT, tags TEXT, pack_uuid TEXT)`,
		`INSERT INTO packs VALUES ('u1', 'Deep House Vol 2', 'House')`,
	} {
		_, err := db.Exec(stmt)
		must(t, err)
	}
	// Splice says 62 BPM (half time) in G minor: its word beats the name's.
	_, err = db.Exec(`INSERT INTO samples VALUES (?, 'g', 'minor', 62, 'loop', 'chords,keys', 'u1'), (?, NULL, NULL, NULL, 'oneshot', 'drums,kicks', 'u1')`, loop, kick)
	must(t, err)
	must(t, db.Close())

	a := newMachine(t, t.TempDir())
	t.Cleanup(a.shutdown)
	settings := a.GetState().Settings
	settings.SpliceFolder = spliceDir
	_, err = a.UpdateSettings(settings)
	must(t, err)

	// Detection works before any library exists (the first-run screen).
	info := a.DetectSplice()
	want := []model.SplicePack{
		{Name: "Deep House Vol 2", Count: 2, Genre: "House"},
		{Name: "Other Pack", Count: 1},
	}
	if !info.Found || !info.HasDB || info.Folder != spliceDir || !slices.Equal(info.Packs, want) {
		t.Fatalf("DetectSplice = %+v", info)
	}
	if _, err := a.ImportSplice(spliceDir, nil); err == nil {
		t.Error("ImportSplice succeeded without a library")
	}

	_, err = a.CreateLibrary(filepath.Join(t.TempDir(), "Library"), "Main")
	must(t, err)
	a.settle()
	a.clearEvents()
	jobID, err := a.ImportSplice(spliceDir, []string{"Deep House Vol 2"})
	must(t, err)
	a.settle()
	if p := a.lastProgress(model.JobSplice); p.JobID != jobID || p.Added != 2 || p.Failed != 0 {
		t.Errorf("splice import = %+v", p)
	}

	a.expect("", "DH2_Chord_Loop_124_Fmin", "DH2_Kick_03")
	a.expect(`@"Deep House Vol 2"`, "DH2_Chord_Loop_124_Fmin", "DH2_Kick_03")
	a.expect("tag:chords", "DH2_Chord_Loop_124_Fmin")
	a.expect("#kicks", "DH2_Kick_03")
	d := a.detail(a.id("DH2_Chord_Loop_124_Fmin"))
	if d.BPM != 62 || d.BPMSrc != model.SrcSplice || d.Key != "Gm" || d.KeySrc != model.SrcSplice || d.Type != model.TypeLoop || !d.Analyzed {
		t.Errorf("splice metadata after analysis = %+v", d.Sample)
	}
	if wantPath := filepath.Join(a.GetState().LibraryPath, "Samples", "Splice", "Deep House Vol 2", "Loops", "DH2_Chord_Loop_124_Fmin.wav"); d.AbsPath != wantPath {
		t.Errorf("imported to %q, want %q", d.AbsPath, wantPath)
	}
	sb := a.GetSidebar()
	if len(sb.Packs) != 1 || sb.Packs[0].Source != "splice" || sb.Packs[0].Dir != "Splice/Deep House Vol 2" {
		t.Errorf("packs = %+v", sb.Packs)
	}

	info = a.DetectSplice()
	if !info.Packs[0].Imported || info.Packs[1].Imported {
		t.Errorf("imported flags = %+v", info.Packs)
	}
}
