package library

import (
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

func TestSanitizeComponent(t *testing.T) {
	nfd := norm.NFD.String("Café Señor.wav")
	tests := []struct{ name, in, want string }{
		{"plain", "Kick_01.wav", "Kick_01.wav"},
		{"nfd to nfc", nfd, "Café Señor.wav"},
		{"illegal chars", `a<b>c:d"e/f\g|h?i*j.wav`, "a_b_c_d_e_f_g_h_i_j.wav"},
		{"control chars", "a\tb\x00c\x7f.wav", "a_b_c_.wav"},
		{"trailing dots and spaces", "name. . ", "name"},
		{"only dots", "...", "_"},
		{"empty", "", "_"},
		{"reserved", "CON", "CON_"},
		{"reserved lower with ext", "nul.wav", "nul_.wav"},
		{"reserved multi ext", "COM1.tar.gz", "COM1_.tar.gz"},
		{"reserved lpt", "LPT9.flac", "LPT9_.flac"},
		{"not reserved", "CONSOLE.wav", "CONSOLE.wav"},
		{"com0 is fine", "COM0.wav", "COM0.wav"},
		{"office lock prefix", "~$kick.wav", "_$kick.wav"},
		{"dot tilde prefix", ".~lock.wav", "_~lock.wav"},
		{"appledouble prefix", "._kick.wav", "__kick.wav"},
		{"trailing tilde", "kick.wav~", "kick.wav_"},
		{"invalid utf8", "a\xffb.wav", "a_b.wav"},
		{"hidden file kept", ".hidden.wav", ".hidden.wav"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeComponent(tt.in)
			if got != tt.want {
				t.Errorf("SanitizeComponent(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if again := SanitizeComponent(got); again != got {
				t.Errorf("not idempotent: %q -> %q", got, again)
			}
		})
	}
}

func TestSanitizeComponentLength(t *testing.T) {
	for _, in := range []string{
		strings.Repeat("a", 300) + ".wav",
		strings.Repeat("é", 200) + ".flac",
		strings.Repeat("日本語", 80) + ".aiff",
		strings.Repeat("x", 300),
	} {
		got := SanitizeComponent(in)
		if len(got) > maxComponentBytes {
			t.Errorf("%d bytes, want <= %d: %q", len(got), maxComponentBytes, got)
		}
		if !utf8.ValidString(got) {
			t.Errorf("invalid utf8: %q", got)
		}
		if i := strings.LastIndex(in, "."); i >= 0 && !strings.HasSuffix(got, in[i:]) {
			t.Errorf("extension lost: %q", got)
		}
		if !strings.Contains(got, truncMark) {
			t.Errorf("no truncation mark in %q", got)
		}
	}
}

func TestSanitizeRelPath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Drums/Kicks/Kick_01.wav", "Drums/Kicks/Kick_01.wav"},
		{"/Drums//Kicks/./Kick.wav", "Drums/Kicks/Kick.wav"},
		{"../../etc/passwd", "etc/passwd"},
		{"", "_"},
		{"a:b/c?.wav", "a_b/c_.wav"},
		{"Drums./ Kicks /x.wav", "Drums/ Kicks/x.wav"},
		{`back\slash.wav`, "back_slash.wav"},
	}
	for _, tt := range tests {
		if got := SanitizeRelPath(tt.in); got != tt.want {
			t.Errorf("SanitizeRelPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSanitizeRelPathLength(t *testing.T) {
	long := strings.Repeat("d", 100)
	tests := []struct {
		name string
		in   string
	}{
		{"long file", "Drums/" + strings.Repeat("k", 119) + ".wav"},
		{"long dirs", long + "/" + long + "/" + long + "/kick.wav"},
		{"many short dirs", strings.Repeat("ab/", 120) + "kick.wav"},
		{"everything long", long + "/" + long + "/" + strings.Repeat("k", 110) + ".wav"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeRelPath(tt.in)
			if n := utf8.RuneCountInString(got); n > maxRelPathChars {
				t.Errorf("%d chars, want <= %d: %q", n, maxRelPathChars, got)
			}
			if !strings.HasSuffix(got, ".wav") {
				t.Errorf("extension lost: %q", got)
			}
			if strings.Contains(got, "//") || strings.HasPrefix(got, "/") {
				t.Errorf("malformed path %q", got)
			}
			if again := SanitizeRelPath(got); again != got {
				t.Errorf("not idempotent: %q -> %q", got, again)
			}
		})
	}
	if got := sanitizeRelPath(strings.Repeat("ab/", 120)+"kick.wav", 40); utf8.RuneCountInString(got) > 40 {
		t.Errorf("small limit not honoured: %q", got)
	}
}

func TestPathKey(t *testing.T) {
	a := PathKey(norm.NFD.String("Drums/Café.WAV"))
	b := PathKey("drums/café.wav")
	if a != b {
		t.Errorf("keys differ: %q vs %q", a, b)
	}
}

func TestWithSuffix(t *testing.T) {
	tests := []struct {
		rel  string
		n    int
		want string
	}{
		{"a/kick.wav", 1, "a/kick.wav"},
		{"a/kick.wav", 2, "a/kick (2).wav"},
		{"kick", 3, "kick (3)"},
		{".hidden", 2, ".hidden (2)"},
	}
	for _, tt := range tests {
		if got := withSuffix(tt.rel, tt.n, maxRelPathChars); got != tt.want {
			t.Errorf("withSuffix(%q, %d) = %q, want %q", tt.rel, tt.n, got, tt.want)
		}
	}
	long := strings.Repeat("k", 116) + ".wav"
	got := withSuffix(long, 2, maxRelPathChars)
	if len(got) > maxComponentBytes || !strings.HasSuffix(got, " (2).wav") {
		t.Errorf("long name suffix: %q", got)
	}
}

func TestNormalizeTag(t *testing.T) {
	tests := []struct{ in, want string }{
		{"drums/kick", "drums/kick"},
		{" Drums / Kick ", "drums/kick"},
		{"#Drums", "drums"},
		{"//a///b/", "a/b"},
		{"hi  hat", "hi hat"},
		{norm.NFD.String("Café"), "café"},
		{"  ", ""},
		{"/", ""},
	}
	for _, tt := range tests {
		if got := NormalizeTag(tt.in); got != tt.want {
			t.Errorf("NormalizeTag(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestConflictNames(t *testing.T) {
	tests := []struct {
		name     string
		conflict bool
		stem     string
	}{
		{"pk_01ABC.json", false, "pk_01ABC"},
		{"pk_01ABC (conflicted copy 2026-11-15 101500).json", true, "pk_01ABC"},
		{"pk_01ABC (Ann's conflicted copy 2026-11-15).json", true, "pk_01ABC"},
		{"pk_01ABC.sync-conflict-20261115-101500-ABCDEFG.json", true, "pk_01ABC"},
		{"pk_01ABC (conflict 2).json", true, "pk_01ABC"},
		{"tags (conflicted copy).json", true, "tags"},
		{"pk_01ABC (conflicted copy).txt", false, ""},
	}
	for _, tt := range tests {
		if got := isConflictCopy(tt.name); got != tt.conflict {
			t.Errorf("isConflictCopy(%q) = %v", tt.name, got)
		}
		if tt.stem != "" {
			if got := conflictStem(tt.name); got != tt.stem {
				t.Errorf("conflictStem(%q) = %q, want %q", tt.name, got, tt.stem)
			}
		}
	}
}
