// Package splice locates a local Splice folder and its sounds database and
// reads packs and metadata from them.
package splice

import (
	"os"
	"path/filepath"
	"runtime"
)

// FolderCandidates lists where a Splice sample folder may be on this OS,
// most likely first. The location is configurable in Splice, so these are
// only the defaults; a folder from the settings goes in front.
func FolderCandidates(override string) []string {
	var out []string
	if override != "" {
		out = append(out, override)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return out
	}
	out = append(out, filepath.Join(home, "Splice"))
	switch runtime.GOOS {
	case "windows":
		out = append(out,
			filepath.Join(home, "Documents", "Splice"),
			filepath.Join(filepath.VolumeName(home)+string(filepath.Separator), "Documents", "Splice"),
		)
		if appData := os.Getenv("APPDATA"); appData != "" {
			out = append(out, filepath.Join(appData, "Splice", "Plug-in", "samples"))
		}
	case "darwin":
		out = append(out,
			filepath.Join(home, "Documents", "Splice"),
			filepath.Join(home, "Library", "Splice", "Plug-in", "samples"),
		)
	default:
		out = append(out, filepath.Join(home, "Documents", "Splice"))
	}
	return out
}

// Locate returns the first candidate folder that exists.
func Locate(override string) (string, bool) {
	for _, dir := range FolderCandidates(override) {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir, true
		}
	}
	return "", false
}

// dbRoots lists the folders under which Splice keeps
// users/default/<username>/sounds.db.
func dbRoots() []string {
	var roots []string
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		if home != "" {
			roots = append(roots, filepath.Join(home, "Library", "Application Support", "com.splice.Splice"))
		}
	case "windows":
		for _, env := range []string{"LOCALAPPDATA", "APPDATA"} {
			if dir := os.Getenv(env); dir != "" {
				roots = append(roots,
					filepath.Join(dir, "SpliceSettings"),
					filepath.Join(dir, "com.splice.Splice"),
					filepath.Join(dir, "Splice"),
				)
			}
		}
	default:
		if dir, err := os.UserConfigDir(); err == nil {
			roots = append(roots, filepath.Join(dir, "com.splice.Splice"))
		}
	}
	return roots
}

// FindDB looks for Splice's sounds.db: next to the sample folder first (a
// copied or relocated setup), then in the application's own data folders.
// With several Splice accounts on the machine the most recently written
// database wins.
func FindDB(folder string) (string, bool) {
	var patterns []string
	if folder != "" {
		patterns = append(patterns, filepath.Join(folder, "sounds.db"))
	}
	for _, root := range dbRoots() {
		patterns = append(patterns,
			filepath.Join(root, "users", "default", "*", "sounds.db"),
			filepath.Join(root, "*", "users", "default", "*", "sounds.db"),
		)
	}
	return newestMatch(patterns)
}

func newestMatch(patterns []string) (string, bool) {
	best, bestTime := "", int64(0)
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if t := info.ModTime().UnixNano(); best == "" || t > bestTime {
				best, bestTime = m, t
			}
		}
		if best != "" {
			return best, true
		}
	}
	return "", false
}
