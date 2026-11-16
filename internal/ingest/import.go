package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"ssss/internal/library"
	"ssss/internal/model"
)

// idBytes is how much of the SHA-256 forms a sample ID.
const idBytes = 16

// An import copies each file to Samples/~$job_<id>-<n>.part first.
const (
	importTempPrefix = "~$job_"
	importTempSuffix = ".part"
)

// isImportTemp recognises the temp files of imports, and only those: a
// file of the user's that merely ends in ".part" is not ours to delete.
func isImportTemp(name string) bool {
	return strings.HasPrefix(name, importTempPrefix) && strings.HasSuffix(name, importTempSuffix)
}

// ctxReader makes a long copy stop soon after its context is cancelled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// HashFile computes the sample ID of a file's content.
func HashFile(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, ctxReader{ctx, f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)[:idBytes]), nil
}

// Import starts copying the planned files into the library and returns the
// job ID. The target's tag and collection are applied to every file of the
// plan, including those that turn out to be in the library already.
func (e *Engine) Import(kind string, target model.ImportTarget, plan Planner) (string, error) {
	if e.lib.ReadOnly() {
		return "", library.ErrReadOnly
	}
	target.Tag = library.NormalizeTag(target.Tag)
	if target.PackID != "" && !e.hasPack(target.PackID) {
		return "", fmt.Errorf("pack %s: %w", target.PackID, library.ErrNotFound)
	}
	if target.CollectionID != "" {
		if _, ok := e.lib.CollectionItems(target.CollectionID); !ok {
			return "", fmt.Errorf("collection %s: %w", target.CollectionID, library.ErrNotFound)
		}
	}
	j, ctx := e.startJob(kind)
	go e.runImport(ctx, j, target, plan)
	return j.id(), nil
}

func (e *Engine) hasPack(id string) bool {
	for _, p := range e.lib.Packs() {
		if p.ID == id {
			return true
		}
	}
	return false
}

// record indexes a library change made by background work and queues its
// notification.
func (e *Engine) record(ch library.Change, batch int64) {
	e.fail(e.apply(ch, batch))
	e.changes.add(ch)
}

func (e *Engine) runImport(ctx context.Context, j *job, target model.ImportTarget, planner Planner) {
	e.analysis.hold()
	defer e.analysis.release()
	plan, err := planner(ctx)
	if err != nil && ctx.Err() == nil {
		j.addError("import", err)
	}
	total := 0
	for _, g := range plan.Groups {
		total += len(g.Files)
	}
	j.update(func(p *model.Progress) {
		p.Total = total + len(plan.Skipped)
		p.Done = len(plan.Skipped)
		p.Failed += len(plan.Skipped)
		p.Errors = append(p.Errors, plan.Skipped[:min(len(plan.Skipped), model.MaxProgressErrors-len(p.Errors))]...)
	})

	var batch int64
	if total > 0 {
		if batch, err = e.idx.NewBatch(); err != nil {
			e.fail(err)
		}
	}
	im := &importer{e: e, j: j, batch: batch}
	lost := false
	for _, g := range plan.Groups {
		if ctx.Err() != nil || lost {
			break
		}
		packID, prefix := target.PackID, ""
		if packID != "" && !g.Loose {
			prefix = g.Name
		}
		for _, f := range g.Files {
			if ctx.Err() != nil {
				break
			}
			name := filepath.Base(f.Path)
			j.update(func(p *model.Progress) { p.Current = name })
			f.Rel = path.Join(prefix, f.Rel)
			added, err := im.importFile(ctx, &packID, g, f)
			switch {
			case err != nil && ctx.Err() != nil:
				// cancelled mid-copy; not a failure of this file
			case errors.Is(err, library.ErrUnavailable):
				// The library folder went away (drive unplugged, folder
				// moved). Every further file would fail the same way, or
				// worse, be copied into whatever is at that path now.
				j.addError("import", err)
				e.fail(fmt.Errorf("import stopped: %w", err))
				lost = true
			case err != nil:
				j.addError(name, unwrapPath(err))
				j.update(func(p *model.Progress) { p.Done++ })
			case added:
				j.update(func(p *model.Progress) { p.Done++; p.Added++ })
			default:
				j.update(func(p *model.Progress) { p.Done++; p.Duplicates++ })
			}
			if lost {
				break
			}
		}
	}
	im.assign(target)
	j.finish(ctx.Err() != nil || lost, false)
}

// importer carries the state of one import job.
type importer struct {
	e     *Engine
	j     *job
	batch int64
	n     int
	// seen are the IDs of every processed file, new or duplicate.
	seen []string
}

// importFile copies one file into the pack, creating the pack on first use.
// It reports whether the file was new to the library.
func (im *importer) importFile(ctx context.Context, packID *string, g Group, f File) (bool, error) {
	e := im.e
	src, err := os.Open(f.Path)
	if err != nil {
		return false, err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return false, err
	}
	format, ok := IsAudio(f.Path)
	if !ok {
		return false, errors.New("not a supported audio file")
	}

	// Nothing is created under a root that has stopped being the library;
	// see library.Available. Samples itself can be missing (a library whose
	// audio has not synced yet) and is the one folder made here.
	if err := e.lib.Available(); err != nil {
		return false, err
	}
	if err := os.Mkdir(e.lib.SamplesDir(), 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return false, err
	}
	// The temp name is one sync clients ignore, and it sits in Samples so
	// the final rename never crosses a file system.
	im.n++
	tmp := filepath.Join(e.lib.SamplesDir(), importTempPrefix+im.j.id()+"-"+strconv.Itoa(im.n)+importTempSuffix)
	dst, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return false, err
	}
	placed := false
	defer func() {
		if !placed {
			os.Remove(tmp)
		}
	}()
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(dst, h), ctxReader{ctx, src})
	if err == nil {
		err = dst.Sync()
	}
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, err
	}
	id := hex.EncodeToString(h.Sum(nil)[:idBytes])
	if e.lib.Has(id) {
		im.seen = append(im.seen, id)
		return false, nil
	}

	if *packID == "" {
		if *packID, err = im.resolvePack(g); err != nil {
			return false, err
		}
	}
	// Best effort: a file system that refuses to set times still gets the
	// file, with the copy's own time.
	_ = os.Chtimes(tmp, info.ModTime(), info.ModTime())
	rec := library.SampleRecord{
		Path: f.Rel, Size: size, MTime: info.ModTime().UTC(), Fmt: string(format),
		Tags: f.Tags,
	}
	if g.MetaSource != "" {
		if f.BPM > 0 {
			rec.BPM, rec.BPMSrc = f.BPM, g.MetaSource
		}
		if f.Key != "" {
			rec.Key, rec.KeySrc = f.Key, g.MetaSource
		}
		if f.Type != "" {
			rec.Type, rec.TypeSrc = f.Type, g.MetaSource
		}
	}
	ch, err := e.lib.AddSample(*packID, id, rec, func(abs string) error {
		if err := os.Rename(tmp, abs); err != nil {
			return err
		}
		placed = true
		return nil
	})
	if errors.Is(err, library.ErrDuplicate) {
		im.seen = append(im.seen, id)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	e.record(ch, im.batch)
	im.seen = append(im.seen, id)
	// Analysis runs alongside the copy: rows appear at once and fill in.
	e.analysis.enqueue([]string{id})
	return true, nil
}

// resolvePack finds the pack stored in the group's folder or creates it.
func (im *importer) resolvePack(g Group) (string, error) {
	dir := g.Dir
	if dir == "" {
		dir = g.Name
	}
	if info, ok := im.e.lib.PackByDir(library.SanitizeRelPath(dir)); ok {
		return info.ID, nil
	}
	info, ch, err := im.e.lib.CreatePack(g.Name, dir, g.Source)
	if err != nil {
		return "", err
	}
	im.e.record(ch, 0)
	return info.ID, nil
}

// assign applies the drop target's tag and collection to everything the job
// processed, so dropping files that are already in the library on a tag
// still tags them.
func (im *importer) assign(target model.ImportTarget) {
	if len(im.seen) == 0 || im.e.lib.Available() != nil {
		return
	}
	if target.Tag != "" {
		ch, err := im.e.lib.AddTags(im.seen, []string{target.Tag})
		if err != nil {
			im.e.fail(fmt.Errorf("tag imported samples: %w", err))
		}
		im.e.record(ch, 0)
	}
	if target.CollectionID != "" {
		ch, err := im.e.lib.AddToCollection(target.CollectionID, im.seen)
		if err != nil {
			im.e.fail(fmt.Errorf("add imported samples to the collection: %w", err))
		}
		im.e.record(ch, 0)
	}
}
