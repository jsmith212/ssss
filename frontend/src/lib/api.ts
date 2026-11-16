// The one place the frontend talks to the backend. `api` is backed by the
// generated Wails bindings; in development, a plain browser (npm run dev) or
// `?mock` gets the in-memory mock instead, so the whole UI runs without Go.

import type {
  ApiEventName,
  ApiEvents,
  AppState,
  CollectionInfo,
  DragResult,
  ImportTarget,
  MetaPatch,
  SampleDetail,
  SavedView,
  SearchRequest,
  SearchResult,
  Settings,
  Sidebar,
  SortKey,
  SpliceInfo,
} from "./types";

export { audioUrl, peaksUrl } from "./urls";

export interface Api {
  /** True when backed by the in-browser mock. */
  readonly isMock: boolean;

  // lifecycle
  getState(): Promise<AppState>;
  createLibrary(path: string, name: string): Promise<AppState>;
  openLibrary(path: string): Promise<AppState>;
  /** Native folder dialog; "" if cancelled. */
  chooseFolder(title: string): Promise<string>;
  /** Native multi-file dialog with an audio filter; [] if cancelled. */
  chooseFiles(title: string): Promise<string[]>;
  retryLibrary(): Promise<AppState>;
  /** Asynchronous; progress arrives as events. */
  rescan(): Promise<void>;
  rebuildCache(): Promise<void>;
  updateSettings(settings: Settings): Promise<Settings>;

  // read
  search(req: SearchRequest): Promise<SearchResult>;
  /** Ids of every match in sort order, for select-all and shift-ranges. */
  searchIds(query: string, sort: SortKey, desc: boolean): Promise<string[]>;
  getSamples(ids: string[]): Promise<SampleDetail[]>;
  getSidebar(): Promise<Sidebar>;

  // mutate: each pushes an undo entry and emits "samples:changed"
  addTags(ids: string[], tags: string[]): Promise<void>;
  removeTags(ids: string[], tags: string[]): Promise<void>;
  renameTag(oldTag: string, newTag: string): Promise<void>;
  deleteTag(tag: string): Promise<void>;
  setTagColor(tag: string, color: string): Promise<void>;
  setFavorite(ids: string[], fav: boolean): Promise<void>;
  setMeta(ids: string[], patch: MetaPatch): Promise<void>;
  renameSample(id: string, newName: string): Promise<void>;
  removeSamples(ids: string[]): Promise<void>;
  createCollection(name: string): Promise<CollectionInfo>;
  renameCollection(id: string, name: string): Promise<void>;
  deleteCollection(id: string): Promise<void>;
  addToCollection(id: string, sampleIds: string[]): Promise<void>;
  removeFromCollection(id: string, sampleIds: string[]): Promise<void>;
  renamePack(id: string, name: string): Promise<void>;
  saveView(name: string, query: string): Promise<SavedView>;
  deleteView(id: string): Promise<void>;
  /** Description of what was undone; "" if there was nothing. */
  undo(): Promise<string>;
  redo(): Promise<string>;
  reanalyze(ids: string[]): Promise<void>;

  // ingest
  /** Returns the job id. */
  import(paths: string[], target?: Partial<ImportTarget>): Promise<string>;
  cancelJob(jobId: string): Promise<void>;
  detectSplice(): Promise<SpliceInfo>;
  importSplice(folder: string, packNames: string[]): Promise<string>;

  // OS integration
  /** Resolves when the native drag ends. */
  startDrag(ids: string[]): Promise<DragResult>;
  reveal(id: string): Promise<void>;
  copyPaths(ids: string[]): Promise<void>;

  /** Subscribes to a backend event; returns the unsubscribe function. */
  on<E extends ApiEventName>(event: E, cb: (data: ApiEvents[E]) => void): () => void;

  /**
   * URL of the decoded audio: planar little-endian float32 (all of channel
   * 0, then channel 1), with X-Frames, X-Channels, X-Rate and optional
   * X-Loop-Start / X-Loop-End headers. Fetch it with `fetch`.
   */
  audioUrl(id: string): string;
  /** URL of the full-resolution peaks: 2000 x (int8 min, int8 max). */
  peaksUrl(id: string): string;
}

/**
 * Mirrors how @wailsio/runtime finds its native bridge. `window._wails`
 * cannot be used: the runtime creates it itself, in a browser too.
 */
export function insideWails(): boolean {
  const w = window as unknown as {
    chrome?: { webview?: { postMessage?: unknown } };
    webkit?: { messageHandlers?: Record<string, { postMessage?: unknown } | undefined> };
    wails?: { invoke?: unknown };
  };
  return Boolean(
    w.chrome?.webview?.postMessage ||
      w.webkit?.messageHandlers?.["external"]?.postMessage ||
      w.wails?.invoke,
  );
}

async function load(): Promise<Api> {
  // The mock is a development tool. `import.meta.env.DEV` is a constant, so
  // a production build drops this branch and never ships the fake library.
  if (import.meta.env.DEV) {
    const forceMock = new URLSearchParams(location.search).has("mock");
    if (forceMock || !insideWails()) return (await import("./api.mock")).createMockApi();
  }
  return (await import("./api.wails")).createWailsApi();
}

/** Resolved before anything renders: main.ts imports this module first. */
export const api: Api = await load();
