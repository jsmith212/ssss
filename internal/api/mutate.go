package api

import (
	"fmt"
	"slices"
	"strings"

	"ssss/internal/analysis"
	"ssss/internal/library"
	"ssss/internal/model"
)

// Every mutation pushes an undo entry and emits "samples:changed".

// mutate runs one library mutation and does what follows from it: the index
// is updated, the frontend is told, and the change goes on the undo stack
// under desc.
func (a *API) mutate(desc string, fn func(lib *library.Library) (library.Change, error)) error {
	s, err := a.writable()
	if err != nil {
		return err
	}
	ch, err := fn(s.lib)
	if err != nil {
		return err
	}
	a.commit(s, ch)
	s.history.did(desc, ch.Undo)
	return nil
}

// commit propagates a change the library has already made.
func (a *API) commit(s *session, ch library.Change) {
	if ch.Empty() {
		return
	}
	if err := s.eng.Apply(ch); err != nil {
		a.toastError(fmt.Errorf("could not update the local index: %w", err))
	}
	for _, id := range ch.Removed {
		a.audio.Forget(id)
	}
	event := model.SamplesChanged{Structural: ch.Structural}
	if !ch.All {
		event.IDs = slices.Concat(ch.IDs, ch.Removed)
		if event.IDs == nil {
			event.IDs = []string{}
		}
	}
	a.emit(model.EventSamplesChanged, event)
}

func samples(n int) string {
	if n == 1 {
		return "1 sample"
	}
	return fmt.Sprintf("%d samples", n)
}

// AddTags adds tags to the samples.
func (a *API) AddTags(ids, tags []string) error {
	return a.mutate("tag "+samples(len(ids)), func(lib *library.Library) (library.Change, error) {
		return lib.AddTags(ids, tags)
	})
}

// RemoveTags removes tags from the samples.
func (a *API) RemoveTags(ids, tags []string) error {
	return a.mutate("untag "+samples(len(ids)), func(lib *library.Library) (library.Change, error) {
		return lib.RemoveTags(ids, tags)
	})
}

// RenameTag renames a tag and its descendants on every sample.
func (a *API) RenameTag(oldTag, newTag string) error {
	return a.mutate("rename tag "+oldTag, func(lib *library.Library) (library.Change, error) {
		return lib.RenameTag(oldTag, newTag)
	})
}

// DeleteTag removes a tag and its descendants from every sample.
func (a *API) DeleteTag(tag string) error {
	return a.mutate("delete tag "+tag, func(lib *library.Library) (library.Change, error) {
		return lib.DeleteTag(tag)
	})
}

// SetTagColor sets the decoration colour of a tag; "" clears it.
func (a *API) SetTagColor(tag, color string) error {
	return a.mutate("colour tag "+tag, func(lib *library.Library) (library.Change, error) {
		return lib.SetTagColor(tag, color)
	})
}

// SetFavorite marks or unmarks the samples as favourites.
func (a *API) SetFavorite(ids []string, fav bool) error {
	desc := "favourite " + samples(len(ids))
	if !fav {
		desc = "unfavourite " + samples(len(ids))
	}
	return a.mutate(desc, func(lib *library.Library) (library.Change, error) {
		return lib.SetFavorite(ids, fav)
	})
}

// SetMeta applies patch to the samples with source "user".
func (a *API) SetMeta(ids []string, patch model.MetaPatch) error {
	if patch.Key != nil {
		if key := strings.TrimSpace(*patch.Key); key != "" {
			norm, ok := analysis.NormalizeKey(key)
			if !ok {
				return fmt.Errorf("%q is not a key; try Am, F# or Bb minor", key)
			}
			patch.Key = &norm
		}
	}
	return a.mutate("edit "+samples(len(ids)), func(lib *library.Library) (library.Change, error) {
		return lib.SetMeta(ids, patch)
	})
}

// RenameSample renames the file of a sample, keeping its extension.
func (a *API) RenameSample(id, newName string) error {
	return a.mutate("rename sample", func(lib *library.Library) (library.Change, error) {
		return lib.RenameSample(id, newName)
	})
}

// RemoveSamples moves the files to the library trash and tombstones them.
func (a *API) RemoveSamples(ids []string) error {
	return a.mutate("remove "+samples(len(ids)), func(lib *library.Library) (library.Change, error) {
		return lib.RemoveSamples(ids)
	})
}

// CreateCollection creates an empty collection.
func (a *API) CreateCollection(name string) (model.CollectionInfo, error) {
	var info model.CollectionInfo
	err := a.mutate("create collection "+strings.TrimSpace(name), func(lib *library.Library) (ch library.Change, err error) {
		info, ch, err = lib.CreateCollection(name)
		return ch, err
	})
	return info, err
}

// RenameCollection renames a collection.
func (a *API) RenameCollection(id, name string) error {
	return a.mutate("rename collection", func(lib *library.Library) (library.Change, error) {
		return lib.RenameCollection(id, name)
	})
}

// DeleteCollection deletes a collection; its samples stay in the library.
func (a *API) DeleteCollection(id string) error {
	return a.mutate("delete collection", func(lib *library.Library) (library.Change, error) {
		return lib.DeleteCollection(id)
	})
}

// AddToCollection adds samples to a collection.
func (a *API) AddToCollection(id string, sampleIDs []string) error {
	return a.mutate("add "+samples(len(sampleIDs))+" to collection", func(lib *library.Library) (library.Change, error) {
		return lib.AddToCollection(id, sampleIDs)
	})
}

// RemoveFromCollection removes samples from a collection.
func (a *API) RemoveFromCollection(id string, sampleIDs []string) error {
	return a.mutate("remove "+samples(len(sampleIDs))+" from collection", func(lib *library.Library) (library.Change, error) {
		return lib.RemoveFromCollection(id, sampleIDs)
	})
}

// RenamePack changes the display name of a pack; its folder is untouched.
func (a *API) RenamePack(id, name string) error {
	return a.mutate("rename pack", func(lib *library.Library) (library.Change, error) {
		return lib.RenamePack(id, name)
	})
}

// SaveView stores query as a saved search.
func (a *API) SaveView(name, query string) (model.SavedView, error) {
	var view model.SavedView
	err := a.mutate("save view "+strings.TrimSpace(name), func(lib *library.Library) (ch library.Change, err error) {
		view, ch, err = lib.SaveView(name, query)
		return ch, err
	})
	return view, err
}

// DeleteView deletes a saved search.
func (a *API) DeleteView(id string) error {
	return a.mutate("delete view", func(lib *library.Library) (library.Change, error) {
		return lib.DeleteView(id)
	})
}

// Reanalyze queues the samples for analysis again; values with source "user"
// are kept.
func (a *API) Reanalyze(ids []string) error {
	s, err := a.writable()
	if err != nil {
		return err
	}
	s.eng.Analyze(ids)
	return nil
}
