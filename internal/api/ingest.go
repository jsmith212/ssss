package api

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ssss/internal/ingest"
	"ssss/internal/library"
	"ssss/internal/model"
	"ssss/internal/splice"
)

// Import copies files and folders into the library in the background and
// returns the job id; progress arrives as "ingest:progress" events.
func (a *API) Import(paths []string, target model.ImportTarget) (string, error) {
	s, err := a.writable()
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", errors.New("nothing to import")
	}
	return s.eng.Import(model.JobImport, target, ingest.PlanPaths(paths))
}

// CancelJob cancels a running import, rescan or analysis job.
func (a *API) CancelJob(jobId string) error {
	s, err := a.open()
	if err != nil {
		return err
	}
	if !s.eng.Cancel(jobId) {
		return fmt.Errorf("job %s is not running", jobId)
	}
	return nil
}

// spliceFolder is the configured Splice folder, or the first default
// location that exists.
func (a *API) spliceFolder() (string, bool) {
	a.mu.RLock()
	override := a.cfg.Settings.SpliceFolder
	a.mu.RUnlock()
	return splice.Locate(override)
}

func splicePackDir(name string) string {
	return splice.PackDirPrefix + "/" + library.SanitizeComponent(name)
}

// DetectSplice looks for a local Splice folder and lists its packs.
func (a *API) DetectSplice() model.SpliceInfo {
	info := model.SpliceInfo{Packs: []model.SplicePack{}}
	folder, ok := a.spliceFolder()
	if !ok {
		return info
	}
	info.Folder = folder
	tree, err := splice.Open(folder)
	if err != nil {
		return info
	}
	info.Found, info.HasDB = true, tree.HasDB
	s := a.current()
	for _, p := range tree.Packs {
		pack := model.SplicePack{Name: p.Name, Count: len(p.Files), Genre: p.Genre}
		if s != nil && s.lib != nil {
			if existing, ok := s.lib.PackByDir(splicePackDir(p.Name)); ok {
				pack.Imported = existing.Count > 0
			}
		}
		info.Packs = append(info.Packs, pack)
	}
	return info
}

// ImportSplice imports the named packs from a Splice folder and returns the
// job id. No names means every pack.
func (a *API) ImportSplice(folder string, packNames []string) (string, error) {
	s, err := a.writable()
	if err != nil {
		return "", err
	}
	if folder = strings.TrimSpace(folder); folder == "" {
		var ok bool
		if folder, ok = a.spliceFolder(); !ok {
			return "", errors.New("no Splice folder found; set one in Settings")
		}
	}
	plan := func(ctx context.Context) (ingest.Plan, error) {
		tree, err := splice.Open(folder)
		if err != nil {
			return ingest.Plan{}, err
		}
		var plan ingest.Plan
		for _, p := range tree.Packs {
			if len(packNames) > 0 && !slices.Contains(packNames, p.Name) {
				continue
			}
			plan.Groups = append(plan.Groups, spliceGroup(p))
		}
		return plan, ctx.Err()
	}
	return s.eng.Import(model.JobSplice, model.ImportTarget{}, plan)
}

func spliceGroup(p splice.Pack) ingest.Group {
	g := ingest.Group{
		Name:       p.Name,
		Dir:        splicePackDir(p.Name),
		Source:     library.SourceSplice,
		MetaSource: model.SrcSplice,
		Files:      make([]ingest.File, len(p.Files)),
	}
	for i, f := range p.Files {
		g.Files[i] = ingest.File{
			Path: f.Path, Rel: f.Rel,
			Tags: f.Meta.Tags, BPM: f.Meta.BPM, Key: f.Meta.Key, Type: f.Meta.Type,
		}
	}
	return g
}
