// The result list behind SampleList: which rows exist, which are loaded,
// and every way the focus and the selection move. Components and commands
// call these functions; the page cache and the id list stay private.

import { api } from "../api";
import {
  audition,
  clearSelection,
  errorMessage,
  libraryReadable,
  libraryUsable,
  pushToast,
  selectOnly,
  selection,
  setFocus,
  setSelection,
  settings,
  toggleSelect,
  view,
} from "../state.svelte";
import { formatKeys } from "../commands";
import type { Sample, SortKey } from "../types";
import { PageCache } from "./pages";
import { navTarget, rangeBounds, scrollToReveal, type NavKey } from "./selection";

export const PAGE_SIZE = 200;
export const OVERSCAN = 12;
/** Empty space above the first and below the last row. */
export const LIST_PAD = 4;
/** A query slower than this shows skeleton rows instead of the old result. */
const SLOW_QUERY_MS = 150;

export const list = $state({
  /** Bumped whenever loaded rows changed; `rowAt` depends on it. */
  version: 0,
  total: 0,
  /** The first result has arrived (keeps the empty state from flashing at startup). */
  ready: false,
  /** The current query is taking long; unloaded rows render as skeletons. */
  slow: false,
  /** Sample being renamed inline, or null. */
  renamingId: null as string | null,
  /**
   * The name as typed so far. It lives here, not in the input: the row
   * holding the input is unmounted and remounted as rows shift or reload.
   */
  renameDraft: "",
  /** A native drag-out is in flight. */
  dragging: false,
  scrollTop: 0,
  viewportHeight: 0,
});

let current: PageCache | null = null;
/** Cache of a query that has been issued but not shown yet. */
let pending: PageCache | null = null;
let activeKey = "";
let activeRevision = -1;
let slowTimer: ReturnType<typeof setTimeout> | undefined;
let allIds: Promise<string[]> | null = null;
let viewport: HTMLElement | null = null;
/** Invalidates async selection work that a newer action has superseded. */
let actionToken = 0;
/** The sample `list.renamingId` names, as it was when the rename started. */
let renameTarget: Sample | null = null;
/** A lookup of where the focused sample went is in flight. */
let locating = false;

export function rowHeight(): number {
  return settings().density === "compact" ? 28 : 32;
}

/** Gives the keyboard to the list, e.g. when an inline field in it goes away. */
export function focusList(): void {
  viewport?.focus({ preventScroll: true });
}

/** Registers the scrolling element; SampleList calls this on mount. */
export function attachViewport(el: HTMLElement): () => void {
  viewport = el;
  return () => {
    if (viewport === el) viewport = null;
  };
}

/**
 * Points the list at a query. Called from an effect on (query, sort, desc,
 * revision): a new query or sort starts a fresh cache, a new revision only
 * marks the loaded rows stale.
 */
export function sync(query: string, sort: SortKey, desc: boolean, revision: number): void {
  const key = JSON.stringify([query, sort, desc]);
  if (key === activeKey) {
    if (revision === activeRevision) return;
    activeRevision = revision;
    allIds = null;
    const cache = pending ?? current;
    cache?.invalidate();
    // Visible rows are refetched as the list renders them, but an empty
    // result renders none: ask again, or rows added since would never show.
    if (cache && !cache.total) cache.ensure(0, PAGE_SIZE - 1);
    list.version++;
    return;
  }
  activeKey = key;
  activeRevision = revision;
  allIds = null;
  actionToken++;
  abandonRename();

  const cache: PageCache = new PageCache((offset, limit) => api.search({ query, sort, desc, offset, limit }), {
    pageSize: PAGE_SIZE,
    onChange: () => onCacheChange(cache),
  });
  pending = cache;
  clearTimeout(slowTimer);
  slowTimer = setTimeout(() => {
    if (pending === cache) adopt(cache);
  }, SLOW_QUERY_MS);
  cache.ensure(0, PAGE_SIZE - 1);
}

function adopt(cache: PageCache): void {
  clearTimeout(slowTimer);
  pending = null;
  current = cache;
  // Row indexes mean something else now.
  if (selection.focusIndex >= 0 || selection.ids.size > 0) clearSelection();
  list.scrollTop = 0;
  if (viewport) viewport.scrollTop = 0;
  publish(cache);
}

function onCacheChange(cache: PageCache): void {
  if (cache === pending && cache.total !== null) adopt(cache);
  else if (cache === current) publish(cache);
}

function publish(cache: PageCache): void {
  if (cache.total !== null) {
    list.total = cache.total;
    list.ready = true;
    view.total = cache.total;
  }
  list.slow = cache.total === null;
  view.error = cache.error;
  reconcileFocus(cache);
  list.version++;
}

/**
 * After rows were refetched, keeps the focus on the same sample: follows it
 * if it moved, and falls to the row now at its index only once it is known
 * to have left the result.
 */
function reconcileFocus(cache: PageCache): void {
  const focused = selection.focusSample;
  if (!focused || selection.focusIndex < 0 || cache.total === null) return;
  if (cache.total === 0) {
    abandonRename();
    clearSelection();
    return;
  }
  const index = Math.min(selection.focusIndex, cache.total - 1);
  if (!cache.isFresh(index)) return;
  const row = cache.row(index);
  if (!row) return;
  if (row.id === focused.id) {
    if (index !== selection.focusIndex) setFocus(row, index);
    else if (JSON.stringify(row) !== JSON.stringify(focused)) selection.focusSample = row;
    return;
  }
  const moved = cache.indexOf(focused.id);
  const movedRow = moved >= 0 && cache.isFresh(moved) ? cache.row(moved) : undefined;
  if (movedRow) {
    follow(movedRow, moved);
    return;
  }
  // Not among the fresh rows. That is all the pages can say: the sample may
  // be on a page that is not loaded or not refetched yet (rows were added
  // above it, or it was renamed under a name sort), or it may be gone.
  void locateFocus(cache, focused);
}

/** The focused sample now sits at `index`: the focus, the anchor and the scroll position go with it. */
function follow(sample: Sample, index: number): void {
  const delta = index - selection.focusIndex;
  const top = LIST_PAD + selection.focusIndex * rowHeight();
  const onScreen = top + rowHeight() > list.scrollTop && top < list.scrollTop + list.viewportHeight;
  selection.anchorIndex = index;
  setFocus(sample, index);
  // Keep the row where the user is looking at it, rather than letting the
  // rows slide under the pointer. A list resting at its top stays there:
  // that is where newly added samples show up.
  if (onScreen && delta !== 0 && list.scrollTop > 0) scrollTo(list.scrollTop + delta * rowHeight());
}

function scrollTo(top: number): void {
  list.scrollTop = Math.max(0, top);
  if (viewport) viewport.scrollTop = list.scrollTop;
}

/**
 * Asks the backend where the focused sample is in the result now. One
 * lookup runs at a time; rows that change again meanwhile are picked up by
 * reconciling once more when it lands.
 */
async function locateFocus(cache: PageCache, focused: Sample): Promise<void> {
  if (locating) return;
  locating = true;
  const revision = activeRevision;
  const from = selection.focusIndex;
  let all: string[];
  try {
    all = await ids();
  } catch {
    // The search failed; the list shows that. Leave the focus where it is.
    return;
  } finally {
    locating = false;
  }
  // A new query, or the user moved the focus meanwhile: theirs wins.
  if (cache !== current || pending !== null) return;
  const now = selection.focusSample;
  if (now?.id !== focused.id || selection.focusIndex !== from) return;
  if (revision !== activeRevision) {
    reconcileFocus(cache);
    return;
  }
  const index = all.indexOf(focused.id);
  if (index >= 0) {
    follow(now, index);
    cache.ensure(index, index);
    return;
  }
  // The sample left the result; the row that took its place is next.
  const at = Math.min(from, all.length - 1);
  const row = cache.isFresh(at) ? cache.row(at) : undefined;
  // Not loaded yet: the page's arrival reconciles again, and the id list is cached.
  if (!row || row.id !== all[at]) return;
  if (list.renamingId === focused.id) abandonRename();
  const onlyFocused = selection.ids.size === 0 || (selection.ids.size === 1 && selection.ids.has(focused.id));
  if (onlyFocused) selectOnly(row, at);
  else setFocus(row, at);
}

/** The sample at a row, if loaded. Reactive: reruns when rows arrive. */
export function rowAt(index: number): Sample | undefined {
  void list.version;
  return current?.row(index);
}

/** Asks for the rows first..last; cheap when they are already loaded. */
export function ensureRange(first: number, last: number): void {
  if (last >= first) current?.ensure(first, last);
}

/** Rows that fit the viewport, for PageUp/PageDown. */
export function pageRows(): number {
  return Math.max(1, Math.floor(list.viewportHeight / rowHeight()));
}

function reveal(index: number): void {
  const top = scrollToReveal(index, list.scrollTop, list.viewportHeight, rowHeight(), LIST_PAD);
  if (top !== null) scrollTo(top);
}

/** Ids of the whole result in order; fetched once per revision. */
function ids(): Promise<string[]> {
  if (!allIds) {
    const request = api.searchIds(view.query, view.sort, view.desc);
    allIds = request;
    // A failure must not be the answer for the rest of the revision.
    request.catch(() => {
      if (allIds === request) allIds = null;
    });
  }
  return allIds;
}

async function selectRange(anchor: number, index: number): Promise<void> {
  const cache = current;
  if (!cache) return;
  const token = ++actionToken;
  const [first, last] = rangeBounds(anchor, index);
  let range = cache.idsInRange(first, last);
  if (!range) {
    // Part of the range was never scrolled through; ask for the id list.
    const all = await ids();
    if (token !== actionToken) return;
    range = all.slice(first, last + 1);
  }
  setSelection(range);
}

function autoAudition(sample: Sample): void {
  if (settings().playOnSelect) audition(sample);
}

export interface PickModifiers {
  /** Shift: select from the anchor to this row. */
  range: boolean;
  /** Cmd/Ctrl: toggle this row. */
  toggle: boolean;
}

/** A click on a row. */
export function pick(index: number, mods: PickModifiers = { range: false, toggle: false }): void {
  const row = current?.row(index);
  if (!row) return;
  if (mods.range && selection.anchorIndex >= 0) {
    setFocus(row, index);
    void selectRange(selection.anchorIndex, index);
  } else if (mods.toggle) {
    actionToken++;
    toggleSelect(row, index);
  } else {
    actionToken++;
    selectOnly(row, index);
    autoAudition(row);
  }
}

/** Moves the focus to a row: selects and auditions it, or extends the range to it. */
export function moveTo(index: number, extend = false): void {
  const cache = current;
  if (!cache || list.total <= 0 || index < 0) return;
  const target = Math.min(index, list.total - 1);
  const token = ++actionToken;
  reveal(target);
  const apply = (row: Sample) => {
    if (extend) {
      const anchor = selection.anchorIndex >= 0 ? selection.anchorIndex : Math.max(0, selection.focusIndex);
      selection.anchorIndex = anchor;
      setFocus(row, target);
      void selectRange(anchor, target);
    } else {
      selectOnly(row, target);
      autoAudition(row);
    }
  };
  const row = cache.row(target);
  if (row) {
    apply(row);
    return;
  }
  void cache.load(target).then((loaded) => {
    if (loaded && token === actionToken && cache === current) apply(loaded);
  });
}

export function navigate(key: NavKey, extend = false): void {
  moveTo(navTarget(key, selection.focusIndex, list.total, pageRows()), extend);
}

export async function selectAll(): Promise<void> {
  if (list.total === 0) return;
  const token = ++actionToken;
  const all = await ids();
  if (token === actionToken) setSelection(all);
}

/** Where a popover for the focused row should open (below its name). */
export function focusAnchor(): { x: number; y: number } {
  const row = viewport?.querySelector<HTMLElement>(`[data-index="${selection.focusIndex}"]`);
  const rect = (row ?? viewport)?.getBoundingClientRect();
  if (!rect) return { x: window.innerWidth / 2, y: window.innerHeight / 3 };
  return row ? { x: rect.left + 36, y: rect.bottom } : { x: rect.left + 36, y: rect.top + 36 };
}

// --- rename -------------------------------------------------------------------
//
// The rename belongs to the focused sample. Its state lives here; the input
// in the row only shows the draft and reports keys, because the row is
// unmounted whenever it leaves the mounted window, its index changes or its
// page reloads, and a webview does not reliably say so (WebKit fires no blur
// when a focused input is removed).

export function startRename(): void {
  const sample = selection.focusSample;
  if (!sample || !libraryUsable()) return;
  reveal(selection.focusIndex);
  renameTarget = sample;
  list.renameDraft = sample.name;
  list.renamingId = sample.id;
}

/** Drops the rename without touching the keyboard focus. */
function abandonRename(): void {
  renameTarget = null;
  list.renamingId = null;
  list.renameDraft = "";
}

/** Escape: forget the draft. */
export function cancelRename(): void {
  abandonRename();
}

/** Enter, or the field lost the focus: apply the draft. */
export async function commitRename(): Promise<void> {
  const sample = renameTarget;
  if (!sample || list.renamingId !== sample.id) return;
  const next = list.renameDraft.trim();
  abandonRename();
  if (next === "" || next === sample.name) return;
  try {
    await api.renameSample(sample.id, next);
  } catch (err) {
    pushToast(errorMessage(err), { level: "error" });
  }
}

/**
 * The focus is on another sample than the one being renamed (a click, an
 * arrow key from elsewhere): the rename ends as it does on blur, by applying
 * the draft. SampleList calls this from an effect.
 */
export function settleRename(): void {
  if (list.renamingId !== null && selection.focusSample?.id !== list.renamingId) void commitRename();
}

// --- drag out -----------------------------------------------------------------

/** Files can leave a read-only library, but not one whose folder is gone. */
export function canDragOut(): boolean {
  return libraryReadable();
}

/**
 * Hands the selection to the native file drag. If the pressed row is not
 * selected it becomes the selection first. Resolves when the drag has ended.
 */
export async function dragOut(index: number): Promise<void> {
  if (list.dragging || !canDragOut()) return;
  const row = current?.row(index);
  if (!row || row.missing) return;
  if (!selection.ids.has(row.id)) {
    actionToken++;
    selectOnly(row, index);
  }
  const dragged = [...selection.ids];
  list.dragging = true;
  try {
    const result = await api.startDrag(dragged);
    // Not started without an error is not a failure: the button was already
    // up again, or another drag is still ending.
    if (!result.started && result.error) {
      const reveal = formatKeys("Mod+Shift+R");
      const copy = formatKeys("Mod+Shift+C");
      pushToast(`Couldn’t drag out. Reveal the file (${reveal}) or copy its path (${copy}) instead.`, {
        level: "error",
        action: { label: "Reveal", run: () => api.reveal(row.id) },
      });
    }
  } catch (err) {
    pushToast(errorMessage(err), { level: "error" });
  } finally {
    list.dragging = false;
  }
}
