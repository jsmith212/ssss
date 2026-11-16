package api

import (
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"

	"ssss/internal/model"
)

// Registering the payload types lets the binding generator emit typed events
// for the frontend. Wails then checks the type on every emit: the data must
// be exactly the registered type (a value, not a pointer).
func init() {
	application.RegisterEvent[model.AppState](model.EventState)
	application.RegisterEvent[model.SamplesChanged](model.EventSamplesChanged)
	application.RegisterEvent[model.Progress](model.EventIngestProgress)
	application.RegisterEvent[model.Progress](model.EventAnalysisProgress)
	application.RegisterEvent[model.DropEvent](model.EventDrop)
	application.RegisterEvent[model.Toast](model.EventToast)
}

// DropTarget identifies the element an OS file drop landed on, taken from its
// data-drop-kind and data-drop-id attributes.
type DropTarget struct {
	Kind string // pack|tag|collection|list
	ID   string
}

// Attach gives the service the application and window it needs for events,
// dialogs, the clipboard and native drags. Call it before Start.
func Attach(a *API, app *application.App, window *application.WebviewWindow) {
	a.app = app
	a.window = window
}

// Start loads the settings and opens the library used last. It returns once
// the library's state is known, so the frontend's first GetState is right;
// index sync, rescan and analysis carry on in the background.
func Start(a *API) {
	// The window does not exist yet and events sent now reach nobody, so
	// what startup has to tell the user is kept for the first GetState.
	a.toastMu.Lock()
	a.holding = true
	a.toastMu.Unlock()
	a.start()
}

// AssetHandler serves GET /audio/{id}.f32 and GET /peaks/{id}.
func AssetHandler(a *API) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/audio/{file}", a.audio.AudioHandler())
	mux.Handle("/peaks/{id}", a.audio.PeaksHandler())
	return mux
}

// HandleFileDrop reacts to files dropped on the window from the OS.
func HandleFileDrop(a *API, paths []string, target DropTarget) {
	a.handleFileDrop(paths, target)
}

// WindowFocused tells the service the window gained focus, which triggers a
// debounced rescan.
func WindowFocused(a *API) {
	a.windowFocused()
}

// Shutdown flushes pending writes. Call it from the application's OnShutdown.
func Shutdown(a *API) {
	a.shutdown()
}

// emit sends an event to the frontend. data must be the exact type
// registered for name in init.
func (a *API) emit(name string, data any) {
	if a.closing.Load() {
		return
	}
	if a.sink != nil {
		a.sink(name, data)
	}
	if a.app != nil {
		a.app.Event.Emit(name, data)
	}
}

func (a *API) toast(level, message string) {
	t := model.Toast{Level: level, Message: message}
	a.toastMu.Lock()
	if a.holding {
		a.held = append(a.held, t)
		a.toastMu.Unlock()
		return
	}
	a.toastMu.Unlock()
	a.emit(model.EventToast, t)
}

// releaseToasts delivers the toasts held back during startup.
func (a *API) releaseToasts() {
	a.toastMu.Lock()
	held := a.held
	a.holding, a.held = false, nil
	a.toastMu.Unlock()
	for _, t := range held {
		a.emit(model.EventToast, t)
	}
}

func (a *API) toastError(err error) {
	if err != nil {
		a.toast(model.ToastError, sentence(err.Error()))
	}
}

// sentence capitalises the first letter of an error message for display.
func sentence(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}
