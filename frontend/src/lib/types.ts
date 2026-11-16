// TypeScript mirrors of the Go types in internal/model. The JSON names are
// the contract; these are stricter than the generated bindings (string unions,
// arrays never null) and lib/api.wails.ts normalises backend values to match.

export type SampleType = "loop" | "oneshot" | "";
export type MetaSource = "user" | "splice" | "filename" | "detected" | "";
export type SortKey = "added" | "name" | "bpm" | "key" | "dur" | "pack" | "relevance";
export type LibraryStatus = "none" | "ok" | "unavailable" | "readonly";
export type Theme = "system" | "light" | "dark";
export type Density = "compact" | "regular";
export type Platform = "darwin" | "windows" | "linux";
export type JobKind = "import" | "splice" | "analysis" | "rescan";

export interface Sample {
  id: string;
  packId: string;
  packName: string;
  /** File name without extension. */
  name: string;
  ext: string;
  /** Path inside the pack dir, slash-separated. */
  relPath: string;
  size: number;
  /** Seconds. */
  dur: number;
  sr: number;
  ch: number;
  type: SampleType;
  /** 0 = unknown. */
  bpm: number;
  bpmSrc: MetaSource;
  /** "Am", "F#"; "" = unknown. */
  key: string;
  keySrc: MetaSource;
  /**
   * The key is a detected root note with no mode: it is written like a major
   * key ("A") but fits A minor as well as A major.
   */
  keyRoot: boolean;
  tags: string[];
  fav: boolean;
  /** Unix ms. */
  added: number;
  missing: boolean;
  analyzed: boolean;
  /** base64 of 96 uint8 peak buckets; "" until analysed. */
  mini: string;
}

export interface SampleDetail extends Sample {
  absPath: string;
  /** Collection ids. */
  collections: string[];
  note: string;
  /** From filename/folders, not yet applied. */
  suggestedTags: string[];
}

export interface SearchRequest {
  query: string;
  sort: SortKey;
  desc: boolean;
  offset: number;
  limit: number;
}

export interface SearchResult {
  total: number;
  rows: Sample[];
  /** Query parse problem, human readable; rows are still best-effort. */
  error: string;
}

export interface TagCount {
  tag: string;
  /** Includes descendants. */
  count: number;
  color: string;
}

export interface PackInfo {
  id: string;
  name: string;
  dir: string;
  source: string;
  count: number;
}

export interface CollectionInfo {
  id: string;
  name: string;
  count: number;
}

export interface SavedView {
  id: string;
  name: string;
  query: string;
}

export interface Sidebar {
  all: number;
  favorites: number;
  untagged: number;
  /** Size of the last import batch. */
  recent: number;
  tags: TagCount[];
  packs: PackInfo[];
  collections: CollectionInfo[];
  views: SavedView[];
}

export interface Settings {
  theme: Theme;
  density: Density;
  playOnSelect: boolean;
  /** 0..1 */
  volume: number;
  projectBpm: number;
  /** "" = none. */
  projectKey: string;
  matchTempo: boolean;
  matchKey: boolean;
  sidebarWidth: number;
  sidebarOpen: boolean;
  /** Slots 1..9. */
  pinnedTags: string[];
  spliceFolder: string;
}

export interface AppState {
  status: LibraryStatus;
  libraryPath: string;
  libraryName: string;
  /** Why the library is unavailable or read-only. */
  message: string;
  platform: Platform;
  defaultLibraryPath: string;
  settings: Settings;
}

/** Every field is optional; "" means unset. */
export interface ImportTarget {
  packId: string;
  tag: string;
  collectionId: string;
}

export interface Progress {
  jobId: string;
  kind: JobKind;
  done: number;
  total: number;
  added: number;
  duplicates: number;
  failed: number;
  current: string;
  finished: boolean;
  cancelled: boolean;
  /** Capped at 50, "file: reason". */
  errors: string[];
}

/** Omitted or null fields are left untouched. */
export interface MetaPatch {
  bpm?: number | null;
  key?: string | null;
  type?: SampleType | null;
  note?: string | null;
}

export interface SplicePack {
  name: string;
  count: number;
  imported: boolean;
  genre: string;
}

export interface SpliceInfo {
  found: boolean;
  folder: string;
  hasDb: boolean;
  packs: SplicePack[];
}

export interface DragResult {
  started: boolean;
  dropped: boolean;
  error: string;
}

// Event payloads (Go -> frontend).

export interface SamplesChanged {
  /** null => refetch everything. */
  ids: string[] | null;
  /** Sidebar counts changed. */
  structural: boolean;
}

export interface DropEvent {
  kind: "import" | "assign";
  jobId?: string;
  count: number;
  target: ImportTarget;
}

export interface ToastEvent {
  level: "info" | "error";
  message: string;
}

export interface ApiEvents {
  state: AppState;
  "samples:changed": SamplesChanged;
  "ingest:progress": Progress;
  "analysis:progress": Progress;
  drop: DropEvent;
  toast: ToastEvent;
}

export type ApiEventName = keyof ApiEvents;

/** Wire format constants shared with internal/audio. */
export const MINI_PEAKS = 96;
export const FULL_PEAK_BUCKETS = 2000;
