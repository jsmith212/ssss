package main

import (
	"embed"
	"log"
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"ssss/internal/api"
)

//go:embed all:frontend/dist
var assets embed.FS

// instanceID is the application id: the product identifier of
// build/config.yml, the Flatpak app id, the GTK application id and the name
// used for the single-instance handover.
const instanceID = "com.ssss.app"

// optionalScript answers the Wails runtime's request for /wails/custom.js,
// which it makes on every start. Outside server mode Wails replies 404, and
// the webview logs that as an error each time; an empty script is the same
// thing without the noise. It runs ahead of Wails' own handling of the path.
func optionalScript(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wails/custom.js" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
	})
}

func main() {
	service := api.New()

	// /audio/ and /peaks/ share the webview's origin with the frontend, so
	// fetches of PCM and peak data need no CORS and no extra port. They are
	// mounted ahead of the catch-all that serves the embedded frontend.
	mux := http.NewServeMux()
	dynamic := api.AssetHandler(service)
	mux.Handle("/audio/", dynamic)
	mux.Handle("/peaks/", dynamic)
	mux.Handle("/", application.AssetFileServerFS(assets))

	// A second launch hands over to the running copy and exits: two copies
	// would each keep their own idea of the library and write it over the
	// other's. (The cache lock in the service catches what gets past this,
	// such as a copy started without a session bus on Linux.)
	var window *application.WebviewWindow
	app := application.New(application.Options{
		Name:        "ssss",
		Description: "Super Simple Sample Station",
		SingleInstance: singleInstance(func(application.SecondInstanceData) {
			if window == nil {
				return
			}
			if window.IsMinimised() {
				window.UnMinimise()
			}
			window.Show()
			window.Focus()
		}),
		Services: []application.Service{
			application.NewService(service),
		},
		Assets: application.AssetOptions{
			Handler:    mux,
			Middleware: optionalScript,
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Linux: application.LinuxOptions{
			// Must equal the Flatpak app id (build/flatpak): inside the
			// sandbox WebKit may only own bus names under that id, and its
			// web process aborts when the two differ.
			ApplicationID: instanceID,
		},
		OnShutdown: func() { api.Shutdown(service) },
	})

	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:          "ssss",
		Width:          1280,
		Height:         800,
		MinWidth:       720,
		MinHeight:      480,
		URL:            "/",
		EnableFileDrop: true,
		// Matches --bg-0 of the dark theme so the window does not flash
		// white before the frontend paints.
		BackgroundColour: application.NewRGB(0x11, 0x11, 0x13),
		Windows: application.WindowsWindow{
			DisableMenu: true,
		},
	})

	api.Attach(service, app, window)
	// Opens the last library before the window loads, so the frontend's
	// first GetState already describes it.
	api.Start(service)

	window.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		paths := e.Context().DroppedFiles()
		if len(paths) == 0 {
			return
		}
		var target api.DropTarget
		if details := e.Context().DropTargetDetails(); details != nil {
			target.Kind = details.Attributes["data-drop-kind"]
			target.ID = details.Attributes["data-drop-id"]
		}
		// The callback runs on the event dispatch path; importing can take
		// minutes.
		go api.HandleFileDrop(service, paths, target)
	})

	// Coming back to the window is when files synced or moved by other
	// programs should show up; the service debounces the rescan.
	window.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) {
		api.WindowFocused(service)
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
