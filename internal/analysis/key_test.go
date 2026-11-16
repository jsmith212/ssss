package analysis

import (
	"fmt"
	"math"
	"testing"
)

type keyCase struct {
	name  string
	want  string
	audio []float32
}

// keyCorpus is every major and minor key in three textures, with and
// without drums on top.
func keyCorpus() []keyCase {
	styles := []struct {
		name  string
		style harmonyStyle
	}{{"chords", chords}, {"arp", arpeggio}, {"bass", bassline}}
	return parallelMap(12*2*len(styles)*2, func(n int) keyCase {
		withDrums := n%2 == 1
		i := n / 2 % len(styles)
		minor := n/2/len(styles)%2 == 1
		tonic := n / 2 / len(styles) / 2
		progressions := majorProgressions
		if minor {
			progressions = minorProgressions
		}
		prog := progressions[(tonic+i)%len(progressions)]
		bpm := []float64{90, 120, 140}[(tonic+i)%3]
		want := formatKey(tonic, minor)
		return keyCase{
			name:  fmt.Sprintf("%s %s prog %v drums=%v", want, styles[i].name, prog, withDrums),
			want:  want,
			audio: tonalLoop(styles[i].style, tonic, minor, prog, bpm, 4, withDrums),
		}
	})
}

// keyRelation names how a wrong answer relates to the right one.
func keyRelation(got, want string) string {
	gp, gm, ok1 := splitKey(got)
	wp, wm, ok2 := splitKey(want)
	switch {
	case got == "":
		return "none"
	case !ok1 || !ok2:
		return "invalid"
	case gp == wp && gm == wm:
		return "exact"
	case gp == wp:
		return "parallel"
	case gm != wm && ((wm && gp == (wp+3)%12) || (!wm && gp == (wp+9)%12)):
		return "relative"
	case gm == wm && (gp == (wp+7)%12 || gp == (wp+5)%12):
		return "fifth"
	}
	return "other"
}

func TestKeyDetection(t *testing.T) {
	corpus := keyCorpus()
	results := parallelMap(len(corpus), func(i int) KeyEstimate {
		return DetectKey(corpus[i].audio, testRate, false)
	})
	relations := map[string]int{}
	for i, c := range corpus {
		got := results[i]
		rel := keyRelation(got.Key, c.want)
		relations[rel]++
		if rel != "exact" {
			t.Logf("%s: got %q (%s, confidence %.2f)", c.name, got.Key, rel, got.Confidence)
		}
	}
	exact := relations["exact"]
	t.Logf("key: %d/%d exact; misses by relation: %v", exact, len(corpus), relations)
	// Relative and fifth-related answers still key-match acceptably;
	// anything else is a real error.
	if wrong := len(corpus) - exact - relations["relative"] - relations["fifth"] - relations["none"]; wrong > 0 {
		t.Errorf("%d unrelated keys detected", wrong)
	}
	if float64(exact) < 0.9*float64(len(corpus)) {
		t.Errorf("only %d/%d keys exact", exact, len(corpus))
	}
}

func TestKeyDetuned(t *testing.T) {
	// The same loop a third of a semitone flat, as from a resampled or
	// varispeed recording, by claiming a slower sample rate than it has.
	x := tonalLoop(chords, 4, true, minorProgressions[0], 120, 4, false)
	rate := 43261 // 44100 / 1.0194, 33 cents flat
	if got := DetectKey(x, rate, false); got.Key != "Em" {
		t.Errorf("detuned Em loop: got %q", got.Key)
	}
}

func TestKeySkipsUnpitched(t *testing.T) {
	noise := newSynth(3, testRate, 7)
	noise.noise(0, 0.5, 0, 3)
	cases := map[string][]float32{
		"four-on-floor": drumLoop(fourOnFloor, 124, 2, testRate, 0),
		"backbeat":      drumLoop(backbeat, 95, 2, testRate, 0),
		"breakbeat":     drumLoop(breakbeat, 170, 4, testRate, 0),
		"noise":         noise.out(),
		"silence":       make([]float32, testRate),
		"too short":     drumLoop(backbeat, 120, 1, testRate, 0)[:2000],
		"empty":         nil,
	}
	for name, x := range cases {
		if got := DetectKey(x, testRate, false); got.Key != "" {
			t.Errorf("%s: got key %q, contrast %.2f", name, got.Key, got.Contrast)
		}
	}
}

func TestKeyOneShotRoot(t *testing.T) {
	total, correct := 0, 0
	for midi := 33; midi <= 80; midi++ { // A1..G#5
		for _, kind := range []string{"pluck", "sustained"} {
			s := newSynth(1.2, testRate, uint64(midi))
			if kind == "pluck" {
				s.pluck(0, 1.2, midi, 0.5, 4)
			} else {
				s.note(0, 1.2, midi, 0.3)
			}
			total++
			got := DetectKey(s.out(), testRate, true)
			want := formatKey(midi%12, false)
			switch {
			case got.Key == want && got.RootOnly:
				correct++
			default:
				t.Errorf("%s midi %d: got %q (rootOnly=%v), want %q", kind, midi, got.Key, got.RootOnly, want)
			}
		}
	}
	t.Logf("one-shot root: %d/%d", correct, total)

}

// subBass is a decaying low note: a sine, or one driven into symmetric
// clipping, which adds odd harmonics only, so that the strongest overtone is
// a fifth (plus an octave) above the note.
func subBass(midi int, drive float64) []float32 {
	f := 440 * math.Pow(2, float64(midi-69)/12)
	s := newSynth(1.5, testRate, uint64(midi))
	s.add(0, len(s.x), func(t float64) float64 {
		// An 808's pitch drops into place over the first moments.
		phase := 2 * math.Pi * (f*t + 60*(1-math.Exp(-t*30))/30)
		v := math.Exp(-t*2.5) * sin(phase)
		if drive > 0 {
			v = math.Tanh(drive*v) / math.Tanh(drive)
		}
		return 0.8 * v
	})
	return s.out()
}

// The fundamental of an 808 or sub note lies below where chroma is
// gathered; the root must still be the note, not its loudest overtone.
func TestKeyOneShotSubBass(t *testing.T) {
	for _, drive := range []float64{0, 1.5, 4} {
		for midi := 24; midi <= 48; midi++ { // C1..C3
			got := DetectKey(subBass(midi, drive), testRate, true)
			if want := formatKey(midi%12, false); got.Key != want || !got.RootOnly {
				t.Errorf("drive %g midi %d: got %q (rootOnly=%v), want %q", drive, midi, got.Key, got.RootOnly, want)
			}
		}
	}
}

// A chord's strongest pitch class is as often its third or fifth as its
// root. A triad must come back as its key, and what cannot be named must
// come back empty rather than as a confident wrong root.
func TestKeyOneShotChords(t *testing.T) {
	type voicing struct {
		name      string
		intervals []int
		minor     bool
		rootOnly  bool // want the root alone
		optional  bool // no key is acceptable too
		never     bool // must have no key
	}
	voicings := []voicing{
		{name: "major", intervals: []int{0, 4, 7}},
		{name: "major, first inversion", intervals: []int{4, 7, 12}},
		{name: "major, second inversion", intervals: []int{7, 12, 16}},
		// Spread over the overtones of its bass note, a major chord is
		// that note: the root is right, the mode stays open.
		{name: "major, open", intervals: []int{-12, 7, 16}, rootOnly: true},
		{name: "minor", intervals: []int{0, 3, 7}, minor: true},
		{name: "minor, first inversion", intervals: []int{3, 7, 12}, minor: true},
		{name: "minor, second inversion", intervals: []int{7, 12, 15}, minor: true},
		{name: "major seventh", intervals: []int{0, 4, 7, 11}, optional: true},
		{name: "minor seventh", intervals: []int{0, 3, 7, 10}, minor: true, optional: true},
		{name: "power chord", intervals: []int{0, 7, 12}, rootOnly: true},
		{name: "octaves", intervals: []int{0, 12}, rootOnly: true},
		{name: "bare third", intervals: []int{0, 4}, never: true},
		{name: "sus4", intervals: []int{0, 5, 7}, never: true},
		{name: "diminished", intervals: []int{0, 3, 6}, never: true},
	}
	type job struct {
		v       voicing
		root    int
		sustain bool
		short   bool
	}
	var jobs []job
	for _, v := range voicings {
		for root := range 12 {
			jobs = append(jobs, job{v, root, false, false}, job{v, root, true, false}, job{v, root, false, true})
		}
	}
	results := parallelMap(len(jobs), func(i int) KeyEstimate {
		j := jobs[i]
		seconds := 1.0
		if j.short {
			seconds = 0.3 // one zero-padded frame
		}
		s := newSynth(seconds, testRate, uint64(i))
		for _, iv := range j.v.intervals {
			if j.sustain {
				s.note(0, seconds, 57+j.root+iv, 0.15)
			} else {
				s.pluck(0, seconds, 57+j.root+iv, 0.2, 5)
			}
		}
		return DetectKey(s.out(), testRate, true)
	})
	named := 0
	for i, j := range jobs {
		got := results[i]
		pc := (57 + j.root) % 12
		want := formatKey(pc, j.v.minor)
		switch {
		case j.v.never && got.Key != "":
			t.Errorf("%s on %s (job %+v): got %q, want no key", j.v.name, formatKey(pc, false), j, got.Key)
		case j.v.never, j.v.optional && got.Key == "":
		case got.Key != want || got.RootOnly != j.v.rootOnly:
			t.Errorf("%s on %s (sustain=%v short=%v): got %q (rootOnly=%v), want %q", j.v.name, formatKey(pc, false), j.sustain, j.short, got.Key, got.RootOnly, want)
		default:
			named++
		}
	}
	t.Logf("one-shot chords: %d/%d named correctly, the rest left without a key", named, len(jobs))
}

// A loop that sits on one note fits no key profile; it still has a root.
func TestKeyLoopOnOneNote(t *testing.T) {
	s := newSynth(loopSeconds(120, 8), testRate, 2)
	for i := range 16 {
		s.pluck(float64(i)*0.25, 0.25, 40, 0.4, 6) // E2 eighths
	}
	got := DetectKey(s.out(), testRate, false)
	if got.Key != "E" || !got.RootOnly {
		t.Errorf("one-note bass loop: got %q (rootOnly=%v), want the root E", got.Key, got.RootOnly)
	}
}

func TestCorrelateProfiles(t *testing.T) {
	// A chroma vector shaped exactly like a profile must correlate
	// perfectly with it at the right rotation and less anywhere else.
	for tonic := range 12 {
		var chroma [12]float64
		for i, v := range minorProfile {
			chroma[(i+tonic)%12] = v
		}
		if r := correlate(chroma, minorProfile, tonic); r < 0.999 {
			t.Errorf("tonic %d: self-correlation %g", tonic, r)
		}
		if r := correlate(chroma, minorProfile, (tonic+1)%12); r > 0.5 {
			t.Errorf("tonic %d: neighbour correlation %g", tonic, r)
		}
	}
	if r := correlate([12]float64{}, majorProfile, 0); r != 0 {
		t.Errorf("flat chroma: correlation %g", r)
	}
}
