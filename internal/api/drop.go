package api

import (
	"ssss/internal/dragout"
	"ssss/internal/ingest"
	"ssss/internal/library"
	"ssss/internal/model"
)

// Drop target kinds, the values of the data-drop-kind attribute.
const (
	dropPack       = "pack"
	dropTag        = "tag"
	dropCollection = "collection"
)

func (t DropTarget) importTarget() model.ImportTarget {
	switch t.Kind {
	case dropPack:
		return model.ImportTarget{PackID: t.ID}
	case dropTag:
		return model.ImportTarget{Tag: t.ID}
	case dropCollection:
		return model.ImportTarget{CollectionID: t.ID}
	}
	return model.ImportTarget{}
}

// ownSamples maps dropped paths to samples; ok is false unless every path
// is a file of this library.
func ownSamples(lib *library.Library, paths []string) (ids []string, ok bool) {
	for _, p := range paths {
		id, found := lib.SampleIDForPath(p)
		if !found {
			return nil, false
		}
		ids = append(ids, id)
	}
	return ids, len(ids) > 0
}

// handleFileDrop decides what an OS file drop means. Files of this library
// are our own drag-out coming back: on a tag or collection in the sidebar
// that assigns it, anywhere else it means nothing, which is also how a drag
// is abandoned, so it must stay silent whatever state the library is in.
// Everything else is an import, assigned to whatever it was dropped on.
func (a *API) handleFileDrop(paths []string, target DropTarget) {
	if len(paths) == 0 {
		return
	}
	s, err := a.open()
	if err != nil {
		a.toastError(err)
		return
	}
	it := target.importTarget()
	if ids, own := ownSamples(s.lib, paths); own {
		a.assign(ids, it)
		return
	}
	if dragout.Active() {
		// Our own drag, but not in a form we can map back to samples;
		// importing our own files into themselves helps nobody.
		return
	}
	jobID, err := s.eng.Import(model.JobImport, it, ingest.PlanPaths(paths))
	if err != nil {
		a.toastError(err)
		return
	}
	a.emit(model.EventDrop, model.DropEvent{Kind: model.DropImport, JobID: jobID, Count: len(paths), Target: it})
}

// assign applies a sidebar target to samples that are already in the
// library, as an ordinary undoable mutation.
func (a *API) assign(ids []string, it model.ImportTarget) {
	var err error
	switch {
	case it.Tag != "":
		err = a.AddTags(ids, []string{it.Tag})
	case it.CollectionID != "":
		err = a.AddToCollection(it.CollectionID, ids)
	default:
		return
	}
	if err != nil {
		a.toastError(err)
		return
	}
	a.emit(model.EventDrop, model.DropEvent{Kind: model.DropAssign, Count: len(ids), Target: it})
}
