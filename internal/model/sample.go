package model

// Sample type values.
const (
	TypeLoop    = "loop"
	TypeOneShot = "oneshot"
)

// Metadata source values, highest priority first. A rescan or re-analysis
// never overwrites a value whose source is SrcUser.
const (
	SrcUser     = "user"
	SrcSplice   = "splice"
	SrcFilename = "filename"
	SrcDetected = "detected"
)

// MiniPeaks is the number of uint8 buckets in Sample.Mini.
const MiniPeaks = 96

// Sample is one row of the sample list.
type Sample struct {
	ID       string   `json:"id"`
	PackID   string   `json:"packId"`
	PackName string   `json:"packName"`
	Name     string   `json:"name"`    // file name without extension
	Ext      string   `json:"ext"`     // "wav"
	RelPath  string   `json:"relPath"` // path inside the pack dir, slash-separated
	Size     int64    `json:"size"`
	Dur      float64  `json:"dur"` // seconds
	SR       int      `json:"sr"`
	Ch       int      `json:"ch"`
	Type     string   `json:"type"`   // "loop" | "oneshot" | ""
	BPM      float64  `json:"bpm"`    // 0 = unknown
	BPMSrc   string   `json:"bpmSrc"` // user|splice|filename|detected|""
	Key      string   `json:"key"`    // "Am", "F#", "" = unknown
	KeySrc   string   `json:"keySrc"`
	KeyRoot  bool     `json:"keyRoot"` // key is a detected root note, mode unknown: it fits "A" and "Am" alike
	Tags     []string `json:"tags"`
	Fav      bool     `json:"fav"`
	Added    int64    `json:"added"` // unix ms
	Missing  bool     `json:"missing"`
	Analyzed bool     `json:"analyzed"`
	Mini     string   `json:"mini"` // base64, 96 uint8 peak buckets (0..255), "" until analysed
}

// SampleDetail is a Sample plus the fields only the info popover needs.
type SampleDetail struct {
	Sample
	AbsPath       string   `json:"absPath"`
	Collections   []string `json:"collections"` // collection ids
	Note          string   `json:"note"`
	SuggestedTags []string `json:"suggestedTags"` // from filename/folders, not yet applied
}

// MetaPatch edits sample metadata; nil fields are left untouched.
type MetaPatch struct {
	BPM  *float64 `json:"bpm"`
	Key  *string  `json:"key"`
	Type *string  `json:"type"`
	Note *string  `json:"note"`
}
