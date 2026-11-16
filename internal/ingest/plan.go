package ingest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"ssss/internal/audio"
	"ssss/internal/library"
)

// UnsortedPack is the pack loose files are imported into.
const UnsortedPack = "Unsorted"

// File is one file to import.
type File struct {
	Path string // absolute source path
	Rel  string // wanted slash-separated path inside the pack
	// Metadata the source already knows; recorded with MetaSource.
	Tags []string
	BPM  float64
	Key  string // library notation
	Type string // model.TypeLoop, model.TypeOneShot or ""
}

// Group is a set of files that become (or join) one pack.
type Group struct {
	Name   string // pack display name
	Dir    string // pack folder under Samples; "" derives it from Name
	Source string // library.SourceImport, library.SourceSplice
	// Loose marks individually picked files, as opposed to a folder: when
	// the import targets an existing pack, a folder keeps its own name as
	// a subfolder there and loose files do not.
	Loose bool
	// MetaSource is the source recorded for File metadata, e.g.
	// model.SrcSplice.
	MetaSource string
	Files      []File
}

// Plan is what an import is going to copy.
type Plan struct {
	Groups []Group
	// Skipped lists explicitly chosen paths that cannot be imported, as
	// "name: reason".
	Skipped []string
}

// Planner builds the plan inside the import job, so that walking a large
// folder neither blocks the caller nor escapes cancellation.
type Planner func(ctx context.Context) (Plan, error)

// skipName reports whether a directory entry is never part of a library:
// hidden files, sync-client temp files and our own partial copies.
func skipName(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~$") ||
		strings.HasSuffix(name, ".part") || strings.HasSuffix(name, "~")
}

// IsAudio reports whether the file's content is in a format we can decode.
func IsAudio(path string) (audio.Format, bool) {
	format, err := audio.Sniff(path)
	return format, err == nil
}

// PlanPaths plans an import of dropped or picked paths: each folder becomes
// a pack named after it, loose files go to the Unsorted pack. Files that are
// not audio are left out. Whatever the user chose and gets nothing from is
// reported in Plan.Skipped: a loose file that is not audio, a folder without
// any, and symbolic links inside a folder that are not followed.
func PlanPaths(paths []string) Planner {
	return func(ctx context.Context) (Plan, error) {
		var plan Plan
		loose := Group{Name: UnsortedPack, Source: library.SourceImport, Loose: true}
		for _, p := range paths {
			if err := ctx.Err(); err != nil {
				return plan, err
			}
			p = filepath.Clean(p)
			name := filepath.Base(p)
			info, err := os.Stat(p)
			if err != nil {
				plan.Skipped = append(plan.Skipped, fmt.Sprintf("%s: %v", name, unwrapPath(err)))
				continue
			}
			switch {
			case info.IsDir():
				// The walk does not follow a link it is started on, so a
				// linked folder is walked at its target; the pack still
				// gets the name the user sees.
				root, err := filepath.EvalSymlinks(p)
				if err != nil {
					plan.Skipped = append(plan.Skipped, fmt.Sprintf("%s: %v", name, unwrapPath(err)))
					continue
				}
				files, links, err := walkAudio(ctx, root)
				if err != nil {
					return plan, err
				}
				if links > 0 {
					plan.Skipped = append(plan.Skipped, fmt.Sprintf("%s: %d linked folders inside it were not followed", name, links))
				}
				if len(files) == 0 {
					plan.Skipped = append(plan.Skipped, name+": no audio files found")
					continue
				}
				plan.Groups = append(plan.Groups, Group{Name: name, Source: library.SourceImport, Files: files})
			case !info.Mode().IsRegular():
				plan.Skipped = append(plan.Skipped, name+": not a file")
			default:
				if _, ok := IsAudio(p); !ok {
					plan.Skipped = append(plan.Skipped, name+": not a supported audio file")
					continue
				}
				loose.Files = append(loose.Files, File{Path: p, Rel: name})
			}
		}
		if len(loose.Files) > 0 {
			plan.Groups = append(plan.Groups, loose)
		}
		return plan, nil
	}
}

// walkAudio lists the audio files under root in directory order. Unreadable
// subfolders are skipped: one bad folder should not sink a large import. A
// symbolic link to a file is imported like the file. One to a folder is not
// followed (it may loop, or lead back into the library) and is counted in
// links.
func walkAudio(ctx context.Context, root string) (files []File, links int, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			if d != nil && d.IsDir() && p != root {
				return fs.SkipDir
			}
			return nil
		}
		if p != root && skipName(d.Name()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Stat(p)
			switch {
			case err != nil:
				return nil // dangling
			case target.IsDir():
				links++
				return nil
			case !target.Mode().IsRegular():
				return nil
			}
		} else if !d.Type().IsRegular() {
			return nil
		}
		if _, ok := IsAudio(p); !ok {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		files = append(files, File{Path: p, Rel: filepath.ToSlash(rel)})
		return nil
	})
	return files, links, err
}

// unwrapPath drops the path from an *fs.PathError, for messages that already
// name the file.
func unwrapPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
