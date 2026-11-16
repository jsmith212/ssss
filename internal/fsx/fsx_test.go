package fsx

import (
	"io/fs"
	"testing"
)

func TestIsFile(t *testing.T) {
	cases := []struct {
		name string
		mode fs.FileMode
		want bool
	}{
		{"regular", 0o644, true},
		{"irregular (cloud placeholder on Windows)", fs.ModeIrregular | 0o666, true},
		{"directory", fs.ModeDir | 0o755, false},
		{"symlink", fs.ModeSymlink | 0o777, false},
		{"pipe", fs.ModeNamedPipe | 0o644, false},
		{"socket", fs.ModeSocket | 0o644, false},
		{"device", fs.ModeDevice | 0o644, false},
		{"char device", fs.ModeDevice | fs.ModeCharDevice | 0o644, false},
	}
	for _, c := range cases {
		if got := IsFile(c.mode); got != c.want {
			t.Errorf("%s: IsFile = %v, want %v", c.name, got, c.want)
		}
	}
}
