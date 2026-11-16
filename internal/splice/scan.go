package splice

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// PackDirPrefix is the folder under Samples that imported Splice packs go
// into, one subfolder per pack.
const PackDirPrefix = "Splice"

// File is one audio file of a Splice pack.
type File struct {
	Path string // absolute
	Rel  string // slash-separated, relative to the pack folder
	Meta Meta   // zero when the database does not know the file
}

// Pack is one pack folder in the Splice tree.
type Pack struct {
	Name  string
	Genre string
	Files []File
}

// Tree is a scanned Splice folder.
type Tree struct {
	Folder string
	HasDB  bool
	Packs  []Pack
}

var audioExts = map[string]bool{
	".wav": true, ".wave": true, ".aif": true, ".aiff": true, ".aifc": true,
	".flac": true, ".mp3": true, ".ogg": true,
}

// packsRoot finds the folder whose subfolders are packs. Splice lays its
// folder out as sounds/packs/<Pack Name>; a user may also point us straight
// at "packs" or at any folder of pack folders.
func packsRoot(folder string) string {
	for _, sub := range []string{filepath.Join("sounds", "packs"), "packs"} {
		dir := filepath.Join(folder, sub)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return folder
}

// Scan enumerates the packs of a Splice folder from its folder tree and, if
// db is not nil, attaches the database's metadata to the files it knows.
// The tree alone is enough to import; the database only adds to it.
func Scan(folder string, db *DB) (*Tree, error) {
	root := packsRoot(folder)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("splice: %w", err)
	}
	tree := &Tree{Folder: folder, HasDB: db != nil}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		pack := Pack{Name: entry.Name()}
		dir := filepath.Join(root, entry.Name())
		genres := map[string]int{}
		// An unreadable subfolder costs its files, not the whole scan.
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || !audioExts[strings.ToLower(filepath.Ext(p))] {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return nil
			}
			f := File{Path: p, Rel: filepath.ToSlash(rel)}
			if meta, ok := db.Lookup(p); ok {
				f.Meta = meta
				if meta.Genre != "" {
					genres[meta.Genre]++
				}
			}
			pack.Files = append(pack.Files, f)
			return nil
		})
		if len(pack.Files) == 0 {
			continue // presets or MIDI only
		}
		pack.Genre = mostCommon(genres)
		tree.Packs = append(tree.Packs, pack)
	}
	slices.SortFunc(tree.Packs, func(a, b Pack) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return tree, nil
}

func mostCommon(counts map[string]int) string {
	best, bestN := "", 0
	for value, n := range counts {
		if n > bestN || n == bestN && value < best {
			best, bestN = value, n
		}
	}
	return best
}

// Open locates the database for folder, reads it if there is one and scans
// the folder. A database that cannot be read is treated as absent: the
// folder tree alone is a complete import source.
func Open(folder string) (*Tree, error) {
	var db *DB
	if path, ok := FindDB(folder); ok {
		if read, err := ReadDB(path); err == nil && read.Len() > 0 {
			db = read
		}
	}
	return Scan(folder, db)
}
