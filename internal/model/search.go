package model

// Sort values for SearchRequest.Sort.
const (
	SortAdded     = "added"
	SortName      = "name"
	SortBPM       = "bpm"
	SortKey       = "key"
	SortDur       = "dur"
	SortPack      = "pack"
	SortRelevance = "relevance"
)

// SearchRequest asks for one page of the list.
type SearchRequest struct {
	Query  string `json:"query"` // omnibar string
	Sort   string `json:"sort"`  // added|name|bpm|key|dur|pack|relevance
	Desc   bool   `json:"desc"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// SearchResult is one page of matches plus the total match count.
type SearchResult struct {
	Total int      `json:"total"`
	Rows  []Sample `json:"rows"`
	Error string   `json:"error"` // query parse problem, human readable; rows still best-effort
}

// TagCount is a sidebar tag entry. Count includes descendants.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
	Color string `json:"color"`
}

// PackInfo is a sidebar pack entry.
type PackInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Dir    string `json:"dir"`
	Source string `json:"source"`
	Count  int    `json:"count"`
}

// CollectionInfo is a sidebar collection entry.
type CollectionInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// SavedView is a saved search.
type SavedView struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Query string `json:"query"`
}

// Sidebar carries everything the sidebar shows.
type Sidebar struct {
	All         int              `json:"all"`
	Favorites   int              `json:"favorites"`
	Untagged    int              `json:"untagged"`
	Recent      int              `json:"recent"` // last import batch
	Tags        []TagCount       `json:"tags"`
	Packs       []PackInfo       `json:"packs"`
	Collections []CollectionInfo `json:"collections"`
	Views       []SavedView      `json:"views"`
}
