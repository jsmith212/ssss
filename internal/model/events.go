package model

// Event names emitted from Go to the frontend.
const (
	EventState            = "state"             // AppState
	EventSamplesChanged   = "samples:changed"   // SamplesChanged
	EventIngestProgress   = "ingest:progress"   // Progress
	EventAnalysisProgress = "analysis:progress" // Progress
	EventDrop             = "drop"              // DropEvent
	EventToast            = "toast"             // Toast
)

// SamplesChanged tells the frontend which rows to refetch.
type SamplesChanged struct {
	IDs        []string `json:"ids"`        // nil => refetch everything
	Structural bool     `json:"structural"` // sidebar counts changed
}

// Drop kinds for DropEvent.Kind.
const (
	DropImport = "import"
	DropAssign = "assign"
)

// DropEvent is emitted after Go has handled an OS file drop on the window.
type DropEvent struct {
	Kind   string       `json:"kind"` // import|assign
	JobID  string       `json:"jobId,omitempty"`
	Count  int          `json:"count"`
	Target ImportTarget `json:"target"`
}

// Toast levels.
const (
	ToastInfo  = "info"
	ToastError = "error"
)

// Toast is a one-line message for the user.
type Toast struct {
	Level   string `json:"level"` // info|error
	Message string `json:"message"`
}
