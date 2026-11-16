package index

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Collection is a hand-picked, ordered set of sample ids. Items may name
// samples the index does not (yet) hold; they simply do not match anything.
type Collection struct {
	ID    string
	Name  string
	Items []string
}

// ReplaceCollections makes the indexed collections exactly cols.
func (ix *Index) ReplaceCollections(cols []Collection) error {
	return ix.write(func(w *writer) error {
		if err := w.exec(`DELETE FROM collection_item`); err != nil {
			return err
		}
		if err := w.exec(`DELETE FROM collection`); err != nil {
			return err
		}
		for _, c := range cols {
			if err := w.putCollection(c); err != nil {
				return err
			}
		}
		return nil
	})
}

// UpsertCollection inserts a collection or replaces its name and membership.
func (ix *Index) UpsertCollection(c Collection) error {
	return ix.write(func(w *writer) error {
		if err := w.exec(`DELETE FROM collection_item WHERE coll_id = ?`, c.ID); err != nil {
			return err
		}
		return w.putCollection(c)
	})
}

// DeleteCollection drops a collection and its membership.
func (ix *Index) DeleteCollection(id string) error {
	return ix.write(func(w *writer) error {
		if err := w.exec(`DELETE FROM collection_item WHERE coll_id = ?`, id); err != nil {
			return err
		}
		return w.exec(`DELETE FROM collection WHERE id = ?`, id)
	})
}

// SampleCollections returns, for each of the given sample ids that is in any
// collection, the ids of those collections.
func (ix *Index) SampleCollections(sampleIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	for chunk := range slices.Chunk(sampleIDs, maxInArgs) {
		rows, err := ix.db.Query(
			`SELECT sample_id, coll_id FROM collection_item WHERE sample_id IN (`+placeholders(len(chunk))+`) ORDER BY coll_id`,
			anys(chunk)...)
		if err != nil {
			return nil, fmt.Errorf("index: sample collections: %w", err)
		}
		for rows.Next() {
			var sample, coll string
			if err := rows.Scan(&sample, &coll); err != nil {
				rows.Close()
				return nil, fmt.Errorf("index: sample collections: %w", err)
			}
			out[sample] = append(out[sample], coll)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("index: sample collections: %w", err)
		}
	}
	return out, nil
}

// putCollection writes a collection whose items have already been cleared.
func (w *writer) putCollection(c Collection) error {
	if c.ID == "" {
		return errors.New("collection without id")
	}
	err := w.exec(`
		INSERT INTO collection(id, name, name_key) VALUES(?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, name_key = excluded.name_key`,
		c.ID, c.Name, strings.ToLower(c.Name))
	if err != nil {
		return fmt.Errorf("collection %s: %w", c.ID, err)
	}
	for pos, id := range c.Items {
		// A duplicate keeps its first position.
		err := w.exec(`INSERT OR IGNORE INTO collection_item(coll_id, sample_id, pos) VALUES(?, ?, ?)`, c.ID, id, pos)
		if err != nil {
			return fmt.Errorf("collection %s: %w", c.ID, err)
		}
	}
	return nil
}
