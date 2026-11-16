// Package fsx holds small file-system helpers shared by the packages that
// walk folders of audio.
package fsx

import "io/fs"

// notFile is every type bit that rules an entry out as file content.
const notFile = fs.ModeDir | fs.ModeSymlink | fs.ModeNamedPipe | fs.ModeSocket | fs.ModeDevice | fs.ModeCharDevice

// IsFile reports whether a mode describes something that can be opened and
// read as a file.
//
// It is deliberately wider than fs.FileMode.IsRegular: on Windows, Go marks
// files that carry a reparse point it does not interpret as irregular, and
// that includes files kept by cloud-sync clients such as OneDrive. They read
// like any other file, so treating them as "not a file" would hide a whole
// synced folder of samples.
func IsFile(mode fs.FileMode) bool {
	return mode&notFile == 0
}
