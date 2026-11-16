package api

import (
	"slices"
	"strings"

	"ssss/internal/analysis"
	"ssss/internal/model"
)

// Search returns one page of samples matching req.
func (a *API) Search(req model.SearchRequest) model.SearchResult {
	s := a.current()
	if s == nil || s.idx == nil {
		return model.SearchResult{Rows: []model.Sample{}}
	}
	res, err := s.idx.Search(req)
	if err != nil {
		res.Error = sentence(err.Error())
	}
	if res.Rows == nil {
		res.Rows = []model.Sample{}
	}
	return res
}

// SearchIDs returns the ids of every sample matching query, in sort order,
// for select-all and shift-range across pages that are not loaded.
func (a *API) SearchIDs(query, sort string, desc bool) []string {
	s := a.current()
	if s == nil || s.idx == nil {
		return []string{}
	}
	ids, err := s.idx.SearchIDs(query, sort, desc)
	if err != nil {
		a.toastError(err)
	}
	if ids == nil {
		ids = []string{}
	}
	return ids
}

// GetSamples returns details for ids; unknown ids are skipped.
func (a *API) GetSamples(ids []string) []model.SampleDetail {
	s := a.current()
	switch {
	case s == nil:
		return []model.SampleDetail{}
	case s.lib == nil:
		return cachedDetails(s, ids)
	}
	details := s.lib.Details(ids)
	for i := range details {
		d := &details[i]
		d.SuggestedTags = suggestTags(d.RelPath, d.Tags)
	}
	return details
}

// cachedDetails answers from the index while the library folder is away.
func cachedDetails(s *session, ids []string) []model.SampleDetail {
	out := []model.SampleDetail{}
	if s.idx == nil {
		return out
	}
	rows, err := s.idx.Samples(ids)
	if err != nil {
		return out
	}
	colls, _ := s.idx.SampleCollections(ids)
	for _, row := range rows {
		d := model.SampleDetail{
			Sample:        row.Sample,
			Collections:   colls[row.ID],
			Note:          row.Note,
			SuggestedTags: []string{},
		}
		if d.Tags == nil {
			d.Tags = []string{}
		}
		if d.Collections == nil {
			d.Collections = []string{}
		}
		out = append(out, d)
	}
	return out
}

// suggestTags derives tags from the file and folder names, leaving out the
// ones the sample already has or already has a more specific form of.
func suggestTags(relPath string, have []string) []string {
	out := []string{}
	for _, tag := range analysis.ParseName(relPath).SuggestedTags {
		covered := slices.ContainsFunc(have, func(h string) bool {
			return h == tag || strings.HasPrefix(h, tag+"/")
		})
		if !covered && !slices.Contains(out, tag) {
			out = append(out, tag)
		}
	}
	return out
}

// GetSidebar returns the sidebar counts and lists.
func (a *API) GetSidebar() model.Sidebar {
	empty := model.Sidebar{
		Tags:        []model.TagCount{},
		Packs:       []model.PackInfo{},
		Collections: []model.CollectionInfo{},
		Views:       []model.SavedView{},
	}
	s := a.current()
	if s == nil || s.idx == nil {
		return empty
	}
	var views []model.SavedView
	var colors map[string]string
	if s.lib != nil {
		views, colors = s.lib.Views(), s.lib.TagColors()
	}
	sb, err := s.idx.Sidebar(views, colors)
	if err != nil {
		a.toastError(err)
		return empty
	}
	return sb
}
