package index

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"ssss/internal/model"
)

const (
	defaultPageSize = 200
	maxPageSize     = 5000
)

// Search returns one page of the samples matching req.Query, plus the total
// number of matches. A query the parser only partly understood still
// searches on the terms it did understand; the problem is reported in
// SearchResult.Error. The returned error is for database failures.
func (ix *Index) Search(req model.SearchRequest) (model.SearchResult, error) {
	res := model.SearchResult{Rows: []model.Sample{}}
	q, perr := Parse(req.Query)
	if perr != nil {
		res.Error = perr.Error()
	}
	f := compile(q)

	var err error
	if res.Total, err = ix.count(f); err != nil {
		return res, fmt.Errorf("index: search count: %w", err)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	limit = min(limit, maxPageSize)
	offset := max(req.Offset, 0)
	if offset >= res.Total {
		return res, nil
	}

	// Page through bare rids first and fetch the wide rows for just that
	// page, so the sort never has to carry row payloads.
	order := resolveSort(q, f, req.Sort, req.Desc)
	if order.by == model.SortRelevance && res.Total > relevanceMax {
		order = newestFirst
	}
	pageSQL, args := f.selectSQL("s.rid", order)
	rids, err := queryColumn[int64](ix.db, pageSQL+" LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if err != nil {
		return res, fmt.Errorf("index: search: %w", err)
	}
	rows, err := samplesBy(ix, "s.rid", rids, func(s Sample, rid int64) int64 { return rid })
	if err != nil {
		return res, fmt.Errorf("index: search rows: %w", err)
	}
	res.Rows = make([]model.Sample, len(rows))
	for i, s := range rows {
		res.Rows[i] = s.Sample
	}
	return res, nil
}

// SearchIDs returns the ids of every match in sort order, for select-all and
// range selection across pages that are not loaded. Terms the parser did not
// understand are ignored, as in Search.
func (ix *Index) SearchIDs(query, sort string, desc bool) ([]string, error) {
	q, _ := Parse(query)
	f := compile(q)
	order := resolveSort(q, f, sort, desc)
	if order.by == model.SortRelevance {
		// Must agree with Search on when a result is too big to rank.
		total, err := ix.count(f)
		if err != nil {
			return nil, fmt.Errorf("index: search ids: %w", err)
		}
		if total > relevanceMax {
			order = newestFirst
		}
	}
	stmt, args := f.selectSQL("s.id", order)
	ids, err := queryColumn[string](ix.db, stmt, args...)
	if err != nil {
		return nil, fmt.Errorf("index: search ids: %w", err)
	}
	return ids, nil
}

func (ix *Index) count(f filter) (int, error) {
	var n int
	stmt, args := f.countSQL()
	err := ix.db.QueryRow(stmt, args...).Scan(&n)
	return n, err
}

// Samples returns the indexed samples with the given ids, in the order
// given. Unknown ids are skipped.
func (ix *Index) Samples(ids []string) ([]Sample, error) {
	rows, err := samplesBy(ix, "s.id", ids, func(s Sample, _ int64) string { return s.ID })
	if err != nil {
		return nil, fmt.Errorf("index: samples: %w", err)
	}
	return rows, nil
}

// Count returns the number of indexed samples.
func (ix *Index) Count() (int, error) {
	var n int
	if err := ix.db.QueryRow(`SELECT count(*) FROM sample`).Scan(&n); err != nil {
		return 0, fmt.Errorf("index: count: %w", err)
	}
	return n, nil
}

const sampleColumns = `s.rid, s.id, s.pack_id, coalesce(p.name, ''), s.name, s.ext, s.relpath, s.size, s.dur,
	s.sr, s.ch, s.type, s.bpm, s.bpm_src, s.key, s.key_src, s.key_root, s.tags, s.fav, s.added, s.missing, s.analyzed,
	coalesce(m.mini, ''), s.note`

// samplesBy loads full rows whose column col is one of keys and returns them
// in the order of keys. keyOf extracts that column's value from a loaded row.
func samplesBy[K comparable](ix *Index, col string, keys []K, keyOf func(Sample, int64) K) ([]Sample, error) {
	found := make(map[K]Sample, len(keys))
	for chunk := range slices.Chunk(keys, maxInArgs) {
		rows, err := ix.db.Query(`SELECT `+sampleColumns+`
			FROM sample s
			LEFT JOIN pack p ON p.id = s.pack_id
			LEFT JOIN sample_mini m ON m.rid = s.rid
			WHERE `+col+` IN (`+placeholders(len(chunk))+`)`, anys(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			s, rid, err := scanSample(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			found[keyOf(s, rid)] = s
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]Sample, 0, len(found))
	for _, k := range keys {
		if s, ok := found[k]; ok {
			out = append(out, s)
			delete(found, k) // a repeated key yields one row
		}
	}
	return out, nil
}

func scanSample(rows *sql.Rows) (Sample, int64, error) {
	var (
		s    Sample
		rid  int64
		tags string
	)
	err := rows.Scan(&rid, &s.ID, &s.PackID, &s.PackName, &s.Name, &s.Ext, &s.RelPath, &s.Size, &s.Dur,
		&s.SR, &s.Ch, &s.Type, &s.BPM, &s.BPMSrc, &s.Key, &s.KeySrc, &s.KeyRoot, &tags, &s.Fav, &s.Added, &s.Missing,
		&s.Analyzed, &s.Mini, &s.Note)
	if err != nil {
		return s, 0, err
	}
	s.Tags = []string{}
	if tags != "" {
		s.Tags = strings.Split(tags, tagSep)
	}
	return s, rid, nil
}

func queryColumn[T any](db *sql.DB, query string, args ...any) ([]T, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var v T
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
