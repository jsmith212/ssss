package index

import (
	"strings"
	"unicode"
)

// token is one whitespace-separated term of the query string with quoting
// already resolved.
type token struct {
	text string
	neg  bool
	// literal is set when the token started with a quote: it is free text
	// even if it looks like key:value, #tag or @pack.
	literal bool
	// colon is the byte offset in text of the first colon that was typed
	// outside quotes, or -1.
	colon int
}

// lex splits a query into tokens. Double quotes group whitespace into one
// token and may start anywhere inside it (pack:"KSHMR Vol 4"); inside quotes a
// backslash escapes a quote or a backslash. An unterminated quote runs to the
// end of the input, which is what a half-typed query looks like.
func lex(s string) []token {
	var (
		toks    []token
		b       strings.Builder
		cur     = token{colon: -1}
		inTok   bool
		inQuote bool
	)
	flush := func() {
		if inTok && b.Len() > 0 {
			cur.text = b.String()
			toks = append(toks, cur)
		}
		b.Reset()
		cur = token{colon: -1}
		inTok = false
	}

	runes := []rune(strings.ToValidUTF8(s, ""))
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if unicode.IsControl(r) {
			r = ' '
		}
		switch {
		case inQuote:
			switch {
			case r == '\\' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\'):
				i++
				b.WriteRune(runes[i])
			case r == '"':
				inQuote = false
			default:
				b.WriteRune(r)
			}
		case unicode.IsSpace(r):
			flush()
		case r == '"':
			if b.Len() == 0 {
				cur.literal = true
			}
			inTok, inQuote = true, true
		case r == '-' && !inTok:
			cur.neg = true
			inTok = true
		default:
			if r == ':' && cur.colon < 0 && !cur.literal {
				cur.colon = b.Len()
			}
			inTok = true
			b.WriteRune(r)
		}
	}
	flush()
	return toks
}
