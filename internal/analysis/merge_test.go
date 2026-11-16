package analysis

import "testing"

func TestMerge(t *testing.T) {
	loop := Result{
		Parsed:  Parsed{BPM: 128, Key: "Am", TypeHint: TypeLoop},
		Type:    TypeLoop,
		BPM:     128,
		BarBPMs: []float64{64, 96, 128, 192},
		Key:     "C",
	}
	tests := []struct {
		name     string
		existing Meta
		result   Result
		want     Merged
	}{
		{
			name:   "fresh sample, name and audio agree",
			result: loop,
			want:   Merged{Meta: Meta{TypeLoop, SourceFilename, 128, SourceFilename, "Am", SourceFilename}},
		},
		{
			name:   "nothing in the name: detected values",
			result: Result{Type: TypeLoop, BPM: 124, BarBPMs: []float64{62, 93, 124, 186}, Key: "F#m"},
			want:   Merged{Meta: Meta{TypeLoop, SourceDetected, 124, SourceDetected, "F#m", SourceDetected}},
		},
		{
			name:   "nothing known at all",
			result: Result{Type: TypeOneShot},
			want:   Merged{Meta: Meta{Type: TypeOneShot, TypeSrc: SourceDetected}},
		},
		{
			name:   "name says half of what was detected",
			result: Result{Parsed: Parsed{BPM: 70}, Type: TypeLoop, BPM: 140, BarBPMs: []float64{105, 140}},
			want:   Merged{Meta: Meta{TypeLoop, SourceDetected, 70, SourceFilename, "", ""}},
		},
		{
			name:   "name says double of what was detected",
			result: Result{Parsed: Parsed{BPM: 174}, Type: TypeLoop, BPM: 87, BarBPMs: []float64{87, 130.5}},
			want:   Merged{Meta: Meta{TypeLoop, SourceDetected, 174, SourceFilename, "", ""}},
		},
		{
			name:   "name matches a bar candidate the detector did not pick",
			result: Result{Parsed: Parsed{BPM: 90}, Type: TypeLoop, BPM: 120, BarBPMs: []float64{60, 90, 120, 180}},
			want:   Merged{Meta: Meta{TypeLoop, SourceDetected, 90, SourceFilename, "", ""}},
		},
		{
			name:   "name within 1% of a bar candidate",
			result: Result{Parsed: Parsed{BPM: 128}, Type: TypeLoop, BPM: 127.4, BarBPMs: []float64{127.4}},
			want:   Merged{Meta: Meta{TypeLoop, SourceDetected, 128, SourceFilename, "", ""}},
		},
		{
			name:   "name conflicts with the audio: keep it, flag it",
			result: Result{Parsed: Parsed{BPM: 100}, Type: TypeLoop, BPM: 128, BarBPMs: []float64{64, 96, 128, 192}},
			want:   Merged{Meta: Meta{TypeLoop, SourceDetected, 100, SourceFilename, "", ""}, NeedsReview: true},
		},
		{
			name:   "name tempo with nothing detected is not a conflict",
			result: Result{Parsed: Parsed{BPM: 100}, Type: TypeOneShot},
			want:   Merged{Meta: Meta{TypeOneShot, SourceDetected, 100, SourceFilename, "", ""}},
		},
		{
			name:   "folder tempo does not stick to a one-shot",
			result: Result{Parsed: Parsed{BPM: 140, BPMFromFolder: true}, Type: TypeOneShot},
			want:   Merged{Meta: Meta{Type: TypeOneShot, TypeSrc: SourceDetected}},
		},
		{
			name:   "folder tempo applies to a loop",
			result: Result{Parsed: Parsed{BPM: 140, BPMFromFolder: true}, Type: TypeLoop, BPM: 140, BarBPMs: []float64{140}},
			want:   Merged{Meta: Meta{TypeLoop, SourceDetected, 140, SourceFilename, "", ""}},
		},
		{
			name:   "guessed tempo that the audio bears out is kept",
			result: Result{Parsed: Parsed{BPM: 128, BPMWeak: true}, Type: TypeLoop, BPM: 128, BarBPMs: []float64{96, 128}},
			want:   Merged{Meta: Meta{TypeLoop, SourceDetected, 128, SourceFilename, "", ""}},
		},
		{
			name:   "guessed tempo at half the detected one is kept",
			result: Result{Parsed: Parsed{BPM: 87, BPMWeak: true}, Type: TypeLoop, BPM: 174, BarBPMs: []float64{174}},
			want:   Merged{Meta: Meta{TypeLoop, SourceDetected, 87, SourceFilename, "", ""}},
		},
		{
			name:   "guessed tempo that the audio contradicts gives way",
			result: Result{Parsed: Parsed{BPM: 182, BPMWeak: true}, Type: TypeLoop, BPM: 120, BarBPMs: []float64{90, 120}},
			want:   Merged{Meta: Meta{TypeLoop, SourceDetected, 120, SourceDetected, "", ""}},
		},
		{
			name:   "guessed tempo does not stick to a one-shot",
			result: Result{Parsed: Parsed{BPM: 78, BPMWeak: true}, Type: TypeOneShot, BarBPMs: []float64{78}},
			want:   Merged{Meta: Meta{Type: TypeOneShot, TypeSrc: SourceDetected}},
		},
		{
			name:   "guessed tempo with no audio to check it against is dropped",
			result: Result{Parsed: Parsed{BPM: 100, BPMWeak: true}},
			want:   Merged{},
		},
		{
			name:   "name key always beats a detected key",
			result: Result{Parsed: Parsed{Key: "Am"}, Type: TypeLoop, Key: "E", KeyConf: 0.99},
			want:   Merged{Meta: Meta{TypeLoop, SourceDetected, 0, "", "Am", SourceFilename}},
		},
		{
			name:   "weak name key loses to a detected key",
			result: Result{Parsed: Parsed{Key: "E", KeyWeak: true}, Type: TypeLoop, Key: "Am"},
			want:   Merged{Meta: Meta{TypeLoop, SourceDetected, 0, "", "Am", SourceDetected}},
		},
		{
			name:   "weak name key stands when nothing was detected",
			result: Result{Parsed: Parsed{Key: "Gm", KeyWeak: true}, Type: TypeOneShot},
			want:   Merged{Meta: Meta{TypeOneShot, SourceDetected, 0, "", "Gm", SourceFilename}},
		},
		{
			name:   "chord name supplies the mode for a matching detected root",
			result: Result{Parsed: Parsed{Key: "Gm", KeyWeak: true}, Type: TypeOneShot, Key: "G", KeyRootOnly: true},
			want:   Merged{Meta: Meta{TypeOneShot, SourceDetected, 0, "", "Gm", SourceFilename}},
		},
		{
			name:   "chord name with a different detected root",
			result: Result{Parsed: Parsed{Key: "Gm", KeyWeak: true}, Type: TypeOneShot, Key: "D", KeyRootOnly: true},
			want:   Merged{Meta: Meta{TypeOneShot, SourceDetected, 0, "", "D", SourceDetected}},
		},
		{
			name:     "user values are never overwritten",
			existing: Meta{TypeOneShot, SourceUser, 99, SourceUser, "D#m", SourceUser},
			result:   loop,
			want:     Merged{Meta: Meta{TypeOneShot, SourceUser, 99, SourceUser, "D#m", SourceUser}},
		},
		{
			name:     "a user clearing a value is respected",
			existing: Meta{BPMSrc: SourceUser, KeySrc: SourceUser, Type: TypeLoop, TypeSrc: SourceDetected},
			result:   loop,
			want:     Merged{Meta: Meta{TypeLoop, SourceFilename, 0, SourceUser, "", SourceUser}},
		},
		{
			name:     "splice values outrank name and audio",
			existing: Meta{TypeLoop, SourceSplice, 64, SourceSplice, "C", SourceSplice},
			result:   loop,
			want:     Merged{Meta: Meta{TypeLoop, SourceSplice, 64, SourceSplice, "C", SourceSplice}},
		},
		{
			name:     "splice gaps are filled in",
			existing: Meta{Type: TypeLoop, TypeSrc: SourceSplice, BPMSrc: SourceSplice, KeySrc: SourceSplice},
			result:   loop,
			want:     Merged{Meta: Meta{TypeLoop, SourceSplice, 128, SourceFilename, "Am", SourceFilename}},
		},
		{
			name:     "splice tempo that the audio contradicts is flagged",
			existing: Meta{BPM: 100, BPMSrc: SourceSplice},
			result:   Result{Type: TypeLoop, BPM: 128, BarBPMs: []float64{64, 96, 128, 192}},
			want:     Merged{Meta: Meta{TypeLoop, SourceDetected, 100, SourceSplice, "", ""}, NeedsReview: true},
		},
		{
			name:     "re-analysis replaces earlier name and detected values",
			existing: Meta{TypeOneShot, SourceDetected, 90, SourceFilename, "G", SourceDetected},
			result:   loop,
			want:     Merged{Meta: Meta{TypeLoop, SourceFilename, 128, SourceFilename, "Am", SourceFilename}},
		},
		{
			name:     "re-analysis clears what no longer holds",
			existing: Meta{TypeLoop, SourceDetected, 90, SourceFilename, "G", SourceDetected},
			result:   Result{Type: TypeOneShot},
			want:     Merged{Meta: Meta{Type: TypeOneShot, TypeSrc: SourceDetected}},
		},
		{
			name:   "type disagreeing with the name hint counts as detected",
			result: Result{Parsed: Parsed{TypeHint: TypeLoop}, Type: TypeOneShot},
			want:   Merged{Meta: Meta{Type: TypeOneShot, TypeSrc: SourceDetected}},
		},
	}
	for _, tc := range tests {
		if got := Merge(tc.existing, tc.result); got != tc.want {
			t.Errorf("%s:\n  got  %+v\n  want %+v", tc.name, got, tc.want)
		}
	}
}

func TestMergeIsIdempotent(t *testing.T) {
	r := Result{Parsed: Parsed{BPM: 100, Key: "E", KeyWeak: true}, Type: TypeLoop, BPM: 128, BarBPMs: []float64{128}, Key: "Am"}
	once := Merge(Meta{}, r)
	if twice := Merge(once.Meta, r); twice != once {
		t.Errorf("second merge changed the outcome:\n  first  %+v\n  second %+v", once, twice)
	}
}
