//go:build linux

package dragout

import "testing"

func TestFileURI(t *testing.T) {
	tests := []struct {
		path, want string
	}{
		{"/home/u/Samples/kick.wav", "file:///home/u/Samples/kick.wav"},
		{"/lib/My Pack/Kick 01 #1.wav", "file:///lib/My%20Pack/Kick%2001%20%231.wav"},
		{"/lib/100%/a?b.wav", "file:///lib/100%25/a%3Fb.wav"},
		{"/lib/Schöne Töne/ä.flac", "file:///lib/Sch%C3%B6ne%20T%C3%B6ne/%C3%A4.flac"},
	}
	for _, tt := range tests {
		if got := fileURI(tt.path); got != tt.want {
			t.Errorf("fileURI(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
