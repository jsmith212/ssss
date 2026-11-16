package splice

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ssss/internal/fsx"
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
// at "packs" or at any folder of pack folders. Names are matched without
// regard to case, as Splice's own spelling differs between platforms.
func packsRoot(folder string) string {
	for _, sub := range [][]string{{"sounds", "packs"}, {"samples", "packs"}, {"packs"}} {
		dir, ok := folder, true
		for _, name := range sub {
			if dir, ok = childDir(dir, name); !ok {
				break
			}
		}
		if ok {
			return dir
		}
	}
	return folder
}

// childDir returns the subfolder of dir called name, ignoring case.
func childDir(dir, name string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), name) && isDir(dir, entry) {
			return filepath.Join(dir, entry.Name()), true
		}
	}
	return "", false
}

// isDir reports whether an entry is a folder, following a link to one.
func isDir(parent string, entry fs.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&fs.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(filepath.Join(parent, entry.Name()))
	return err == nil && info.IsDir()
}

// isAudioFile reports whether the entry at p is an audio file by name and
// kind. A link is judged by what it points at.
func isAudioFile(p string, d fs.DirEntry) bool {
	if !audioExts[strings.ToLower(filepath.Ext(p))] {
		return false
	}
	mode := d.Type()
	if mode&fs.ModeSymlink != 0 {
		info, err := os.Stat(p)
		if err != nil {
			return false
		}
		mode = info.Mode()
	}
	return fsx.IsFile(mode)
}

// Scan enumerates the packs of a Splice folder from its folder tree and, if
// db is not nil, attaches the database's metadata to the files it knows.
// The tree alone is enough to import; the database only adds to it.
//
// Every subfolder of the packs folder that holds audio, at any depth, is a
// pack. Audio lying directly in the packs folder forms one more pack, named
// after the folder the user chose.
func Scan(folder string, db *DB) (*Tree, error) {
	root := packsRoot(folder)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("splice: %w", err)
	}
	tree := &Tree{Folder: folder, HasDB: db != nil}
	loose := Pack{Name: filepath.Base(filepath.Clean(folder))}
	looseGenres := map[string]int{}
	add := func(pack *Pack, genres map[string]int, base, p string) {
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return
		}
		f := File{Path: p, Rel: filepath.ToSlash(rel)}
		if meta, ok := db.Lookup(p); ok {
			f.Meta = meta
			if meta.Genre != "" {
				genres[meta.Genre]++
			}
		}
		pack.Files = append(pack.Files, f)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		p := filepath.Join(root, entry.Name())
		if !isDir(root, entry) {
			if isAudioFile(p, entry) {
				add(&loose, looseGenres, root, p)
			}
			continue
		}
		pack := Pack{Name: entry.Name()}
		genres := map[string]int{}
		// WalkDir does not descend into a root that is a link, so a linked
		// pack folder is walked at its target.
		if entry.Type()&fs.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(p)
			if err != nil {
				continue
			}
			p = target
		}
		// An unreadable subfolder costs its files, not the whole scan.
		_ = filepath.WalkDir(p, func(file string, d fs.DirEntry, err error) error {
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
			if !d.IsDir() && isAudioFile(file, d) {
				add(&pack, genres, p, file)
			}
			return nil
		})
		if len(pack.Files) == 0 {
			continue // presets or MIDI only
		}
		pack.Genre = mostCommon(genres)
		tree.Packs = append(tree.Packs, pack)
	}
	if len(loose.Files) > 0 {
		loose.Genre = mostCommon(looseGenres)
		tree.Packs = append(tree.Packs, loose)
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
