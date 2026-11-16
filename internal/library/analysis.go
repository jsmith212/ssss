package library

import (
	"fmt"
	"time"

	"ssss/internal/model"
)

// Analysis is what ingest or (re-)analysis learned about a file. Zero
// values leave the corresponding record fields untouched.
type Analysis struct {
	// Size and MTime are the file as analysed.
	Size  int64
	MTime time.Time
	Fmt   string
	Dur   float64
	SR    int
	Ch    int
	Mini  string // base64 of model.MiniPeaks uint8 buckets

	// Detected replaces the record's "detected" block when non-nil.
	Detected *Detected

	// Candidate effective values with their source. Each replaces the
	// current value only if its source ranks at least as high, and never
	// when the current value came from the user.
	BPM     float64
	BPMSrc  string
	Key     string
	KeySrc  string
	Type    string
	TypeSrc string
}

func sourceRank(src string) int {
	switch src {
	case model.SrcUser:
		return 4
	case model.SrcSplice:
		return 3
	case model.SrcFilename:
		return 2
	case model.SrcDetected:
		return 1
	}
	return 0
}

// supersedes reports whether a candidate from newSrc may replace the current
// value (curUnset tells whether there is one) from curSrc.
func supersedes(newUnset bool, newSrc string, curUnset bool, curSrc string) bool {
	switch {
	case newUnset, curSrc == model.SrcUser:
		return false
	case curUnset:
		return true
	}
	return sourceRank(newSrc) >= sourceRank(curSrc)
}

// ApplyAnalysis merges an analysis result into a sample and marks it
// analysed. Not undoable.
func (l *Library) ApplyAnalysis(id string, a Analysis) (Change, error) {
	return l.mutate(func(t *tx) error {
		p := l.st.byID[id]
		if p == nil {
			return fmt.Errorf("sample %s: %w", id, ErrNotFound)
		}
		rec := p.Samples[id]
		if a.Size > 0 {
			rec.Size = a.Size
		}
		if !a.MTime.IsZero() {
			rec.MTime = a.MTime
		}
		if a.Fmt != "" {
			rec.Fmt = a.Fmt
		}
		if a.Dur > 0 {
			rec.Dur = a.Dur
		}
		if a.SR > 0 {
			rec.SR = a.SR
		}
		if a.Ch > 0 {
			rec.Ch = a.Ch
		}
		if a.Mini != "" {
			rec.Mini = a.Mini
		}
		if a.Detected != nil {
			d := *a.Detected
			if rec.Detected != nil && d.extra == nil {
				d.extra = rec.Detected.extra
			}
			rec.Detected = &d
		}
		if supersedes(a.BPM <= 0, a.BPMSrc, rec.BPM <= 0, rec.BPMSrc) {
			rec.BPM, rec.BPMSrc = a.BPM, a.BPMSrc
		}
		if supersedes(a.Key == "", a.KeySrc, rec.Key == "", rec.KeySrc) {
			rec.Key, rec.KeySrc = a.Key, a.KeySrc
		}
		if supersedes(a.Type == "", a.TypeSrc, rec.Type == "", rec.TypeSrc) {
			rec.Type, rec.TypeSrc = a.Type, a.TypeSrc
		}
		rec.Analyzed = true
		// No new revision: analysis is derived from the file and can be
		// redone anywhere. If it counted, a machine that merely analysed a
		// sample would beat an older but deliberate edit (a tag, a
		// favourite) made on another machine when the two are merged.
		l.dirtyPack(p.ID)
		t.touch(id)
		return nil
	})
}

// FileChanged records that a sample's file was replaced in place with other
// content (edited in an audio editor, overwritten by a sync client). The
// sample keeps its ID, tags and everything else a person gave it; what was
// derived from the old audio is dropped so that analysis fills it in again.
// Not undoable.
func (l *Library) FileChanged(id string, size int64, mtime time.Time) (Change, error) {
	ch, err := l.mutate(func(t *tx) error {
		p := l.st.byID[id]
		if p == nil {
			return fmt.Errorf("sample %s: %w", id, ErrNotFound)
		}
		rec := p.Samples[id]
		rec.Size, rec.MTime = size, mtime
		rec.Analyzed, rec.Mini, rec.Detected = false, "", nil
		rec.Dur, rec.SR, rec.Ch = 0, 0, 0
		if rec.BPMSrc == model.SrcDetected {
			rec.BPM, rec.BPMSrc = 0, ""
		}
		if rec.KeySrc == model.SrcDetected {
			rec.Key, rec.KeySrc = "", ""
		}
		if rec.TypeSrc == model.SrcDetected {
			rec.Type, rec.TypeSrc = "", ""
		}
		rec.Rev = now()
		l.dirtyPack(p.ID)
		t.touch(id)
		return nil
	})
	return ch, err
}
