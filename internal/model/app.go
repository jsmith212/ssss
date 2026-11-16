package model

// Library status values for AppState.Status.
const (
	StatusNone        = "none" // first run, no library chosen
	StatusOK          = "ok"
	StatusUnavailable = "unavailable"
	StatusReadOnly    = "readonly"
)

// Settings are per machine and never synced.
type Settings struct {
	Theme        string   `json:"theme"`   // system|light|dark
	Density      string   `json:"density"` // compact(28px)|regular(32px)
	PlayOnSelect bool     `json:"playOnSelect"`
	Volume       float64  `json:"volume"`     // 0..1
	ProjectBPM   float64  `json:"projectBpm"` // default 120
	ProjectKey   string   `json:"projectKey"` // "" = none
	MatchTempo   bool     `json:"matchTempo"`
	MatchKey     bool     `json:"matchKey"`
	SidebarWidth int      `json:"sidebarWidth"`
	SidebarOpen  bool     `json:"sidebarOpen"`
	PinnedTags   []string `json:"pinnedTags"` // slots 1..9
	SpliceFolder string   `json:"spliceFolder"`
}

// DefaultSettings returns the settings used on first run.
func DefaultSettings() Settings {
	return Settings{
		Theme:        "system",
		Density:      "regular",
		PlayOnSelect: true,
		Volume:       0.8,
		ProjectBPM:   120,
		SidebarWidth: 220,
		SidebarOpen:  true,
		PinnedTags:   []string{},
	}
}

// AppState describes the library and settings; pushed with the "state" event.
type AppState struct {
	Status             string   `json:"status"` // "none" (first run) | "ok" | "unavailable" | "readonly"
	LibraryPath        string   `json:"libraryPath"`
	LibraryName        string   `json:"libraryName"`
	Message            string   `json:"message"`            // why unavailable/readonly
	Platform           string   `json:"platform"`           // darwin|windows|linux
	DefaultLibraryPath string   `json:"defaultLibraryPath"` // ~/Music/Samples
	Settings           Settings `json:"settings"`
}

// DragResult reports how a native drag-out ended.
type DragResult struct {
	Started bool   `json:"started"`
	Dropped bool   `json:"dropped"`
	Error   string `json:"error"`
}
