package index

import (
	"math"
	"strings"
	"unicode/utf8"

	"ssss/internal/model"
)

// trigramMin is the shortest term the FTS5 trigram index can match; shorter
// free-text terms fall back to LIKE.
const trigramMin = 3

// relevanceMax is the largest result set that is ranked. Ranking costs about
// a microsecond per match, and a term that matches a large part of the
// library ("wav", a pack-name fragment) has no meaningful best hit anyway,
// so bigger results are listed newest first.
const relevanceMax = 20_000

// filter is a Query compiled to SQL over "sample s". Every value the user
// typed travels in args; the SQL text is assembled from constants only.
type filter struct {
	conds []string
	args  []any
	// match is the FTS5 expression for the positive free-text terms the
	// trigram index can serve, "" when there are none. It is kept apart
	// from conds because a relevance sort needs it as a join instead.
	match string
}

const ftsSubquery = `(SELECT rowid FROM sample_fts WHERE sample_fts MATCH ?)`

func compile(q Query) filter {
	var (
		f       filter
		phrases []string
	)
	for _, t := range q.Terms {
		if t.Kind == KindText && !t.Neg && utf8.RuneCountInString(t.Text) >= trigramMin {
			phrases = append(phrases, ftsPhrase(t.Text))
			continue
		}
		cond, args := termSQL(t)
		if cond == "" {
			continue
		}
		if t.Neg {
			cond = "NOT " + cond
		}
		f.conds = append(f.conds, cond)
		f.args = append(f.args, args...)
	}
	f.match = strings.Join(phrases, " AND ")
	return f
}

// termSQL returns a parenthesised condition for the positive form of t.
func termSQL(t Term) (string, []any) {
	switch t.Kind {
	case KindText:
		if utf8.RuneCountInString(t.Text) >= trigramMin {
			return `(s.rid IN ` + ftsSubquery + `)`, []any{ftsPhrase(t.Text)}
		}
		// relpath ends in the file name, so it covers name as well. LIKE
		// only folds ASCII case; the term and the *_key columns are both
		// lower-cased by Go, which covers the rest ("É" finds "é").
		p := likeContains(t.Text)
		return `(s.relpath_key LIKE ? ESCAPE '\' OR s.tags_key LIKE ? ESCAPE '\'` +
			` OR s.pack_id IN (SELECT id FROM pack WHERE name_key LIKE ? ESCAPE '\'))`, []any{p, p, p}
	case KindTag:
		return `(s.rid IN (SELECT rid FROM sample_tag WHERE key = ?))`, []any{t.Text}
	case KindTagLeaf:
		return `(s.rid IN (SELECT rid FROM sample_tag WHERE key IN (SELECT key FROM tag WHERE leaf = ?)))`, []any{t.Text}
	case KindPack:
		// An id names exactly one pack, which is what the sidebar counts;
		// a name is only tried when the text is not an id, and then it
		// means every pack called that.
		return `(s.pack_id IN (SELECT id FROM pack WHERE id = ? OR` +
				` (name_key = ? AND NOT EXISTS (SELECT 1 FROM pack WHERE id = ?))))`,
			[]any{t.Text, strings.ToLower(t.Text), t.Text}
	case KindCollection:
		return `(s.id IN (SELECT sample_id FROM collection_item WHERE coll_id IN` +
				` (SELECT id FROM collection WHERE id = ? OR` +
				` (name_key = ? AND NOT EXISTS (SELECT 1 FROM collection WHERE id = ?)))))`,
			[]any{t.Text, strings.ToLower(t.Text), t.Text}
	case KindBPM:
		cond, args := rangesSQL("s.bpm", t.Ranges)
		// 0 means unknown, which no tempo filter should match.
		return `(s.bpm > 0 AND ` + cond + `)`, args
	case KindLen:
		return rangesSQL("s.dur", t.Ranges)
	case KindKey:
		if len(t.Keys) == 0 {
			return "", nil
		}
		// A root note without a mode ("A" for a one-shot) fits any key on
		// that tonic. It is stored under the major name, so only the minor
		// keys asked for need translating.
		var roots []string
		for _, key := range t.Keys {
			if k, ok := parseKey(key); ok && k.minor {
				roots = append(roots, pitchNames[k.pc])
			}
		}
		cond := `s.key IN (` + placeholders(len(t.Keys)) + `)`
		if len(roots) == 0 {
			return `(` + cond + `)`, anys(t.Keys)
		}
		return `(` + cond + ` OR (s.key_root = 1 AND s.key IN (` + placeholders(len(roots)) + `)))`,
			append(anys(t.Keys), anys(roots)...)
	case KindType:
		return `(s.type = ?)`, []any{t.Text}
	case KindFmt:
		exts := []string{t.Text}
		if t.Text == "aif" || t.Text == "aiff" || t.Text == "aifc" {
			exts = []string{"aif", "aiff", "aifc"}
		}
		return `(lower(s.ext) IN (` + placeholders(len(exts)) + `))`, anys(exts)
	case KindFlag:
		switch t.Text {
		case FlagFav:
			return `(s.fav = 1)`, nil
		case FlagUntagged:
			return `(s.tags = '')`, nil
		case FlagMissing:
			return `(s.missing = 1)`, nil
		case FlagUnanalyzed:
			return `(s.analyzed = 0)`, nil
		case FlagRecent:
			return recentCond, nil
		}
	}
	return "", nil
}

// recentCond selects the newest import batch that still has samples.
const recentCond = `(s.batch > 0 AND s.batch = (SELECT max(batch) FROM sample))`

// rangesSQL matches col against any of ranges.
func rangesSQL(col string, ranges []Range) (string, []any) {
	var (
		alts []string
		args []any
	)
	for _, r := range ranges {
		var bounds []string
		if !math.IsInf(r.Lo, -1) {
			op := " >= ?"
			if r.LoExcl {
				op = " > ?"
			}
			bounds = append(bounds, col+op)
			args = append(args, r.Lo)
		}
		if !math.IsInf(r.Hi, 1) {
			op := " <= ?"
			if r.HiExcl {
				op = " < ?"
			}
			bounds = append(bounds, col+op)
			args = append(args, r.Hi)
		}
		if len(bounds) == 0 {
			bounds = []string{"1"}
		}
		alts = append(alts, "("+strings.Join(bounds, " AND ")+")")
	}
	if len(alts) == 0 {
		return "(0)", nil
	}
	return "(" + strings.Join(alts, " OR ") + ")", args
}

// ftsPhrase quotes a term as an FTS5 string, so nothing in it is read as
// query syntax.
func ftsPhrase(term string) string {
	return `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
}

func likeContains(term string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term) + "%"
}

// ordering is a resolved sort: the field after defaults have been applied.
type ordering struct {
	by   string
	desc bool
}

var newestFirst = ordering{by: model.SortAdded, desc: true}

// resolveSort applies the defaults: no explicit sort means relevance when the
// query has free text and newest first otherwise. Relevance needs something
// to rank by, so it degrades to newest first when the trigram index is not
// involved; the caller does the same when there are more than relevanceMax
// matches.
func resolveSort(q Query, f filter, sort string, desc bool) ordering {
	switch sort {
	case model.SortName, model.SortBPM, model.SortKey, model.SortDur, model.SortPack, model.SortAdded:
		return ordering{by: sort, desc: desc}
	case model.SortRelevance:
	default:
		if !q.HasText() {
			return newestFirst
		}
	}
	if f.match == "" {
		return newestFirst
	}
	return ordering{by: model.SortRelevance}
}

// orderBy returns the ORDER BY list. Unknown BPM and key sort last in both
// directions, and every order ends in rid so paging is stable.
func (o ordering) orderBy() string {
	dir := " ASC"
	if o.desc {
		dir = " DESC"
	}
	switch o.by {
	case model.SortName:
		return "s.name COLLATE NOCASE" + dir + ", s.rid" + dir
	case model.SortBPM:
		return "s.bpm = 0, s.bpm" + dir + ", s.rid"
	case model.SortKey:
		return "s.key_ord < 0, s.key_ord" + dir + ", s.rid"
	case model.SortDur:
		return "s.dur" + dir + ", s.rid"
	case model.SortPack:
		return "s.pack_key" + dir + ", s.pack_id" + dir + ", s.relpath COLLATE NOCASE" + dir + ", s.rid" + dir
	case model.SortRelevance:
		return "sample_fts.rank, s.added DESC, s.rid DESC"
	default:
		return "s.added" + dir + ", s.rid" + dir
	}
}

// selectSQL builds "SELECT col FROM sample s ... WHERE ... ORDER BY ...".
func (f filter) selectSQL(col string, o ordering) (string, []any) {
	var b strings.Builder
	conds, args := f.conds, f.args
	b.WriteString("SELECT " + col + " FROM sample s")
	switch {
	case o.by == model.SortRelevance:
		b.WriteString(" JOIN sample_fts ON sample_fts.rowid = s.rid")
		conds = append([]string{"sample_fts MATCH ?"}, conds...)
		args = append([]any{f.match}, args...)
	case f.match != "":
		conds = append([]string{"s.rid IN " + ftsSubquery}, conds...)
		args = append([]any{f.match}, args...)
	}
	if len(conds) > 0 {
		b.WriteString(" WHERE " + strings.Join(conds, " AND "))
	}
	if ob := o.orderBy(); o.by != "" {
		b.WriteString(" ORDER BY " + ob)
	}
	return b.String(), args
}

// countSQL builds the statement that counts the matches.
func (f filter) countSQL() (string, []any) {
	if f.match != "" && len(f.conds) == 0 {
		// Every FTS row has a sample row, so there is nothing to join.
		return `SELECT count(*) FROM sample_fts WHERE sample_fts MATCH ?`, []any{f.match}
	}
	return f.selectSQL("count(*)", ordering{})
}

// maxInArgs bounds the length of generated IN lists.
const maxInArgs = 500

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

func anys[T any](in []T) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}
