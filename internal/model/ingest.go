package model

// Job kinds for Progress.Kind.
const (
	JobImport   = "import"
	JobSplice   = "splice"
	JobAnalysis = "analysis"
	JobRescan   = "rescan"
)

// MaxProgressErrors caps Progress.Errors.
const MaxProgressErrors = 50

// ImportTarget optionally assigns imported samples; every field may be empty.
type ImportTarget struct {
	PackID       string `json:"packId"`
	Tag          string `json:"tag"`
	CollectionID string `json:"collectionId"`
}

// Progress is the payload of the "ingest:progress" and "analysis:progress" events.
type Progress struct {
	JobID      string   `json:"jobId"`
	Kind       string   `json:"kind"` // import|splice|analysis|rescan
	Done       int      `json:"done"`
	Total      int      `json:"total"`
	Added      int      `json:"added"`
	Duplicates int      `json:"duplicates"`
	Failed     int      `json:"failed"`
	Current    string   `json:"current"`
	Finished   bool     `json:"finished"`
	Cancelled  bool     `json:"cancelled"`
	Errors     []string `json:"errors"` // capped at MaxProgressErrors, "file: reason"
}

// SplicePack is one pack found in a Splice folder.
type SplicePack struct {
	Name     string `json:"name"`
	Count    int    `json:"count"`
	Imported bool   `json:"imported"`
	Genre    string `json:"genre"`
}

// SpliceInfo is the result of looking for a local Splice installation.
type SpliceInfo struct {
	Found  bool         `json:"found"`
	Folder string       `json:"folder"`
	HasDB  bool         `json:"hasDb"`
	Packs  []SplicePack `json:"packs"`
}
