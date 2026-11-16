package dragout

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"
)

func TestDefaultLabel(t *testing.T) {
	abs := func(parts ...string) string {
		return filepath.Join(append([]string{string(filepath.Separator)}, parts...)...)
	}
	tests := []struct {
		name  string
		paths []string
		want  string
	}{
		{"single file uses its name", []string{abs("lib", "Samples", "Kick 01.wav")}, "Kick 01.wav"},
		{"two files are counted", []string{abs("a.wav"), abs("b.wav")}, "2 files"},
		{"many files are counted", make([]string, 12), "12 files"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultLabel(tt.paths); got != tt.want {
				t.Errorf("defaultLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStartRejectsBadInput(t *testing.T) {
	var x int
	window := unsafe.Pointer(&x)
	abs, err := filepath.Abs("kick.wav")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		window  unsafe.Pointer
		paths   []string
		wantErr error
		wantMsg string
	}{
		{name: "no paths", window: window, wantErr: ErrNoFiles},
		{name: "relative path", window: window, paths: []string{"kick.wav"}, wantMsg: "not absolute"},
		{name: "one relative among absolute", window: window, paths: []string{abs, "x/y.wav"}, wantMsg: "not absolute"},
		{name: "nil window", paths: []string{abs}, wantErr: ErrNoWindow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dropped, err := Start(tt.window, tt.paths, "")
			if dropped {
				t.Error("dropped = true for rejected input")
			}
			switch {
			case err == nil:
				t.Fatal("expected an error")
			case tt.wantErr != nil && !errors.Is(err, tt.wantErr):
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			case tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg):
				t.Errorf("err = %v, want it to mention %q", err, tt.wantMsg)
			}
			if Active() {
				t.Error("Active() = true after a rejected Start")
			}
		})
	}
}

func TestStartRejectsConcurrentDrag(t *testing.T) {
	running.Store(true)
	defer running.Store(false)

	if !Active() {
		t.Error("Active() = false while a drag is running")
	}
	var x int
	abs, err := filepath.Abs("kick.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Start(unsafe.Pointer(&x), []string{abs}, ""); !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v, want ErrBusy", err)
	}
	if !running.Load() {
		t.Error("a rejected Start cleared the running flag of the drag in progress")
	}
}

func TestRevealRejectsBadInput(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.wav")
	tests := []struct {
		name, path, wantMsg string
	}{
		{"relative path", "kick.wav", "not absolute"},
		{"missing file", missing, "not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Reveal(tt.path)
			if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("Reveal(%q) = %v, want an error mentioning %q", tt.path, err, tt.wantMsg)
			}
		})
	}
}
