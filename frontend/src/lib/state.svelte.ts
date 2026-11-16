// Global UI state (Svelte 5 runes). Components read these objects directly
// and change them through the functions below, so every rule about how state
// moves lives in one file.
//
// The shapes and function names are the contract between the shell (list,
// sidebar, omnibar), the player and the overlays.

import { SvelteSet } from "svelte/reactivity";
import { api } from "./api";
import { setMac } from "./commands";
import { hasFreeText } from "./query";
import type { AppState, Progress, Sample, Settings, Sidebar, SortKey } from "./types";

// --- app --------------------------------------------------------------------

export const app = $state({
  /** null until the first GetState answers. */
  state: null as AppState | null,
  /** Narrow window: the sidebar auto-hides below 900px. */
  narrow: false,
});

/** Current settings, with safe defaults before the state has loaded. */
export function settings(): Settings {
  return app.state?.settings ?? FALLBACK_SETTINGS;
}

const FALLBACK_SETTINGS: Settings = {
  theme: "system",
  density: "regular",
  playOnSelect: true,
  volume: 0.8,
  projectBpm: 120,
  projectKey: "",
  matchTempo: false,
  matchKey: false,
  sidebarWidth: 220,
  sidebarOpen: true,
  pinnedTags: [],
  spliceFolder: "",
};

/** True when the library can be changed: samples edited, imported and removed. */
export function libraryUsable(): boolean {
  return app.state?.status === "ok";
}

/**
 * True when the library's files can be read: played, revealed and dragged
 * out. That includes a read-only library, but not one whose folder is gone.
 */
export function libraryReadable(): boolean {
  const status = app.state?.status;
  return status === "ok" || status === "readonly";
}

/** The sidebar is on screen: the user has it open and the window is wide enough. */
export function sidebarVisible(): boolean {
  return settings().sidebarOpen && !app.narrow;
}

/**
 * Applies a settings change optimistically and persists it. Rapid changes
 * (dragging the volume, resizing the sidebar) are fine: the last write wins.
 */
export async function updateSettings(patch: Partial<Settings>): Promise<void> {
  if (!app.state) return;
  const next = { ...app.state.settings, ...patch };
  app.state.settings = next;
  applyAppearance(next);
  try {
    await api.updateSettings(next);
  } catch (err) {
    pushToast(errorMessage(err), { level: "error" });
  }
}

/** Reflects theme and density on <html> for app.css. */
export function applyAppearance(s: Settings): void {
  const root = document.documentElement;
  if (s.theme === "system") delete root.dataset.theme;
  else root.dataset.theme = s.theme;
  root.dataset.density = s.density;
}

function setAppState(state: AppState): void {
  const previous = app.state;
  app.state = state;
  // A different library, or the same one coming back: nothing cached about
  // the old rows can be trusted.
  if (previous && (previous.libraryPath !== state.libraryPath || previous.status !== state.status)) {
    view.revision++;
    scheduleSidebarRefresh();
  }
  setMac(state.platform === "darwin");
  document.documentElement.dataset.platform = state.platform;
  applyAppearance(state.settings);
}

/** Changes a setting on screen only (live sidebar resize); persist with updateSettings. */
export function previewSettings(patch: Partial<Settings>): void {
  if (app.state) app.state.settings = { ...app.state.settings, ...patch };
}

/** Banner "Retry": asks the backend to look for the library folder again. */
export async function retryLibrary(): Promise<void> {
  setAppState(await api.retryLibrary());
}

/** Banner "Locate…": lets the user point at the library folder's new place. */
export async function locateLibrary(): Promise<void> {
  const path = await api.chooseFolder("Locate library folder");
  if (path === "") return;
  setAppState(await api.openLibrary(path));
}

// --- query, sort and data freshness -------------------------------------------

export const view = $state({
  /** The omnibar string; the single source of truth for filtering. */
  query: "",
  sort: "added" as SortKey,
  desc: true,
  /** The user picked a sort, so free text no longer switches to relevance. */
  sortExplicit: false,
  /** Matches of the current query; written by the list after each search. */
  total: 0,
  /** Parse problem reported by the last search, "" if none. */
  error: "",
  facetsOpen: false,
  /**
   * Bumped whenever rows may have changed on the backend. The list keys its
   * page cache on (query, sort, desc, revision).
   */
  revision: 0,
});

/** Replaces the query (sidebar click, clear, saved view). */
export function setQuery(query: string): void {
  if (query === view.query) return;
  view.query = query;
  if (!view.sortExplicit) {
    const relevance = hasFreeText(query);
    view.sort = relevance ? "relevance" : "added";
    view.desc = true;
  }
  clearSelection();
}

export function setSort(sort: SortKey, desc: boolean): void {
  view.sort = sort;
  view.desc = desc;
  view.sortExplicit = true;
}

// --- selection and focus ------------------------------------------------------

class Selection {
  /** Selected sample ids. Distinct from the focus row (file-manager semantics). */
  readonly ids = new SvelteSet<string>();
  /** Row index of the keyboard focus in the current result list; -1 = none. */
  focusIndex = $state(-1);
  /** Where a Shift-range starts. */
  anchorIndex = $state(-1);
  /**
   * The focused row's sample; the player follows this. Raw, so it is the
   * very object the list handed over and can be compared by identity.
   */
  focusSample = $state.raw<Sample | null>(null);
}

export const selection = new Selection();

/** Ids the current action applies to. */
export function selectedIds(): string[] {
  return [...selection.ids];
}

/** Plain click or arrow key: the row becomes both the selection and the focus. */
export function selectOnly(sample: Sample, index: number): void {
  selection.ids.clear();
  selection.ids.add(sample.id);
  selection.anchorIndex = index;
  setFocus(sample, index);
}

/** Cmd/Ctrl-click: toggles the row and moves the focus to it. */
export function toggleSelect(sample: Sample, index: number): void {
  if (selection.ids.has(sample.id)) selection.ids.delete(sample.id);
  else selection.ids.add(sample.id);
  selection.anchorIndex = index;
  setFocus(sample, index);
}

/** Replaces the selection wholesale (Shift-range, Cmd+A via SearchIDs). */
export function setSelection(ids: Iterable<string>): void {
  selection.ids.clear();
  for (const id of ids) selection.ids.add(id);
}

export function setFocus(sample: Sample | null, index: number): void {
  selection.focusSample = sample;
  selection.focusIndex = index;
}

export function clearSelection(): void {
  selection.ids.clear();
  selection.focusIndex = -1;
  selection.anchorIndex = -1;
  selection.focusSample = null;
}

// --- playback (written by the player, read by rows) ----------------------------

export const playback = $state({
  /** Id of the sample that is sounding, or null. */
  playingId: null as string | null,
  /** Tempo/key matched preview is active for the playing sample. */
  matched: false,
  /** Session override of the per-type loop default; null = use the default. */
  loopOverride: null as boolean | null,
});

// --- audition hook (the list asks, the audio engine answers) --------------------

export type AuditionHandler = (sample: Sample) => void;

let auditionHandler: AuditionHandler | null = null;

/**
 * The player registers its engine here, once. The handler starts (or
 * restarts) playback of the sample from the top. Returns the unregister
 * function.
 */
export function setAuditionHandler(handler: AuditionHandler): () => void {
  auditionHandler = handler;
  return () => {
    if (auditionHandler === handler) auditionHandler = null;
  };
}

/**
 * Plays a sample now. Call sites: the list on click / arrow keys (only when
 * `settings().playOnSelect`), and anything that wants an explicit preview.
 * Does nothing while the library is offline or the file is missing.
 */
export function audition(sample: Sample): void {
  if (!libraryReadable() || sample.missing) return;
  auditionHandler?.(sample);
}

// --- sidebar ------------------------------------------------------------------

export const sidebar = $state({
  data: null as Sidebar | null,
});

export async function refreshSidebar(): Promise<void> {
  sidebar.data = await api.getSidebar();
}

let sidebarTimer: ReturnType<typeof setTimeout> | undefined;

/** Refreshes the sidebar soon; a burst of change events costs one fetch. */
function scheduleSidebarRefresh(): void {
  clearTimeout(sidebarTimer);
  sidebarTimer = setTimeout(() => {
    refreshSidebar().catch((err) => pushToast(errorMessage(err), { level: "error", key: "sidebar" }));
  }, 80);
}

// --- jobs ---------------------------------------------------------------------

export const jobs = $state({
  /** Running and just-finished import/analysis/rescan jobs, oldest first. */
  list: [] as Progress[],
});

function trackJob(progress: Progress): void {
  const i = jobs.list.findIndex((j) => j.jobId === progress.jobId);
  if (i >= 0) jobs.list[i] = progress;
  else jobs.list.push(progress);
  if (progress.finished) {
    // Leave the result visible for a moment before the pill goes away.
    setTimeout(() => dismissJob(progress.jobId), 4000);
  }
}

export function dismissJob(jobId: string): void {
  jobs.list = jobs.list.filter((j) => j.jobId !== jobId);
}

// --- toasts -------------------------------------------------------------------

export interface ToastAction {
  label: string;
  run: () => void | Promise<void>;
}

export interface Toast {
  id: number;
  level: "info" | "error";
  message: string;
  action?: ToastAction;
  key?: string;
}

export interface ToastOptions {
  level?: "info" | "error";
  action?: ToastAction;
  /** Milliseconds until the toast leaves; 0 keeps it until dismissed. */
  timeout?: number;
  /** A new toast replaces the one showing with the same key. */
  key?: string;
}

export const toasts = $state({
  list: [] as Toast[],
});

let nextToastId = 1;

/** Toasts on screen at once; older ones make room. */
const MAX_TOASTS = 3;

/**
 * Shows a toast; returns its id. Errors stay until dismissed (or pass a
 * timeout). A toast saying the same thing as one already showing replaces
 * it, so a failure that repeats does not pile up.
 */
export function pushToast(message: string, options: ToastOptions = {}): number {
  const id = nextToastId++;
  const level = options.level ?? "info";
  const kept = toasts.list.filter(
    (t) => !(options.key !== undefined && t.key === options.key) && !(t.level === level && t.message === message),
  );
  toasts.list = [...kept, { id, level, message, action: options.action, key: options.key }].slice(-MAX_TOASTS);
  const timeout = options.timeout ?? (level === "error" ? 0 : 4000);
  if (timeout > 0) setTimeout(() => dismissToast(id), timeout);
  return id;
}

export function dismissToast(id: number): void {
  toasts.list = toasts.list.filter((t) => t.id !== id);
}

/** Dismisses the newest error toast (they do not leave by themselves); false when there is none. */
function dismissNewestError(): boolean {
  const newest = toasts.list.findLast((t) => t.level === "error");
  if (!newest) return false;
  dismissToast(newest.id);
  return true;
}

/** Announces an undoable action, e.g. `announceUndoable("Removed 3 samples")`. */
export function announceUndoable(message: string): void {
  pushToast(message, { action: { label: "Undo", run: undo }, key: EDIT_TOAST });
}

/** Edits, undo and redo share one toast slot: only the latest is news. */
const EDIT_TOAST = "edit";

export async function undo(): Promise<void> {
  const what = await api.undo();
  pushToast(what ? `Undid: ${what}` : "Nothing to undo", { key: EDIT_TOAST });
}

export async function redo(): Promise<void> {
  const what = await api.redo();
  pushToast(what ? `Redid: ${what}` : "Nothing to redo", { key: EDIT_TOAST });
}

export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// --- overlays (popovers, sheets, palette) ----------------------------------------

export type OverlayId =
  | "tags"
  | "collections"
  | "palette"
  | "info"
  | "settings"
  | "shortcuts"
  | "splice"
  | "context"
  | "rename"
  | "project";

export interface Overlay {
  id: OverlayId;
  /** Where to place a popover; overlays centre themselves without it. */
  anchor?: { x: number; y: number };
  /** Overlay-specific input, e.g. context menu items. */
  props?: Record<string, unknown>;
}

export const overlays = $state({
  /** Open overlays, topmost last. Escape closes from the end. */
  stack: [] as Overlay[],
});

/** Opens an overlay, or brings it to the top if it is already open. */
export function openOverlay(id: OverlayId, options: Omit<Overlay, "id"> = {}): void {
  overlays.stack = [...overlays.stack.filter((o) => o.id !== id), { id, ...options }];
}

export function closeOverlay(id: OverlayId): void {
  overlays.stack = overlays.stack.filter((o) => o.id !== id);
}

export function toggleOverlay(id: OverlayId, options: Omit<Overlay, "id"> = {}): void {
  if (isOverlayOpen(id)) closeOverlay(id);
  else openOverlay(id, options);
}

export function isOverlayOpen(id: OverlayId): boolean {
  return overlays.stack.some((o) => o.id === id);
}

export function overlay(id: OverlayId): Overlay | undefined {
  return overlays.stack.find((o) => o.id === id);
}

/** Closes the topmost overlay; false when none was open. */
export function closeTopOverlay(): boolean {
  if (overlays.stack.length === 0) return false;
  overlays.stack = overlays.stack.slice(0, -1);
  return true;
}

/** Escape: close the topmost overlay, else dismiss an error toast, else clear the query, else blur. */
export function escape(): void {
  if (closeTopOverlay()) return;
  if (dismissNewestError()) return;
  if (view.query !== "") {
    setQuery("");
    return;
  }
  (document.activeElement as HTMLElement | null)?.blur?.();
}

// --- file drop feedback ---------------------------------------------------------

export const drop = $state({
  /** An OS file drag is over the window (DropOverlay). */
  active: false,
});

// --- startup ----------------------------------------------------------------

let started = false;

/**
 * Loads the initial state and subscribes to backend events. Call once, from
 * App.svelte. Returns a function that removes the subscriptions.
 */
export async function init(): Promise<() => void> {
  if (started) return () => {};
  started = true;

  const media = window.matchMedia("(max-width: 899px)");
  const onMedia = () => (app.narrow = media.matches);
  onMedia();
  media.addEventListener("change", onMedia);

  const off = [
    api.on("state", (state) => {
      setAppState(state);
    }),
    api.on("samples:changed", (change) => {
      view.revision++;
      if (change.structural) scheduleSidebarRefresh();
    }),
    api.on("ingest:progress", trackJob),
    api.on("analysis:progress", trackJob),
    api.on("toast", (t) => void pushToast(t.message, { level: t.level })),
    api.on("drop", () => {
      drop.active = false;
    }),
    () => media.removeEventListener("change", onMedia),
  ];

  setAppState(await api.getState());
  await refreshSidebar();

  return () => {
    off.forEach((fn) => fn());
    clearTimeout(sidebarTimer);
    started = false;
  };
}
