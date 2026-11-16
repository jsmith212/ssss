package analysis

import "math"

const (
	// noteMinHz is the lowest partial read for notes: just under A0, so
	// that the fundamental of a sub-bass note (C1 is 32.7 Hz) is seen and
	// not only its overtones.
	noteMinHz = 26.0
	// noteBins is the length of a noteSpectrum: C0 to past chromaMaxHz.
	noteBins = 86 * fineBins
	// noteHarmonics is how many partials of a note are looked for. Beyond
	// ten they are closer together than the tolerance and say nothing.
	noteHarmonics = 10
	// noteTolerance is how far, in histogram bins, a partial may sit from
	// its harmonic position: 0.4 semitones, for the resolution of the
	// lowest bins, detuned layers and stretched strings.
	noteTolerance = 4
	// loopNoteFrame is the frame length, in samples at keyRate, that notes
	// in a loop are read with: under 200 ms.
	loopNoteFrame = keyFFT / 4
	// maxNotes is how many notes are separated before the sound is given
	// up as too dense to name.
	maxNotes = 4
	// minFundamental is the least a note's first partial must hold of its
	// strongest. It keeps out the phantom an octave or two below a chord,
	// whose overtones the chord's notes happen to be.
	minFundamental = 0.1
	// minNoteShare is the least of the spectrum a further note must explain
	// to count as one, dominantShare what a note must explain to be the
	// root on its own, and maxUnexplained how much may be left over when
	// several notes are read as a chord.
	minNoteShare   = 0.1
	dominantShare  = 0.6
	maxUnexplained = 0.3
)

// noteSpectrum collects spectral peak weight by pitch above C0, fineBins to
// the semitone, octaves kept apart.
type noteSpectrum [noteBins]float64

// harmonicBins[h-1] is how many bins above its fundamental partial h lies.
var harmonicBins = func() (out [noteHarmonics]float64) {
	for h := range out {
		out[h] = 12 * fineBins * math.Log2(float64(h+1))
	}
	return out
}()

// note is one harmonic series found in a noteSpectrum.
type note struct {
	pitch  float64 // semitones above C0, from all its partials
	weight float64 // spectral weight it explains
}

// partial returns the weight within noteTolerance of bin centre and the sum
// of weight times bin position, for locating the partial precisely.
func (s *noteSpectrum) partial(centre float64) (weight, moment float64) {
	c := int(math.Round(centre))
	for b := max(0, c-noteTolerance); b <= min(noteBins-1, c+noteTolerance); b++ {
		weight += s[b]
		moment += s[b] * float64(b)
	}
	return weight, moment
}

// strongest finds the fundamental whose harmonic series explains the most
// of the spectrum. Only pitches with a peak of their own are considered:
// that is what tells a note from the common sub-harmonic of several.
func (s *noteSpectrum) strongest() (best note, ok bool) {
	for b, w := range s {
		if w == 0 {
			continue
		}
		var weights [noteHarmonics]float64
		sum, top, offset := 0.0, 0.0, 0.0
		for h, bins := range harmonicBins {
			weight, moment := s.partial(float64(b) + bins)
			weights[h] = weight
			sum += weight
			top = math.Max(top, weight)
			offset += moment - weight*(float64(b)+bins)
		}
		if weights[0] < minFundamental*top || sum <= best.weight {
			continue
		}
		best = note{pitch: (float64(b) + offset/sum) / fineBins, weight: sum}
		ok = true
	}
	return best, ok
}

// remove takes a note's partials out of the spectrum.
func (s *noteSpectrum) remove(n note) {
	for _, bins := range harmonicBins {
		c := int(math.Round(n.pitch*fineBins + bins))
		for b := max(0, c-noteTolerance); b <= min(noteBins-1, c+noteTolerance); b++ {
			s[b] = 0
		}
	}
}

// notes separates the spectrum into harmonic series, strongest first, and
// returns them with the share of the spectrum none of them explains.
func (s noteSpectrum) notes() (found []note, unexplained float64) {
	total := 0.0
	for _, w := range s {
		total += w
	}
	if total == 0 {
		return nil, 1
	}
	left := total
	for len(found) < maxNotes {
		n, ok := s.strongest()
		if !ok || n.weight < minNoteShare*total {
			break
		}
		n.weight /= total
		found = append(found, n)
		s.remove(n)
		left = 0
		for _, w := range s {
			left += w
		}
	}
	return found, left / total
}

func (n note) pitchClass() int { return ((int(math.Round(n.pitch)) % 12) + 12) % 12 }

// estimateNotes names a sound by the notes in it. One note that accounts
// for most of the spectrum is the root, with the mode left open. In a
// one-shot, a major or minor triad, alone or under a seventh, gives its key
// and a bare fifth its root; a loop is only asked for a single root. Anything else (other chords, clusters,
// inharmonic or noisy sounds) gets no key: the strongest pitch class of a chord is as often its
// third or fifth as its root, and a wrong key is worse than none.
func estimateNotes(x []float64, rate int, oneShot bool) KeyEstimate {
	if float64(len(x)) < minKeySeconds*float64(rate) {
		return KeyEstimate{}
	}
	// A one-shot is one event and gets the full frequency resolution. In a
	// loop a frame that long spans several notes, whose repetition splits
	// every partial into side bands a rhythm apart, far enough from a low
	// partial to pass for other notes. Short frames see one note at a time.
	frame := keyFFT
	if !oneShot {
		frame = loopNoteFrame
	}
	var spectrum noteSpectrum
	newChromaBuilder(rate, noteMinHz).addAll(x, spectrum[:], frame)
	var chroma [12]float64
	for b, w := range spectrum {
		chroma[int(math.Round(float64(b)/fineBins))%12] += w
	}
	contrast, _ := chromaContrast(chroma)
	est := KeyEstimate{Contrast: contrast}
	if contrast < minRootContrast {
		return est
	}

	found, unexplained := spectrum.notes()
	if len(found) == 0 {
		return est
	}
	if found[0].weight >= dominantShare {
		est.Key, est.RootOnly = formatKey(found[0].pitchClass(), false), true
		est.Confidence = math.Min(1, found[0].weight)
		return est
	}
	if !oneShot || unexplained > maxUnexplained {
		return est
	}

	var present [12]bool
	classes, lowest := 0, found[0]
	for _, n := range found {
		if pc := n.pitchClass(); !present[pc] {
			present[pc] = true
			classes++
		}
		if n.pitch < lowest.pitch {
			lowest = n
		}
	}
	for root := range 12 {
		if !present[root] || !present[(root+7)%12] {
			continue
		}
		major, minor := present[(root+4)%12], present[(root+3)%12]
		switch {
		case classes == 2:
			est.Key, est.RootOnly = formatKey(root, false), true
		case major == minor:
			continue
		case classes == 3:
			est.Key = formatKey(root, minor)
		case classes == 4 && root == lowest.pitchClass():
			// A seventh chord holds two triads (Am7 is A minor and C
			// major); the one on its bass note names it.
			est.Key = formatKey(root, minor)
		default:
			continue
		}
		est.Confidence = 0.6 * (1 - unexplained)
		break
	}
	return est
}
