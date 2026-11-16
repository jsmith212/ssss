package library

import (
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	maxComponentBytes = 120
	maxRelPathChars   = 200
	minComponentChars = 8
	truncMark         = "~"
)

var reservedNames = func() map[string]struct{} {
	m := map[string]struct{}{"CON": {}, "PRN": {}, "AUX": {}, "NUL": {}}
	for _, d := range "123456789" {
		m["COM"+string(d)] = struct{}{}
		m["LPT"+string(d)] = struct{}{}
	}
	return m
}()

// PathKey is the collision key of a slash-separated path: two paths with the
// same key cannot coexist on every OS and sync client we target.
func PathKey(p string) string {
	return strings.ToLower(norm.NFC.String(p))
}

// SanitizeComponent makes one file or directory name safe on Windows, macOS,
// Linux and the sync clients' exclude lists. It never returns "".
func SanitizeComponent(name string) string {
	s := norm.NFC.String(strings.ToValidUTF8(name, "_"))
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, s)
	s = strings.TrimRight(s, ". ")
	if strings.HasSuffix(s, "~") {
		s = s[:len(s)-1] + "_"
	}
	// Sync clients skip office lock files and AppleDouble forks by prefix.
	for strings.HasPrefix(s, "~$") || strings.HasPrefix(s, ".~") || strings.HasPrefix(s, "._") {
		s = "_" + s[1:]
	}
	if s == "" {
		return "_"
	}
	// Windows reserves these device names whatever follows the first dot.
	base, rest, hasDot := strings.Cut(s, ".")
	if _, ok := reservedNames[strings.ToUpper(strings.TrimRight(base, " "))]; ok {
		s = base + "_"
		if hasDot {
			s += "." + rest
		}
	}
	return truncMiddle(s, maxComponentBytes, maxRelPathChars)
}

// SanitizeRelPath sanitises every component of a slash-separated relative
// path and keeps the whole within the library's length budget.
func SanitizeRelPath(p string) string {
	return sanitizeRelPath(p, maxRelPathChars)
}

func sanitizeRelPath(p string, limit int) string {
	var comps []string
	for c := range strings.SplitSeq(p, "/") {
		if c == "" || c == "." || c == ".." {
			continue
		}
		comps = append(comps, SanitizeComponent(c))
	}
	if len(comps) == 0 {
		return "_"
	}
	for {
		total := utf8.RuneCountInString(strings.Join(comps, "/"))
		if total <= limit {
			break
		}
		longest, longestLen := 0, 0
		for i, c := range comps {
			if n := utf8.RuneCountInString(c); n > longestLen {
				longest, longestLen = i, n
			}
		}
		target := max(minComponentChars, longestLen-(total-limit))
		short := truncMiddle(comps[longest], maxComponentBytes, target)
		switch {
		case utf8.RuneCountInString(short) < longestLen:
			comps[longest] = short
		case len(comps) > 1:
			// Nothing left to shorten: give up a directory level, keeping
			// the top folder and the file name.
			mid := (len(comps) - 1) / 2
			comps = append(comps[:mid], comps[mid+1:]...)
		default:
			return strings.Join(comps, "/")
		}
	}
	return strings.Join(comps, "/")
}

// truncMiddle cuts runes out of the middle of the stem until name fits both
// limits; the extension survives because DAWs and our decoders rely on it.
func truncMiddle(name string, maxBytes, maxRunes int) string {
	fits := func(s string) bool {
		return len(s) <= maxBytes && utf8.RuneCountInString(s) <= maxRunes
	}
	if fits(name) {
		return name
	}
	ext := path.Ext(name)
	if len(ext) > 16 || ext == name {
		ext = ""
	}
	stem := []rune(strings.TrimSuffix(name, ext))
	for keep := len(stem) - 1; keep >= 2; keep-- {
		head := (keep + 1) / 2
		tail := keep - head
		cand := string(stem[:head]) + truncMark + string(stem[len(stem)-tail:]) + ext
		if fits(cand) {
			return cand
		}
	}
	out := []rune(name)
	for len(out) > 1 && !fits(string(out)) {
		out = out[:len(out)-1]
	}
	return string(out)
}

// withSuffix returns rel with " (n)" inserted before the extension.
func withSuffix(rel string, n int, limit int) string {
	if n <= 1 {
		return rel
	}
	ext := path.Ext(rel)
	if len(ext) > 16 || ext == path.Base(rel) {
		ext = ""
	}
	return sanitizeRelPath(strings.TrimSuffix(rel, ext)+" ("+strconv.Itoa(n)+")"+ext, limit)
}

// NormalizeTag canonicalises a tag path: NFC, lower case, segments trimmed,
// empty segments dropped. Tags are compared case-insensitively, so storing
// one spelling keeps every pack file and the index in agreement. It returns
// "" for a tag with no usable segment.
func NormalizeTag(tag string) string {
	tag = strings.ToLower(norm.NFC.String(strings.ToValidUTF8(tag, "")))
	var segs []string
	for seg := range strings.SplitSeq(tag, "/") {
		seg = strings.Join(strings.FieldsFunc(seg, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r)
		}), " ")
		seg = strings.TrimLeft(seg, "#")
		if seg != "" {
			segs = append(segs, seg)
		}
	}
	return strings.Join(segs, "/")
}

func normalizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := map[string]struct{}{}
	for _, t := range tags {
		t = NormalizeTag(t)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}
