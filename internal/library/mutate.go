package library

import (
	"fmt"
	"math"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"golang.org/x/text/unicode/norm"

	"ssss/internal/model"
)

// AddTags adds every tag to every sample. Unknown sample IDs are skipped.
func (l *Library) AddTags(ids, tags []string) (Change, error) {
	tags = normalizeTags(tags)
	if len(tags) == 0 {
		return Change{}, fmt.Errorf("%w: no tag given", ErrInvalid)
	}
	return l.mutate(func(t *tx) error {
		for _, id := range ids {
			t.editTags(id, nil, appendTags(tags))
		}
		return nil
	})
}

// RemoveTags removes exactly the given tags (not their descendants).
func (l *Library) RemoveTags(ids, tags []string) (Change, error) {
	tags = normalizeTags(tags)
	return l.mutate(func(t *tx) error {
		for _, id := range ids {
			t.editTags(id, tags, nil)
		}
		return nil
	})
}

// underTag reports whether tag is root or one of its descendants, returning
// the part after root ("" or "/child/...").
func underTag(tag, root string) (rest string, ok bool) {
	rest, ok = strings.CutPrefix(tag, root)
	if !ok || rest != "" && rest[0] != '/' {
		return "", false
	}
	return rest, true
}

// rewriteTags applies fn to a tag and its descendants across the library.
// fn returns the replacement, or "" to drop the tag.
func (t *tx) rewriteTags(root string, fn func(rest string) string) {
	st := t.l.st
	for id, p := range st.byID {
		rec := p.Samples[id]
		// The replacement takes the old tag's place. If the sample already
		// has it further down (a rename onto an existing tag), that one
		// goes: it is dropped and added like the tags being rewritten, so
		// an undo puts it back where it was.
		var next, drop []string
		moved := map[string]struct{}{}
		for _, tag := range rec.Tags {
			repl := tag
			if rest, ok := underTag(tag, root); ok {
				drop = append(drop, tag)
				if repl = fn(rest); repl == "" {
					continue
				}
				moved[repl] = struct{}{}
			}
			if !slices.Contains(next, repl) {
				next = append(next, repl)
			}
		}
		var add []placedTag
		for i, tag := range next {
			if _, ok := moved[tag]; ok {
				drop = append(drop, tag)
				add = append(add, placedTag{tag, i})
			}
		}
		t.editTags(id, drop, add)
	}
	for _, tag := range sortedKeys(st.tags.Tags) {
		rest, ok := underTag(tag, root)
		if !ok {
			continue
		}
		decor := st.tags.Tags[tag]
		t.setTagDecor(tag, nil)
		if repl := fn(rest); repl != "" {
			if _, exists := st.tags.Tags[repl]; !exists {
				t.setTagDecor(repl, &decor)
			}
		}
	}
}

// RenameTag renames a tag and all its descendants everywhere, merging with
// the target if it already exists.
func (l *Library) RenameTag(oldTag, newTag string) (Change, error) {
	oldTag, newTag = NormalizeTag(oldTag), NormalizeTag(newTag)
	if oldTag == "" || newTag == "" {
		return Change{}, fmt.Errorf("%w: empty tag", ErrInvalid)
	}
	if oldTag == newTag {
		return Change{}, nil
	}
	return l.mutate(func(t *tx) error {
		t.rewriteTags(oldTag, func(rest string) string { return newTag + rest })
		return nil
	})
}

// DeleteTag removes a tag and all its descendants from every sample.
func (l *Library) DeleteTag(tag string) (Change, error) {
	tag = NormalizeTag(tag)
	if tag == "" {
		return Change{}, fmt.Errorf("%w: empty tag", ErrInvalid)
	}
	return l.mutate(func(t *tx) error {
		t.rewriteTags(tag, func(string) string { return "" })
		return nil
	})
}

// SetTagColor sets a tag's colour; "" clears it.
func (l *Library) SetTagColor(tag, color string) (Change, error) {
	tag, color = NormalizeTag(tag), strings.TrimSpace(color)
	if tag == "" || len(color) > 32 {
		return Change{}, fmt.Errorf("%w: tag colour", ErrInvalid)
	}
	return l.mutate(func(t *tx) error {
		if err := l.writableFileLocked(tagsFileName); err != nil {
			return err
		}
		decor, had := l.st.tags.Tags[tag]
		if decor.Color == color {
			return nil
		}
		decor.Color = color
		if color == "" && len(decor.extra) == 0 {
			if had {
				t.setTagDecor(tag, nil)
			}
			return nil
		}
		t.setTagDecor(tag, &decor)
		return nil
	})
}

// SetFavorite marks or unmarks samples as favourites.
func (l *Library) SetFavorite(ids []string, fav bool) (Change, error) {
	return l.mutate(func(t *tx) error {
		for _, id := range ids {
			t.setFields(id, fFav, SampleRecord{Fav: fav})
		}
		return nil
	})
}

// SetMeta applies a user edit. Edited values get source "user", which
// analysis and rescans never overwrite; that includes clearing a value
// (BPM 0, key ""), so a wrong detection stays cleared. Keys must already be
// in canonical notation.
func (l *Library) SetMeta(ids []string, patch model.MetaPatch) (Change, error) {
	var vals SampleRecord
	var mask field
	if patch.BPM != nil {
		bpm := *patch.BPM
		if math.IsNaN(bpm) || bpm < 0 || bpm > 1000 {
			return Change{}, fmt.Errorf("%w: bpm %v", ErrInvalid, bpm)
		}
		vals.BPM, vals.BPMSrc = bpm, model.SrcUser
		mask |= fBPM
	}
	if patch.Key != nil {
		vals.Key, vals.KeySrc = strings.TrimSpace(*patch.Key), model.SrcUser
		mask |= fKey
	}
	if patch.Type != nil {
		switch *patch.Type {
		case "", model.TypeLoop, model.TypeOneShot:
		default:
			return Change{}, fmt.Errorf("%w: type %q", ErrInvalid, *patch.Type)
		}
		vals.Type, vals.TypeSrc = *patch.Type, model.SrcUser
		mask |= fType
	}
	if patch.Note != nil {
		vals.Note = *patch.Note
		mask |= fNote
	}
	return l.mutate(func(t *tx) error {
		for _, id := range ids {
			t.setFields(id, mask, vals)
		}
		return nil
	})
}

// RenameSample renames the file on disk, keeping its folder and extension.
// newName is the name without extension.
func (l *Library) RenameSample(id, newName string) (Change, error) {
	if strings.TrimSpace(newName) == "" {
		return Change{}, fmt.Errorf("%w: empty name", ErrInvalid)
	}
	return l.mutate(func(t *tx) error {
		rec := l.recLocked(id)
		if rec == nil {
			return fmt.Errorf("sample %s: %w", id, ErrNotFound)
		}
		ext := path.Ext(rec.Path)
		stem := strings.TrimSpace(newName)
		// Typing the extension along with the name must not double it.
		if len(stem) > len(ext) && strings.EqualFold(stem[len(stem)-len(ext):], ext) {
			stem = stem[:len(stem)-len(ext)]
		}
		name := SanitizeComponent(stem + ext)
		return t.renamePath(id, path.Join(path.Dir(rec.Path), name))
	})
}

// RemoveSamples moves the files to _library/trash and tombstones the
// records. The Change's Undo restores both.
func (l *Library) RemoveSamples(ids []string) (Change, error) {
	return l.mutate(func(t *tx) error {
		for _, id := range ids {
			if err := t.removeSample(id); err != nil {
				return fmt.Errorf("remove %s: %w", id, err)
			}
		}
		return nil
	})
}

func validSampleID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range []byte(id) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func prepareRecord(rec SampleRecord) SampleRecord {
	rec.Tags = normalizeTags(rec.Tags)
	if rec.Added.IsZero() {
		rec.Added = now()
	}
	return rec
}

// AddSample records a newly ingested file. rec.Path is the wanted path
// inside the pack; it is sanitised and, if another file already has it,
// given a " (2)" style suffix. place is then called with the final absolute
// path and must put the file there (a rename from a temp file next to it);
// it runs under the library lock. Returns ErrDuplicate when the content is
// already in the library.
func (l *Library) AddSample(packID, id string, rec SampleRecord, place func(absPath string) error) (Change, error) {
	if !validSampleID(id) || place == nil {
		return Change{}, fmt.Errorf("%w: sample id %q", ErrInvalid, id)
	}
	rec = prepareRecord(rec)
	rec.Rev = now()
	return l.mutate(func(t *tx) error {
		p := l.st.packs[packID]
		if p == nil {
			return fmt.Errorf("pack %s: %w", packID, ErrNotFound)
		}
		if l.st.byID[id] != nil {
			return ErrDuplicate
		}
		rec.Path = l.freePath(p, sanitizeRelPath(rec.Path, relLimit(p)), id)
		return t.insertSample(packID, id, rec, place)
	})
}

// AdoptSample records a file that is already inside the pack folder at
// rec.Path (found by a rescan); the caller has made sure the file answers
// to the NFC form of that path, which is what is stored.
//
// The record gets no revision: it says nothing a person decided. If the
// file turns out to be described by a pack file that had not been synced
// yet, that description wins the merge whatever its age, and the first real
// edit here gives the record a revision of its own.
func (l *Library) AdoptSample(packID, id string, rec SampleRecord) (Change, error) {
	if !validSampleID(id) || rec.Path == "" {
		return Change{}, fmt.Errorf("%w: sample id %q", ErrInvalid, id)
	}
	rec = prepareRecord(rec)
	rec.Rev = time.Time{}
	rec.Path = norm.NFC.String(rec.Path)
	return l.mutate(func(t *tx) error {
		return t.insertSample(packID, id, rec, nil)
	})
}

// HealPath points a record at rel after the file was moved or renamed
// outside the app. Not undoable.
func (l *Library) HealPath(id, rel string) (Change, error) {
	ch, err := l.mutate(func(t *tx) error {
		p := l.st.byID[id]
		if p == nil {
			return fmt.Errorf("sample %s: %w", id, ErrNotFound)
		}
		if owner, taken := l.st.pathKeys[sampleKey(p, rel)]; taken && owner != id {
			return fmt.Errorf("%w: %s", ErrPathTaken, rel)
		}
		t.setPath(p, id, rel)
		delete(l.missing, id)
		return nil
	})
	ch.Undo = nil
	return ch, err
}

// SetMissing records (in memory only) whether the samples' files were found
// by the last scan.
func (l *Library) SetMissing(ids []string, missing bool) Change {
	l.mu.Lock()
	defer l.mu.Unlock()
	var ch Change
	for _, id := range ids {
		if l.st.byID[id] == nil {
			continue
		}
		if _, was := l.missing[id]; was == missing {
			continue
		}
		if missing {
			l.missing[id] = struct{}{}
		} else {
			delete(l.missing, id)
		}
		ch.IDs = append(ch.IDs, id)
	}
	return ch
}

// CreatePack adds an empty pack. dir is its folder under Samples ("" derives
// it from the name); it is sanitised and made unique among the packs.
func (l *Library) CreatePack(name, dir, source string) (model.PackInfo, Change, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.PackInfo{}, Change{}, fmt.Errorf("%w: empty pack name", ErrInvalid)
	}
	if dir == "" {
		dir = name
	}
	var info model.PackInfo
	ch, err := l.mutate(func(t *tx) error {
		base := sanitizeRelPath(dir, 80)
		taken := map[string]struct{}{}
		for _, p := range l.st.packs {
			taken[PathKey(p.Dir)] = struct{}{}
		}
		final := base
		for n := 2; ; n++ {
			if _, dup := taken[PathKey(final)]; !dup {
				break
			}
			final = fmt.Sprintf("%s (%d)", base, n)
		}
		p := &pack{
			packHeader: packHeader{V: FormatVersion, ID: "pk_" + ulid.Make().String(), Name: name, Dir: final, Source: source, Added: now()},
			Samples:    map[string]*SampleRecord{},
			Removed:    map[string]time.Time{},
		}
		p.file = p.ID + ".json"
		t.putPack(p)
		info = packInfo(p, 0)
		return nil
	})
	return info, ch, err
}

// RenamePack changes the display name only; the folder stays.
func (l *Library) RenamePack(id, name string) (Change, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Change{}, fmt.Errorf("%w: empty pack name", ErrInvalid)
	}
	return l.mutate(func(t *tx) error {
		if l.st.packs[id] == nil {
			return fmt.Errorf("pack %s: %w", id, ErrNotFound)
		}
		t.setPackName(id, name)
		return nil
	})
}

// CreateCollection adds an empty collection.
func (l *Library) CreateCollection(name string) (model.CollectionInfo, Change, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.CollectionInfo{}, Change{}, fmt.Errorf("%w: empty collection name", ErrInvalid)
	}
	c := &collection{V: FormatVersion, ID: "col_" + ulid.Make().String(), Name: name, Rev: now()}
	ch, err := l.mutate(func(t *tx) error {
		t.putCollection(c.ID, c)
		return nil
	})
	return model.CollectionInfo{ID: c.ID, Name: name}, ch, err
}

// RenameCollection changes a collection's name.
func (l *Library) RenameCollection(id, name string) (Change, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Change{}, fmt.Errorf("%w: empty collection name", ErrInvalid)
	}
	return l.mutate(func(t *tx) error {
		if l.st.collections[id] == nil {
			return fmt.Errorf("collection %s: %w", id, ErrNotFound)
		}
		t.setCollectionName(id, name, nil)
		return nil
	})
}

// DeleteCollection deletes a collection; its samples stay in the library.
func (l *Library) DeleteCollection(id string) (Change, error) {
	return l.mutate(func(t *tx) error {
		if l.st.collections[id] == nil {
			return fmt.Errorf("collection %s: %w", id, ErrNotFound)
		}
		t.putCollection(id, nil)
		return nil
	})
}

// AddToCollection appends the samples that exist and are not yet members.
func (l *Library) AddToCollection(id string, sampleIDs []string) (Change, error) {
	return l.mutate(func(t *tx) error {
		if l.st.collections[id] == nil {
			return fmt.Errorf("collection %s: %w", id, ErrNotFound)
		}
		known := slices.DeleteFunc(slices.Clone(sampleIDs), func(sid string) bool { return l.st.byID[sid] == nil })
		t.editCollection(id, nil, appendItems(known))
		return nil
	})
}

// RemoveFromCollection removes the samples from the collection.
func (l *Library) RemoveFromCollection(id string, sampleIDs []string) (Change, error) {
	return l.mutate(func(t *tx) error {
		if l.st.collections[id] == nil {
			return fmt.Errorf("collection %s: %w", id, ErrNotFound)
		}
		t.editCollection(id, sampleIDs, nil)
		return nil
	})
}

// SaveView stores a saved search. Saving under an existing name (compared
// case-insensitively) replaces that view's query.
func (l *Library) SaveView(name, query string) (model.SavedView, Change, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.SavedView{}, Change{}, fmt.Errorf("%w: empty view name", ErrInvalid)
	}
	var saved model.SavedView
	ch, err := l.mutate(func(t *tx) error {
		if err := l.writableFileLocked(viewsFileName); err != nil {
			return err
		}
		views := l.st.views.Views
		v := view{ID: "vw_" + ulid.Make().String()}
		if i := slices.IndexFunc(views, func(v view) bool { return strings.EqualFold(v.Name, name) }); i >= 0 {
			v = views[i]
		}
		v.Name, v.Query = name, query
		saved = model.SavedView{ID: v.ID, Name: name, Query: query}
		t.setView(v.ID, &v, len(views))
		return nil
	})
	return saved, ch, err
}

// DeleteView removes a saved search.
func (l *Library) DeleteView(id string) (Change, error) {
	return l.mutate(func(t *tx) error {
		if err := l.writableFileLocked(viewsFileName); err != nil {
			return err
		}
		if !slices.ContainsFunc(l.st.views.Views, func(v view) bool { return v.ID == id }) {
			return fmt.Errorf("view %s: %w", id, ErrNotFound)
		}
		t.setView(id, nil, 0)
		return nil
	})
}

// recLocked returns the live record for id, or nil. Callers hold mu.
func (l *Library) recLocked(id string) *SampleRecord {
	if p := l.st.byID[id]; p != nil {
		return p.Samples[id]
	}
	return nil
}
