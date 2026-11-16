// Api over the generated Wails bindings. Go marshals nil slices as null and
// types enums as plain strings; this module narrows both so the rest of the
// frontend can rely on lib/types.ts.

import { Events } from "@wailsio/runtime";
import { API } from "../../bindings/ssss/internal/api";
import "../../bindings/events";
import type { Api } from "./api";
import { audioUrl, peaksUrl } from "./urls";
import type {
  ApiEventName,
  ApiEvents,
  AppState,
  Progress,
  Sample,
  SampleDetail,
  SearchResult,
  Settings,
  Sidebar,
  SpliceInfo,
} from "./types";

const list = <T>(v: T[] | null | undefined): T[] => v ?? [];

function sample<T extends { tags: string[] | null }>(s: T): T {
  s.tags = list(s.tags);
  return s;
}

function detail(d: SampleDetail): SampleDetail {
  sample(d);
  d.collections = list(d.collections);
  d.suggestedTags = list(d.suggestedTags);
  return d;
}

function settings(s: Settings): Settings {
  s.pinnedTags = list(s.pinnedTags);
  return s;
}

function appState(s: AppState): AppState {
  settings(s.settings);
  return s;
}

function sidebar(s: Sidebar): Sidebar {
  s.tags = list(s.tags);
  s.packs = list(s.packs);
  s.collections = list(s.collections);
  s.views = list(s.views);
  return s;
}

function progress(p: Progress): Progress {
  p.errors = list(p.errors);
  return p;
}

const normaliseEvent: { [E in ApiEventName]?: (data: ApiEvents[E]) => ApiEvents[E] } = {
  state: appState,
  "ingest:progress": progress,
  "analysis:progress": progress,
};

export function createWailsApi(): Api {
  return {
    isMock: false,

    getState: async () => appState((await API.GetState()) as AppState),
    createLibrary: async (path, name) => appState((await API.CreateLibrary(path, name)) as AppState),
    openLibrary: async (path) => appState((await API.OpenLibrary(path)) as AppState),
    chooseFolder: (title) => API.ChooseFolder(title),
    chooseFiles: async (title) => list(await API.ChooseFiles(title)),
    retryLibrary: async () => appState((await API.RetryLibrary()) as AppState),
    rescan: () => API.Rescan(),
    rebuildCache: () => API.RebuildCache(),
    updateSettings: async (s) => settings((await API.UpdateSettings(s)) as Settings),

    search: async (req) => {
      const res = (await API.Search(req)) as SearchResult;
      res.rows = list(res.rows as Sample[] | null).map(sample);
      return res;
    },
    searchIds: async (query, sort, desc) => list(await API.SearchIDs(query, sort, desc)),
    getSamples: async (ids) => list((await API.GetSamples(ids)) as SampleDetail[] | null).map(detail),
    getSidebar: async () => sidebar((await API.GetSidebar()) as Sidebar),

    addTags: (ids, tags) => API.AddTags(ids, tags),
    removeTags: (ids, tags) => API.RemoveTags(ids, tags),
    renameTag: (oldTag, newTag) => API.RenameTag(oldTag, newTag),
    deleteTag: (tag) => API.DeleteTag(tag),
    setTagColor: (tag, color) => API.SetTagColor(tag, color),
    setFavorite: (ids, fav) => API.SetFavorite(ids, fav),
    setMeta: (ids, patch) =>
      API.SetMeta(ids, {
        bpm: patch.bpm ?? null,
        key: patch.key ?? null,
        type: patch.type ?? null,
        note: patch.note ?? null,
      }),
    renameSample: (id, newName) => API.RenameSample(id, newName),
    removeSamples: (ids) => API.RemoveSamples(ids),
    createCollection: (name) => API.CreateCollection(name),
    renameCollection: (id, name) => API.RenameCollection(id, name),
    deleteCollection: (id) => API.DeleteCollection(id),
    addToCollection: (id, sampleIds) => API.AddToCollection(id, sampleIds),
    removeFromCollection: (id, sampleIds) => API.RemoveFromCollection(id, sampleIds),
    renamePack: (id, name) => API.RenamePack(id, name),
    saveView: (name, query) => API.SaveView(name, query),
    deleteView: (id) => API.DeleteView(id),
    undo: () => API.Undo(),
    redo: () => API.Redo(),
    reanalyze: (ids) => API.Reanalyze(ids),

    import: (paths, target) =>
      API.Import(paths, {
        packId: target?.packId ?? "",
        tag: target?.tag ?? "",
        collectionId: target?.collectionId ?? "",
      }),
    cancelJob: (jobId) => API.CancelJob(jobId),
    detectSplice: async () => {
      const info = (await API.DetectSplice()) as SpliceInfo;
      info.packs = list(info.packs);
      return info;
    },
    importSplice: (folder, packNames) => API.ImportSplice(folder, packNames),

    startDrag: (ids) => API.StartDrag(ids),
    reveal: (id) => API.Reveal(id),
    copyPaths: (ids) => API.CopyPaths(ids),

    on<E extends ApiEventName>(event: E, cb: (data: ApiEvents[E]) => void): () => void {
      const fix = normaliseEvent[event] as ((data: ApiEvents[E]) => ApiEvents[E]) | undefined;
      return Events.On(event, (ev) => {
        const data = ev.data as ApiEvents[E];
        cb(fix ? fix(data) : data);
      });
    },

    audioUrl,
    peaksUrl,
  };
}
