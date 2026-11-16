package api

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ssss/internal/dragout"
	"ssss/internal/library"
	"ssss/internal/model"
)

// existingPaths returns the files of the samples that are on disk, in the
// order asked.
func existingPaths(lib *library.Library, ids []string) []string {
	paths := make([]string, 0, len(ids))
	for _, id := range ids {
		abs, ok := lib.AbsPath(id)
		if !ok {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			paths = append(paths, abs)
		}
	}
	return paths
}

// StartDrag starts a native file drag of the samples' original files and
// blocks until the drag ends.
func (a *API) StartDrag(ids []string) model.DragResult {
	s, err := a.open()
	if err != nil {
		return model.DragResult{Error: err.Error()}
	}
	paths := existingPaths(s.lib, ids)
	if len(paths) == 0 {
		return model.DragResult{Error: "the files are not on this computer"}
	}
	if a.window == nil {
		return model.DragResult{Error: errNoWindow.Error()}
	}
	dropped, err := dragout.Start(a.window.NativeWindow(), paths, "")
	switch {
	case errors.Is(err, dragout.ErrButtonReleased), errors.Is(err, dragout.ErrBusy):
		// The button came up before the drag could begin (a quick flick),
		// or one is already under way. Nothing failed and there is nothing
		// to tell the user: not started, no error.
		return model.DragResult{}
	case err != nil:
		return model.DragResult{Error: err.Error()}
	}
	return model.DragResult{Started: true, Dropped: dropped}
}

// Reveal shows the sample's file in the system file manager.
func (a *API) Reveal(id string) error {
	s, err := a.open()
	if err != nil {
		return err
	}
	abs, ok := s.lib.AbsPath(id)
	if !ok {
		return fmt.Errorf("sample %s: %w", id, library.ErrNotFound)
	}
	return dragout.Reveal(abs)
}

// CopyPaths puts the absolute paths of the samples on the clipboard, one per
// line.
func (a *API) CopyPaths(ids []string) error {
	s, err := a.open()
	if err != nil {
		return err
	}
	var paths []string
	for _, id := range ids {
		if abs, ok := s.lib.AbsPath(id); ok {
			paths = append(paths, abs)
		}
	}
	if len(paths) == 0 {
		return errors.New("nothing to copy")
	}
	if a.app == nil {
		return errNoWindow
	}
	if !a.app.Clipboard.SetText(strings.Join(paths, "\n")) {
		return errors.New("could not write to the clipboard")
	}
	return nil
}
