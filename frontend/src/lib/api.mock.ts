// In-memory backend used when the frontend runs outside Wails (npm run dev).
// It behaves like the real service closely enough to build and review the
// whole UI in a browser: search, sorting, paging, sidebar counts, mutations
// with undo, fake import progress, and synthesised audio and peaks.
//
// URL parameters: ?state=none|unavailable|readonly|empty picks the library
// status, ?n=5000 the number of samples. `window.__ssssMock.drop(paths, kind,
// id)` simulates an OS file drop on a sidebar target.

import type { Api } from "./api";
import { normaliseTag } from "./format";
import { encodeF32, fullPeaks, miniPeaks, render, type Rendered } from "./mock/audio";
import {
  SPLICE_PACKS,
  generateLibrary,
  genreIndex,
  makeSample,
  reindex,
  type MockCollection,
  type MockLibrary,
  type MockPack,
  type MockSample,
} from "./mock/data";
import { keyRank, normaliseKey } from "./mock/keys";
import { matches, parseQuery, type ParsedQuery, type QueryContext } from "./mock/query";
import { Rng } from "./mock/rng";
import { audioUrl, peaksUrl } from "./urls";
import type {
  ApiEventName,
  ApiEvents,
  AppState,
  CollectionInfo,
  DragResult,
  ImportTarget,
  LibraryStatus,
  MetaPatch,
  Progress,
  Sample,
  SampleDetail,
  SavedView,
  SearchRequest,
  SearchResult,
  Settings,
  Sidebar,
  SortKey,
  SpliceInfo,
  TagCount,
} from "./types";

const LIBRARY_PATH = "/home/you/Music/Samples";
const AUDIO_EXT = /\.(wav|aiff?|aifc|flac|mp3|ogg)$/i;

interface UndoEntry {
  description: string;
  undo(): void;
  redo(): void;
}

interface Job {
  id: string;
  cancelled: boolean;
}

type Listener = (data: never) => void;

const lower = (s: string) => s.toLowerCase();
const baseName = (path: string) => path.replace(/[\\/]+$/, "").split(/[\\/]/).pop() ?? path;
const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

function defaultSettings(): Settings {
  return {
    theme: "system",
    density: "regular",
    playOnSelect: true,
    volume: 0.8,
    projectBpm: 124,
    projectKey: "Am",
    matchTempo: false,
    matchKey: false,
    sidebarWidth: 220,
    sidebarOpen: true,
    pinnedTags: ["drums/kick", "drums/snare", "drums/hat", "bass", "synth/lead"],
    spliceFolder: "",
  };
}

/** Ascending comparator for a sort key; samples with unknown values go last. */
function comparator(sort: SortKey, text: string[]): (a: MockSample, b: MockSample) => number {
  const unknownLast = (v: number) => (v > 0 ? v : Number.POSITIVE_INFINITY);
  switch (sort) {
    case "name":
      return (a, b) => (lower(a.name) < lower(b.name) ? -1 : lower(a.name) > lower(b.name) ? 1 : 0);
    case "bpm":
      return (a, b) => unknownLast(a.bpm) - unknownLast(b.bpm);
    case "key":
      return (a, b) => {
        const ra = keyRank(a.key);
        const rb = keyRank(b.key);
        return ra === rb ? 0 : ra < rb ? -1 : 1;
      };
    case "dur":
      return (a, b) => a.dur - b.dur;
    case "pack":
      return (a, b) =>
        a.packName < b.packName ? -1 : a.packName > b.packName ? 1 : a.relPath < b.relPath ? -1 : 1;
    case "relevance": {
      const score = (s: MockSample) => {
        const name = lower(s.name);
        let n = 0;
        for (const t of text) n += name.startsWith(t) ? 3 : name.includes(t) ? 2 : 1;
        return n;
      };
      // Ascending means "least relevant first" so that desc=true is best first.
      return (a, b) => score(a) - score(b) || a.added - b.added;
    }
    default:
      return (a, b) => a.added - b.added;
  }
}

export class MockBackend implements Api {
  readonly isMock = true;

  private lib: MockLibrary;
  private byId = new Map<string, MockSample>();
  private status: LibraryStatus;
  private settings = defaultSettings();
  private listeners = new Map<string, Set<Listener>>();
  private undoStack: UndoEntry[] = [];
  private redoStack: UndoEntry[] = [];
  private jobs = new Map<string, Job>();
  private rng = new Rng(0xc0ffee);
  private rendered = new Map<string, Rendered>();
  /** Sorted match lists keyed by query+sort; dropped on any mutation. */
  private resultCache = new Map<string, { rows: MockSample[]; error: string }>();
  /** What analysis will "find" for freshly imported samples. */
  private pendingAnalysis = new Map<string, Pick<MockSample, "bpm" | "bpmSrc" | "key" | "keySrc" | "keyRoot" | "type">>();
  private sidebarCache: Sidebar | null = null;

  constructor(options: { status?: LibraryStatus; samples?: number } = {}) {
    this.status = options.status ?? "ok";
    this.lib = generateLibrary(options.samples ?? 20000);
    for (const s of this.lib.samples) this.byId.set(s.id, s);
  }

  // --- events ---------------------------------------------------------------

  on<E extends ApiEventName>(event: E, cb: (data: ApiEvents[E]) => void): () => void {
    let set = this.listeners.get(event);
    if (!set) this.listeners.set(event, (set = new Set()));
    set.add(cb as Listener);
    return () => set.delete(cb as Listener);
  }

  private emit<E extends ApiEventName>(event: E, data: ApiEvents[E]): void {
    // Events from Go arrive asynchronously; keep that ordering here.
    queueMicrotask(() => {
      for (const cb of this.listeners.get(event) ?? []) (cb as (d: ApiEvents[E]) => void)(data);
    });
  }

  private changed(ids: string[] | null, structural: boolean): void {
    this.resultCache.clear();
    if (structural) this.sidebarCache = null;
    this.emit("samples:changed", { ids, structural });
  }

  // --- lifecycle ------------------------------------------------------------

  private appState(): AppState {
    const messages: Partial<Record<LibraryStatus, string>> = {
      unavailable: `${LIBRARY_PATH} is not reachable`,
      readonly: "This library was written by a newer version of ssss",
    };
    return {
      status: this.status,
      libraryPath: this.status === "none" ? "" : LIBRARY_PATH,
      libraryName: this.status === "none" ? "" : "Main",
      message: messages[this.status] ?? "",
      platform: /Mac/i.test(navigator.platform) ? "darwin" : /Win/i.test(navigator.platform) ? "windows" : "linux",
      defaultLibraryPath: LIBRARY_PATH,
      settings: structuredClone(this.settings),
    };
  }

  private setStatus(status: LibraryStatus): AppState {
    this.status = status;
    const state = this.appState();
    this.emit("state", state);
    this.changed(null, true);
    return state;
  }

  async getState(): Promise<AppState> {
    return this.appState();
  }

  async createLibrary(path: string, _name: string): Promise<AppState> {
    if (!path.trim()) throw new Error("Choose a folder for the library");
    // A new library starts with nothing in it, as the real one does.
    this.lib = { ...generateLibrary(0), collections: [], views: [] };
    this.byId.clear();
    this.undoStack = [];
    this.redoStack = [];
    return this.setStatus("ok");
  }

  async openLibrary(path: string): Promise<AppState> {
    if (!path.trim()) throw new Error("Choose a library folder");
    return this.setStatus("ok");
  }

  async chooseFolder(_title: string): Promise<string> {
    return LIBRARY_PATH;
  }

  async chooseFiles(_title: string): Promise<string[]> {
    return ["/home/you/Downloads/Kick_Deep_01.wav", "/home/you/Downloads/Bass_Loop_124_Am.wav"];
  }

  async retryLibrary(): Promise<AppState> {
    return this.status === "unavailable" ? this.setStatus("ok") : this.appState();
  }

  async rescan(): Promise<void> {
    void this.runProgress("rescan", "ingest:progress", this.live().length, 1200, () => {});
  }

  async rebuildCache(): Promise<void> {
    this.changed(null, true);
  }

  async updateSettings(settings: Settings): Promise<Settings> {
    this.settings = {
      ...settings,
      volume: Math.min(1, Math.max(0, settings.volume)),
      projectBpm: Math.min(300, Math.max(20, settings.projectBpm || 120)),
      projectKey: normaliseKey(settings.projectKey),
      sidebarWidth: Math.min(320, Math.max(180, Math.round(settings.sidebarWidth))),
      pinnedTags: settings.pinnedTags.slice(0, 9),
    };
    this.emit("state", this.appState());
    return structuredClone(this.settings);
  }

  // --- read -----------------------------------------------------------------

  /** Samples that exist, unless the library has not been opened yet. */
  private live(): MockSample[] {
    return this.status === "none" ? [] : this.lib.samples;
  }

  private queryContext(): QueryContext {
    const pick = <T extends { id: string; name: string }>(items: T[], value: string): T[] => {
      const v = lower(value);
      // As internal/index does it: an id names one item; anything else is a
      // name and means every item called that.
      const byId = items.filter((x) => x.id === value);
      return byId.length ? byId : items.filter((x) => lower(x.name) === v);
    };
    return {
      packIds: (v) => new Set(pick(this.lib.packs, v).map((p) => p.id)),
      collectionSampleIds: (v) => {
        const ids = new Set<string>();
        for (const c of pick(this.lib.collections, v)) for (const id of c.items) ids.add(id);
        return ids;
      },
      recentIds: () => this.lib.recent,
    };
  }

  private results(query: string, sort: SortKey, desc: boolean): { rows: MockSample[]; error: string } {
    const cacheKey = `${sort}\n${desc}\n${query}`;
    const hit = this.resultCache.get(cacheKey);
    if (hit) return hit;

    const parsed: ParsedQuery = parseQuery(query, this.queryContext());
    const rows = this.live().filter((s) => matches(parsed, s));
    const cmp = comparator(sort, parsed.text);
    // Ties fall back to id so paging is stable.
    rows.sort((a, b) => (desc ? cmp(b, a) : cmp(a, b)) || (a.id < b.id ? -1 : 1));

    const entry = { rows, error: parsed.error };
    if (this.resultCache.size > 8) this.resultCache.clear();
    this.resultCache.set(cacheKey, entry);
    return entry;
  }

  private toSample(s: MockSample): Sample {
    if (s.analyzed && !s.mini) s.mini = miniPeaks(s);
    const { hay: _hay, seed: _seed, voice: _voice, note: _note, suggestedTags: _suggested, ...row } = s;
    return { ...row, tags: [...s.tags] };
  }

  async search(req: SearchRequest): Promise<SearchResult> {
    const { rows, error } = this.results(req.query, req.sort, req.desc);
    const offset = Math.max(0, req.offset);
    const page = rows.slice(offset, offset + Math.max(0, req.limit));
    return { total: rows.length, rows: page.map((s) => this.toSample(s)), error };
  }

  async searchIds(query: string, sort: SortKey, desc: boolean): Promise<string[]> {
    return this.results(query, sort, desc).rows.map((s) => s.id);
  }

  async getSamples(ids: string[]): Promise<SampleDetail[]> {
    const out: SampleDetail[] = [];
    for (const id of ids) {
      const s = this.byId.get(id);
      if (!s) continue;
      const pack = this.lib.packs.find((p) => p.id === s.packId);
      out.push({
        ...this.toSample(s),
        absPath: `${LIBRARY_PATH}/Samples/${pack?.dir ?? ""}/${s.relPath}`,
        collections: this.lib.collections.filter((c) => c.items.has(id)).map((c) => c.id),
        note: s.note,
        suggestedTags: s.suggestedTags.filter((t) => !s.tags.includes(t)),
      });
    }
    return out;
  }

  async getSidebar(): Promise<Sidebar> {
    if (this.sidebarCache) return structuredClone(this.sidebarCache);

    const samples = this.live();
    const tagCounts = new Map<string, number>();
    const packCounts = new Map<string, number>();
    let favorites = 0;
    let untagged = 0;
    for (const s of samples) {
      if (s.fav) favorites++;
      if (s.tags.length === 0) untagged++;
      packCounts.set(s.packId, (packCounts.get(s.packId) ?? 0) + 1);
      // A sample counts once for each tag and once for each ancestor.
      const nodes = new Set<string>();
      for (const tag of s.tags) {
        const parts = tag.split("/");
        for (let i = 1; i <= parts.length; i++) nodes.add(parts.slice(0, i).join("/"));
      }
      for (const node of nodes) tagCounts.set(node, (tagCounts.get(node) ?? 0) + 1);
    }

    const tags: TagCount[] = [...tagCounts.entries()]
      .sort(([a], [b]) => (a < b ? -1 : 1))
      .map(([tag, count]) => ({ tag, count, color: this.lib.tagColors.get(tag) ?? "" }));
    const live = new Set(samples.map((s) => s.id));
    const countLive = (ids: Iterable<string>) => {
      let n = 0;
      for (const id of ids) if (live.has(id)) n++;
      return n;
    };

    this.sidebarCache = {
      all: samples.length,
      favorites,
      untagged,
      recent: countLive(this.lib.recent),
      tags,
      packs: this.lib.packs
        .map((p) => ({ ...p, count: packCounts.get(p.id) ?? 0 }))
        .filter((p) => p.count > 0)
        .sort((a, b) => (lower(a.name) < lower(b.name) ? -1 : 1)),
      collections: this.lib.collections.map((c) => ({ id: c.id, name: c.name, count: countLive(c.items) })),
      views: this.lib.views.map((v) => ({ ...v })),
    };
    return structuredClone(this.sidebarCache);
  }

  // --- mutation plumbing ----------------------------------------------------

  private assertWritable(): void {
    if (this.status !== "ok") throw new Error("The library is not writable right now");
  }

  /** Runs `redo` now and records how to revert it. */
  private apply(description: string, redo: () => void, undo: () => void): void {
    this.assertWritable();
    redo();
    this.undoStack.push({ description, undo, redo });
    if (this.undoStack.length > 100) this.undoStack.shift();
    this.redoStack = [];
  }

  /** Edits samples in place, with undo, and announces the change. */
  private editSamples(description: string, ids: string[], structural: boolean, edit: (s: MockSample) => void): void {
    const targets = ids.map((id) => this.byId.get(id)).filter((s): s is MockSample => Boolean(s));
    const before = targets.map((s) => ({ ...s, tags: [...s.tags] }));
    let after: MockSample[] | null = null;
    const restore = (snapshots: MockSample[]) => {
      snapshots.forEach((snap, i) => Object.assign(targets[i], snap, { tags: [...snap.tags] }));
      this.changed(ids, structural);
    };
    this.apply(
      description,
      () => {
        if (after) return restore(after);
        for (const s of targets) {
          edit(s);
          reindex(s);
        }
        after = targets.map((s) => ({ ...s, tags: [...s.tags] }));
        this.changed(ids, structural);
      },
      () => restore(before),
    );
  }

  private plural(n: number): string {
    return n === 1 ? "1 sample" : `${n} samples`;
  }

  // --- mutate ---------------------------------------------------------------

  async addTags(ids: string[], tags: string[]): Promise<void> {
    const clean = tags.map(normaliseTag).filter(Boolean);
    this.editSamples(`Tag ${this.plural(ids.length)} ${clean.join(", ")}`, ids, true, (s) => {
      for (const t of clean) if (!s.tags.includes(t)) s.tags.push(t);
    });
  }

  async removeTags(ids: string[], tags: string[]): Promise<void> {
    const clean = tags.map(normaliseTag);
    this.editSamples(`Untag ${this.plural(ids.length)} ${tags.join(", ")}`, ids, true, (s) => {
      s.tags = s.tags.filter((t) => !clean.includes(t));
    });
  }

  private taggedWith(tag: string): string[] {
    return this.lib.samples.filter((s) => s.tags.some((t) => t === tag || t.startsWith(tag + "/"))).map((s) => s.id);
  }

  async renameTag(oldTag: string, newTag: string): Promise<void> {
    const to = normaliseTag(newTag);
    if (!to) throw new Error("A tag needs a name");
    this.editSamples(`Rename tag ${oldTag} to ${to}`, this.taggedWith(oldTag), true, (s) => {
      const renamed = s.tags.map((t) => (t === oldTag ? to : t.startsWith(oldTag + "/") ? to + t.slice(oldTag.length) : t));
      s.tags = [...new Set(renamed)];
    });
  }

  async deleteTag(tag: string): Promise<void> {
    this.editSamples(`Delete tag ${tag}`, this.taggedWith(tag), true, (s) => {
      s.tags = s.tags.filter((t) => t !== tag && !t.startsWith(tag + "/"));
    });
  }

  async setTagColor(tag: string, color: string): Promise<void> {
    const previous = this.lib.tagColors.get(tag) ?? "";
    const set = (value: string) => {
      if (value) this.lib.tagColors.set(tag, value);
      else this.lib.tagColors.delete(tag);
      this.changed([], true);
    };
    this.apply(`Colour tag ${tag}`, () => set(color), () => set(previous));
  }

  async setFavorite(ids: string[], fav: boolean): Promise<void> {
    this.editSamples(`${fav ? "Favourite" : "Unfavourite"} ${this.plural(ids.length)}`, ids, true, (s) => {
      s.fav = fav;
    });
  }

  async setMeta(ids: string[], patch: MetaPatch): Promise<void> {
    if (patch.key != null && patch.key !== "" && !normaliseKey(patch.key)) {
      throw new Error(`"${patch.key}" is not a key`);
    }
    this.editSamples(`Edit ${this.plural(ids.length)}`, ids, false, (s) => {
      if (patch.bpm != null) {
        s.bpm = Math.max(0, patch.bpm);
        s.bpmSrc = s.bpm ? "user" : "";
      }
      if (patch.key != null) {
        s.key = normaliseKey(patch.key);
        s.keySrc = s.key ? "user" : "";
        s.keyRoot = false;
      }
      if (patch.type != null) s.type = patch.type;
      if (patch.note != null) s.note = patch.note;
    });
  }

  async renameSample(id: string, newName: string): Promise<void> {
    const name = newName.trim().replace(/[<>:"/\\|?*]/g, "_");
    if (!name) throw new Error("A sample needs a name");
    this.editSamples(`Rename to ${name}`, [id], false, (s) => {
      s.relPath = s.relPath.replace(/[^/]+$/, `${name}.${s.ext}`);
      s.name = name;
    });
  }

  async removeSamples(ids: string[]): Promise<void> {
    const removing = new Set(ids);
    const before = this.lib.samples;
    const after = before.filter((s) => !removing.has(s.id));
    const set = (samples: MockSample[]) => {
      this.lib.samples = samples;
      this.changed(null, true);
    };
    this.apply(`Remove ${this.plural(before.length - after.length)}`, () => set(after), () => set(before));
  }

  private collection(id: string): MockCollection {
    const c = this.lib.collections.find((x) => x.id === id);
    if (!c) throw new Error("That collection no longer exists");
    return c;
  }

  async createCollection(name: string): Promise<CollectionInfo> {
    const clean = name.trim();
    if (!clean) throw new Error("A collection needs a name");
    const created: MockCollection = { id: `col_${this.rng.hex(26)}`, name: clean, items: new Set() };
    this.apply(
      `Create collection ${clean}`,
      () => {
        this.lib.collections.push(created);
        this.changed([], true);
      },
      () => {
        this.lib.collections = this.lib.collections.filter((c) => c !== created);
        this.changed([], true);
      },
    );
    return { id: created.id, name: clean, count: 0 };
  }

  async renameCollection(id: string, name: string): Promise<void> {
    const c = this.collection(id);
    const previous = c.name;
    const set = (value: string) => {
      c.name = value;
      this.changed([], true);
    };
    this.apply(`Rename collection to ${name}`, () => set(name.trim() || previous), () => set(previous));
  }

  async deleteCollection(id: string): Promise<void> {
    const c = this.collection(id);
    const index = this.lib.collections.indexOf(c);
    this.apply(
      `Delete collection ${c.name}`,
      () => {
        this.lib.collections.splice(index, 1);
        this.changed(null, true);
      },
      () => {
        this.lib.collections.splice(index, 0, c);
        this.changed(null, true);
      },
    );
  }

  async addToCollection(id: string, sampleIds: string[]): Promise<void> {
    const c = this.collection(id);
    const added = sampleIds.filter((s) => !c.items.has(s));
    this.apply(
      `Add ${this.plural(added.length)} to ${c.name}`,
      () => {
        for (const s of added) c.items.add(s);
        this.changed(added, true);
      },
      () => {
        for (const s of added) c.items.delete(s);
        this.changed(added, true);
      },
    );
  }

  async removeFromCollection(id: string, sampleIds: string[]): Promise<void> {
    const c = this.collection(id);
    const removed = sampleIds.filter((s) => c.items.has(s));
    this.apply(
      `Remove ${this.plural(removed.length)} from ${c.name}`,
      () => {
        for (const s of removed) c.items.delete(s);
        this.changed(null, true);
      },
      () => {
        for (const s of removed) c.items.add(s);
        this.changed(null, true);
      },
    );
  }

  async renamePack(id: string, name: string): Promise<void> {
    const pack = this.lib.packs.find((p) => p.id === id);
    if (!pack) throw new Error("That pack no longer exists");
    const previous = pack.name;
    const set = (value: string) => {
      pack.name = value;
      for (const s of this.lib.samples) {
        if (s.packId !== id) continue;
        s.packName = value;
        reindex(s);
      }
      this.changed(null, true);
    };
    this.apply(`Rename pack to ${name}`, () => set(name.trim() || previous), () => set(previous));
  }

  async saveView(name: string, query: string): Promise<SavedView> {
    const view: SavedView = { id: `view_${this.rng.hex(8)}`, name: name.trim() || query, query };
    this.apply(
      `Save view ${view.name}`,
      () => {
        this.lib.views.push(view);
        this.changed([], true);
      },
      () => {
        this.lib.views = this.lib.views.filter((v) => v !== view);
        this.changed([], true);
      },
    );
    return { ...view };
  }

  async deleteView(id: string): Promise<void> {
    const index = this.lib.views.findIndex((v) => v.id === id);
    if (index < 0) return;
    const view = this.lib.views[index];
    this.apply(
      `Delete view ${view.name}`,
      () => {
        this.lib.views.splice(index, 1);
        this.changed([], true);
      },
      () => {
        this.lib.views.splice(index, 0, view);
        this.changed([], true);
      },
    );
  }

  async undo(): Promise<string> {
    const entry = this.undoStack.pop();
    if (!entry) return "";
    entry.undo();
    this.redoStack.push(entry);
    return entry.description;
  }

  async redo(): Promise<string> {
    const entry = this.redoStack.pop();
    if (!entry) return "";
    entry.redo();
    this.undoStack.push(entry);
    return entry.description;
  }

  async reanalyze(ids: string[]): Promise<void> {
    this.assertWritable();
    void this.analyse(ids);
  }

  // --- ingest ---------------------------------------------------------------

  /** Emits progress for `total` items over roughly `ms`, calling `step` per tick. */
  private async runProgress(
    kind: Progress["kind"],
    event: "ingest:progress" | "analysis:progress",
    total: number,
    ms: number,
    step: (done: number, progress: Progress) => void,
    jobId = `job_${this.rng.hex(8)}`,
  ): Promise<Progress> {
    const job: Job = { id: jobId, cancelled: false };
    this.jobs.set(jobId, job);
    const progress: Progress = {
      jobId, kind, done: 0, total, added: 0, duplicates: 0, failed: 0,
      current: "", finished: false, cancelled: false, errors: [],
    };
    const ticks = Math.max(1, Math.round(ms / 80));
    for (let tick = 1; tick <= ticks && !job.cancelled; tick++) {
      await sleep(80);
      progress.done = Math.round((tick / ticks) * total);
      step(progress.done, progress);
      this.emit(event, { ...progress, errors: [...progress.errors] });
    }
    progress.finished = true;
    progress.cancelled = job.cancelled;
    this.jobs.delete(jobId);
    this.emit(event, { ...progress, errors: [...progress.errors] });
    return progress;
  }

  private async analyse(ids: string[]): Promise<void> {
    let reported = 0;
    await this.runProgress("analysis", "analysis:progress", ids.length, Math.min(4000, 300 + ids.length * 15), (done, p) => {
      const batch = ids.slice(reported, done);
      reported = done;
      for (const id of batch) {
        const s = this.byId.get(id);
        if (!s) continue;
        Object.assign(s, this.pendingAnalysis.get(id), { analyzed: true });
        this.pendingAnalysis.delete(id);
        p.current = s.name;
      }
      if (batch.length) this.changed(batch, false);
    });
  }

  private packFor(name: string, source: string): MockPack {
    let pack = this.lib.packs.find((p) => p.name === name);
    if (!pack) {
      pack = { id: `pk_${this.rng.hex(26)}`, name, dir: source === "splice" ? `Splice/${name}` : name, source };
      this.lib.packs.push(pack);
    }
    return pack;
  }

  /** Fabricates the samples an import of `count` files into `packName` would add. */
  private fabricate(packName: string, source: string, count: number, target: Partial<ImportTarget>): MockSample[] {
    const pack = this.lib.packs.find((p) => p.id === target.packId) ?? this.packFor(packName, source);
    const out: MockSample[] = [];
    for (let i = 0; i < count; i++) {
      const s = makeSample(this.rng, pack, genreIndex(pack.name), i, Date.now() + i);
      // Imported files show up first and are analysed afterwards.
      const { bpm, bpmSrc, key, keySrc, keyRoot, type } = s;
      this.pendingAnalysis.set(s.id, { bpm, bpmSrc, key, keySrc, keyRoot, type });
      Object.assign(s, { analyzed: false, bpm: 0, bpmSrc: "", key: "", keySrc: "", keyRoot: false, type: "", fav: false, missing: false });
      if (target.tag && !s.tags.includes(target.tag)) s.tags.push(target.tag);
      reindex(s);
      out.push(s);
    }
    return out;
  }

  private startImport(
    kind: "import" | "splice",
    batches: { pack: string; count: number }[],
    target: Partial<ImportTarget>,
  ): string {
    this.assertWritable();
    const jobId = `job_${this.rng.hex(8)}`;
    const incoming = batches.flatMap((b) => this.fabricate(b.pack, kind, b.count, target));
    const collection = target.collectionId ? this.lib.collections.find((c) => c.id === target.collectionId) : undefined;
    let landed = 0;

    void this.runProgress(
      kind,
      "ingest:progress",
      incoming.length,
      Math.min(6000, 600 + incoming.length * 25),
      (done, p) => {
        if (landed === 0 && done > 0) this.lib.recent = new Set();
        const batch = incoming.slice(landed, done);
        landed = done;
        for (const s of batch) {
          this.lib.samples.push(s);
          this.byId.set(s.id, s);
          this.lib.recent.add(s.id);
          collection?.items.add(s.id);
          p.current = s.relPath;
        }
        p.added = landed;
        // Pretend a couple of files were already in the library.
        p.duplicates = Math.floor(landed / 40);
        if (batch.length) this.changed(null, true);
      },
      jobId,
    ).then((p) => {
      if (!p.cancelled) void this.analyse(incoming.slice(0, landed).map((s) => s.id));
    });
    return jobId;
  }

  async import(paths: string[], target: Partial<ImportTarget> = {}): Promise<string> {
    if (paths.length === 0) throw new Error("Nothing to import");
    const loose = paths.filter((p) => AUDIO_EXT.test(p)).length;
    const batches = paths
      .filter((p) => !AUDIO_EXT.test(p))
      .map((folder) => ({ pack: baseName(folder), count: this.rng.int(18, 60) }));
    if (loose > 0) batches.push({ pack: "Unsorted", count: loose });
    return this.startImport("import", batches, target);
  }

  async cancelJob(jobId: string): Promise<void> {
    const job = this.jobs.get(jobId);
    if (job) job.cancelled = true;
  }

  async detectSplice(): Promise<SpliceInfo> {
    const imported = new Set(this.lib.packs.map((p) => p.name));
    return {
      found: true,
      folder: "/home/you/Splice",
      hasDb: true,
      packs: SPLICE_PACKS.map((p) => ({ ...p, imported: imported.has(p.name) })),
    };
  }

  async importSplice(_folder: string, packNames: string[]): Promise<string> {
    const batches = SPLICE_PACKS.filter((p) => packNames.includes(p.name)).map((p) => ({
      pack: p.name,
      // Keep the fake import short; real packs can be hundreds of files.
      count: Math.min(p.count, 80),
    }));
    if (batches.length === 0) throw new Error("Pick at least one pack");
    return this.startImport("splice", batches, {});
  }

  /** Simulates an OS file drop on a `data-drop-kind` target. */
  async drop(paths: string[], kind: "pack" | "tag" | "collection" | "list" = "list", id = ""): Promise<void> {
    const target: ImportTarget = {
      packId: kind === "pack" ? id : "",
      tag: kind === "tag" ? id : "",
      collectionId: kind === "collection" ? id : "",
    };
    const jobId = await this.import(paths, target);
    this.emit("drop", { kind: "import", jobId, count: paths.length, target });
  }

  // --- OS integration -------------------------------------------------------

  async startDrag(_ids: string[]): Promise<DragResult> {
    return { started: false, dropped: false, error: "Dragging files out needs the desktop app" };
  }

  async reveal(id: string): Promise<void> {
    const [detail] = await this.getSamples([id]);
    if (!detail) throw new Error("That sample no longer exists");
    this.emit("toast", { level: "info", message: `Would reveal ${detail.absPath}` });
  }

  async copyPaths(ids: string[]): Promise<void> {
    const text = (await this.getSamples(ids)).map((d) => d.absPath).join("\n");
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // Clipboard access needs focus and a secure context; not fatal in a mock.
    }
  }

  audioUrl = audioUrl;
  peaksUrl = peaksUrl;

  // --- audio and peaks, served through the fetch shim ------------------------

  private renderedFor(id: string): Rendered | null {
    const s = this.byId.get(id);
    if (!s || s.missing || this.status === "unavailable") return null;
    let r = this.rendered.get(id);
    if (!r) {
      r = render(s);
      if (this.rendered.size >= 6) this.rendered.delete(this.rendered.keys().next().value!);
      this.rendered.set(id, r);
    }
    return r;
  }

  /** Answers /audio/{id}.f32 and /peaks/{id}; null for any other path. */
  respond(pathname: string): Response | null {
    const audio = /^\/audio\/([^/]+)\.f32$/.exec(pathname);
    const peaks = /^\/peaks\/([^/]+)$/.exec(pathname);
    if (!audio && !peaks) return null;

    const r = this.renderedFor(decodeURIComponent((audio ?? peaks)![1]));
    if (!r) {
      return new Response(JSON.stringify({ error: "sample not found or not decodable" }), {
        status: 404,
        headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
      });
    }
    if (peaks) {
      return new Response(fullPeaks(r), { headers: { "Content-Type": "application/octet-stream" } });
    }
    return new Response(encodeF32(r), {
      headers: {
        "Content-Type": "application/octet-stream",
        "Cache-Control": "no-store",
        "X-Frames": String(r.frames),
        "X-Channels": String(r.channels.length),
        "X-Rate": String(r.rate),
      },
    });
  }
}

/** Routes fetches of the audio and peaks URLs to the mock; everything else passes through. */
function installFetchShim(backend: MockBackend): void {
  const realFetch = window.fetch.bind(window);
  window.fetch = async (input, init) => {
    const url = typeof input === "string" || input instanceof URL ? String(input) : input.url;
    const { pathname, origin } = new URL(url, location.href);
    if (origin === location.origin) {
      const response = backend.respond(pathname);
      if (response) return response;
    }
    return realFetch(input, init);
  };
}

export function createMockApi(): Api {
  const params = new URLSearchParams(location.search);
  const state = params.get("state");
  const empty = state === "empty";
  const status: LibraryStatus =
    state === "none" || state === "unavailable" || state === "readonly" ? state : "ok";
  const requested = Number(params.get("n"));
  const backend = new MockBackend({
    status,
    samples: empty ? 0 : requested > 0 ? Math.min(requested, 200_000) : undefined,
  });
  installFetchShim(backend);
  (window as unknown as { __ssssMock: MockBackend }).__ssssMock = backend;
  return backend;
}
