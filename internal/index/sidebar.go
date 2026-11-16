package index

import (
	"fmt"
	"slices"
	"strings"

	"ssss/internal/model"
)

// Sidebar computes the sidebar aggregates. Saved views and tag colours live
// in the library's JSON, not in the index, so the caller passes them in;
// tagColors is keyed by tag path and matched case-insensitively.
//
// Tag counts include descendants and count a sample once per tag, however
// many of the tag's descendants it carries. Ancestors that are never applied
// directly are listed too, so the result is a complete tree.
func (ix *Index) Sidebar(views []model.SavedView, tagColors map[string]string) (model.Sidebar, error) {
	sb := model.Sidebar{
		Tags:        []model.TagCount{},
		Packs:       []model.PackInfo{},
		Collections: []model.CollectionInfo{},
		Views:       views,
	}
	if sb.Views == nil {
		sb.Views = []model.SavedView{}
	}
	fail := func(err error) (model.Sidebar, error) {
		return sb, fmt.Errorf("index: sidebar: %w", err)
	}

	err := ix.db.QueryRow(`
		SELECT count(*), coalesce(sum(fav), 0), coalesce(sum(tags = ''), 0),
		  coalesce(sum(batch > 0 AND batch = (SELECT max(batch) FROM sample)), 0)
		FROM sample`).Scan(&sb.All, &sb.Favorites, &sb.Untagged, &sb.Recent)
	if err != nil {
		return fail(err)
	}

	colors := make(map[string]string, len(tagColors))
	for tag, color := range tagColors {
		colors[strings.ToLower(normTag(tag))] = color
	}
	rows, err := ix.db.Query(`
		SELECT c.key, t.name, c.n
		FROM (SELECT key, count(*) AS n FROM sample_tag GROUP BY key) c
		JOIN tag t ON t.key = c.key`)
	if err != nil {
		return fail(err)
	}
	type tagRow struct {
		model.TagCount
		segments []string
	}
	var tags []tagRow
	for rows.Next() {
		var (
			key string
			t   tagRow
		)
		if err := rows.Scan(&key, &t.Tag, &t.Count); err != nil {
			rows.Close()
			return fail(err)
		}
		t.Color = colors[key]
		t.segments = strings.Split(key, "/")
		tags = append(tags, t)
	}
	if err := rows.Close(); err != nil {
		return fail(err)
	}
	if err := rows.Err(); err != nil {
		return fail(err)
	}
	// Segment-wise order puts every tag directly before its subtree, which
	// a plain string sort does not ("a-b" sorts between "a" and "a/b").
	slices.SortFunc(tags, func(a, b tagRow) int { return slices.Compare(a.segments, b.segments) })
	for _, t := range tags {
		sb.Tags = append(sb.Tags, t.TagCount)
	}

	rows, err = ix.db.Query(`
		SELECT p.id, p.name, p.dir, p.source, coalesce(c.n, 0)
		FROM pack p
		LEFT JOIN (SELECT pack_id, count(*) AS n FROM sample GROUP BY pack_id) c ON c.pack_id = p.id
		ORDER BY p.name_key, p.id`)
	if err != nil {
		return fail(err)
	}
	for rows.Next() {
		var p model.PackInfo
		if err := rows.Scan(&p.ID, &p.Name, &p.Dir, &p.Source, &p.Count); err != nil {
			rows.Close()
			return fail(err)
		}
		sb.Packs = append(sb.Packs, p)
	}
	if err := rows.Close(); err != nil {
		return fail(err)
	}
	if err := rows.Err(); err != nil {
		return fail(err)
	}

	// Only members the index actually holds are counted, so the number
	// agrees with what in:<collection> lists.
	rows, err = ix.db.Query(`
		SELECT c.id, c.name,
		  (SELECT count(*) FROM collection_item i JOIN sample s ON s.id = i.sample_id WHERE i.coll_id = c.id)
		FROM collection c
		ORDER BY c.name_key, c.id`)
	if err != nil {
		return fail(err)
	}
	defer rows.Close()
	for rows.Next() {
		var c model.CollectionInfo
		if err := rows.Scan(&c.ID, &c.Name, &c.Count); err != nil {
			return fail(err)
		}
		sb.Collections = append(sb.Collections, c)
	}
	if err := rows.Err(); err != nil {
		return fail(err)
	}
	return sb, nil
}
