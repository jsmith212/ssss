// Registers the shell's commands: list navigation, organising the selection,
// and the shortcuts that open overlays. Playback keys belong to the player.

import { api } from "../api";
import { player, stop } from "../audio";
import { formatKeys, registerAll, setErrorHandler, type Command } from "../commands";
import { addToken, hasFreeText, singleCollection, suggestViewName, type Named } from "../query";
import {
  announceUndoable,
  app,
  errorMessage,
  escape,
  libraryReadable,
  libraryUsable,
  openOverlay,
  overlays,
  pushToast,
  redo,
  selectedIds,
  selection,
  setQuery,
  setSort,
  settings,
  sidebar,
  toggleOverlay,
  undo,
  updateSettings,
  view,
} from "../state.svelte";
import type { SortKey } from "../types";
import { focusAnchor, list, navigate, selectAll, startRename } from "./controller.svelte";
import { leaf, plural } from "./format";
import type { NavKey } from "./selection";
import { startCreateCollection } from "./sidebar.svelte";

export const OMNIBAR_INPUT_ID = "omnibar-input";

export const SORT_LABELS: Record<SortKey, string> = {
  added: "Recent",
  name: "Name",
  bpm: "BPM",
  key: "Key",
  dur: "Length",
  pack: "Pack",
  relevance: "Relevance",
};

/** Sorts offered right now; relevance only means something with free text. */
export function sortChoices(): SortKey[] {
  const keys: SortKey[] = ["added", "name", "bpm", "key", "dur", "pack"];
  return hasFreeText(view.query) ? ["relevance", ...keys] : keys;
}

/** Picks a sort; picking the current one flips its direction. */
export function chooseSort(sort: SortKey): void {
  if (sort === view.sort) setSort(sort, !view.desc);
  else setSort(sort, sort === "added" || sort === "relevance");
}

export function focusOmnibar(): void {
  const input = document.getElementById(OMNIBAR_INPUT_ID) as HTMLInputElement | null;
  input?.focus();
  input?.select();
}

const noOverlay = () => overlays.stack.length === 0;
const listActive = () => noOverlay() && list.renamingId === null;
const hasSelection = () => selection.ids.size > 0;
const canEdit = () => libraryUsable() && hasSelection();
const hasFocus = () => selection.focusSample !== null;

function selectionLabel(ids: string[]): string {
  const focused = selection.focusSample;
  return ids.length === 1 && focused && focused.id === ids[0] ? focused.name : plural(ids.length, "sample");
}

export async function toggleFavorite(): Promise<void> {
  const ids = selectedIds();
  if (ids.length === 0) return;
  // The focused row decides the direction, so a mixed selection ends up uniform.
  await api.setFavorite(ids, !(selection.focusSample?.fav ?? false));
}

export async function togglePinnedTag(slot: number): Promise<void> {
  const tag = settings().pinnedTags[slot - 1];
  const ids = selectedIds();
  if (!tag || ids.length === 0) return;
  const remove = selection.focusSample?.tags.includes(tag) ?? false;
  if (remove) await api.removeTags(ids, [tag]);
  else await api.addTags(ids, [tag]);
  announceUndoable(`${remove ? "Removed" : "Added"} #${leaf(tag)} ${remove ? "from" : "to"} ${selectionLabel(ids)}`);
}

/**
 * The samples are about to leave the result. Leave only the focus selected;
 * once the rows reload, the list moves it to whatever now sits at that
 * index, so the same key can be pressed again.
 */
function collapseSelection(leaving: string[]): void {
  if (!leaving.some((id) => selection.ids.has(id))) return;
  selection.ids.clear();
  if (selection.focusSample) selection.ids.add(selection.focusSample.id);
}

/** Removes samples from the library (undoable); the Delete key and the row menu both end here. */
export async function removeSamples(ids: string[]): Promise<void> {
  if (ids.length === 0) return;
  const label = selectionLabel(ids);
  await api.removeSamples(ids);
  // A removed sample must not keep sounding.
  if (player.id !== null && ids.includes(player.id)) stop();
  collapseSelection(ids);
  announceUndoable(`Removed ${label} from library`);
}

/** The collection the list is showing, when the query is confined to exactly one. */
export function shownCollection(): Named | null {
  return singleCollection(view.query, sidebar.data?.collections ?? []);
}

/** Takes samples out of a collection (undoable); they stay in the library. */
export async function removeFromCollection(collection: Named, ids: string[]): Promise<void> {
  if (ids.length === 0) return;
  const label = selectionLabel(ids);
  await api.removeFromCollection(collection.id, ids);
  collapseSelection(ids);
  announceUndoable(`Removed ${label} from “${collection.name}”`);
}

export async function copyPaths(ids: string[]): Promise<void> {
  if (ids.length === 0) return;
  await api.copyPaths(ids);
  pushToast(`Copied ${ids.length === 1 ? "path" : plural(ids.length, "path")}`);
}

/** Asks for a name, then saves the current query as a view under it. */
export function saveView(): void {
  const query = view.query.trim();
  if (query === "") return;
  const views = sidebar.data?.views ?? [];
  const field = document.getElementById(OMNIBAR_INPUT_ID)?.getBoundingClientRect();
  openOverlay("rename", {
    anchor: field ? { x: field.left, y: field.bottom } : undefined,
    props: {
      title: "Save view as",
      value: suggestViewName(query, views, sidebar.data ?? undefined),
      // The suggestion is a name for something new: taking it as it is still saves.
      fresh: true,
      commit: async (name: string) => {
        const replaces = views.some((v) => v.name.toLowerCase() === name.toLowerCase());
        const saved = await api.saveView(name, query);
        pushToast(`${replaces ? "Updated" : "Saved"} view “${saved.name}”`);
      },
    },
  });
}

/** Names and creates a collection in a popover, for when the sidebar's inline field is out of sight. */
function promptNewCollection(): void {
  openOverlay("rename", {
    props: { title: "New collection", value: "", commit: async (name: string) => void (await api.createCollection(name)) },
  });
}

export async function addFiles(): Promise<void> {
  const paths = await api.chooseFiles("Add files");
  if (paths.length > 0) await api.import(paths);
}

function openAtFocus(id: "tags" | "collections" | "info"): void {
  openOverlay(id, { anchor: focusAnchor() });
}

function navigation(): Command[] {
  const keys: [NavKey, string, string][] = [
    ["up", "ArrowUp", "Previous sample"],
    ["down", "ArrowDown", "Next sample"],
    ["pageUp", "PageUp", "Page up"],
    ["pageDown", "PageDown", "Page down"],
    ["home", "Home", "First sample"],
    ["end", "End", "Last sample"],
  ];
  const moves: Command[] = keys.map(([nav, key, title]) => ({
    id: `list.${nav}`,
    title,
    keys: key,
    section: "List",
    hidden: true,
    repeat: true,
    when: listActive,
    run: () => navigate(nav),
  }));
  const extends_: Command[] = keys.map(([nav, key], i) => ({
    id: `list.${nav}.extend`,
    title: "Extend selection",
    keys: `Shift+${key}`,
    section: "List",
    hidden: true,
    // One line in the shortcut sheet stands for all six.
    sheet: i === 0 ? `${formatKeys("Shift+ArrowUp")}↓ …` : false,
    repeat: true,
    when: listActive,
    run: () => navigate(nav, true),
  }));
  return [...moves, ...extends_];
}

function pinnedTags(): Command[] {
  return Array.from({ length: 9 }, (_, i) => i + 1).map((slot) => ({
    id: `sample.pinnedTag.${slot}`,
    title: `Toggle pinned tag ${slot}`,
    keys: String(slot),
    section: "Organise",
    hidden: true,
    when: () => listActive() && canEdit() && Boolean(settings().pinnedTags[slot - 1]),
    run: () => togglePinnedTag(slot),
  }));
}

function sorts(): Command[] {
  return (Object.keys(SORT_LABELS) as SortKey[]).map((sort) => ({
    id: `view.sort.${sort}`,
    title: `Sort by ${SORT_LABELS[sort].toLowerCase()}`,
    section: "View",
    when: () => sortChoices().includes(sort),
    run: () => chooseSort(sort),
  }));
}

function commands(): Command[] {
  return [
    ...navigation(),
    {
      id: "list.selectAll",
      title: "Select all",
      keys: "Mod+A",
      section: "List",
      when: listActive,
      run: selectAll,
    },
    {
      id: "sample.rename",
      title: "Rename",
      keys: ["Enter", "F2"],
      section: "Organise",
      when: () => listActive() && libraryUsable() && hasFocus(),
      run: startRename,
    },
    {
      id: "sample.tags",
      title: "Tag…",
      keys: "T",
      section: "Organise",
      when: () => listActive() && canEdit(),
      run: () => openAtFocus("tags"),
    },
    {
      id: "sample.collections",
      title: "Add to collection…",
      keys: "C",
      section: "Organise",
      when: () => listActive() && canEdit(),
      run: () => openAtFocus("collections"),
    },
    {
      id: "sample.favorite",
      title: "Toggle favourite",
      keys: "F",
      section: "Organise",
      when: () => listActive() && canEdit(),
      run: toggleFavorite,
    },
    ...pinnedTags(),
    {
      id: "sample.info",
      title: "Show info",
      keys: "Mod+I",
      section: "Organise",
      when: hasFocus,
      run: () => toggleOverlay("info", { anchor: focusAnchor() }),
    },
    {
      id: "sample.removeFromCollection",
      title: "Remove from this collection",
      section: "Organise",
      when: () => listActive() && canEdit() && shownCollection() !== null,
      run: async () => {
        const collection = shownCollection();
        if (collection) await removeFromCollection(collection, selectedIds());
      },
    },
    {
      id: "sample.remove",
      title: "Remove from library",
      keys: ["Mod+Backspace", "Delete"],
      section: "Organise",
      when: () => listActive() && canEdit(),
      run: () => removeSamples(selectedIds()),
    },
    {
      id: "sample.reveal",
      title: "Reveal in file manager",
      keys: "Mod+Shift+R",
      section: "Organise",
      when: () => hasFocus() && libraryReadable(),
      run: async () => {
        if (selection.focusSample) await api.reveal(selection.focusSample.id);
      },
    },
    {
      id: "sample.copyPaths",
      title: "Copy file paths",
      keys: "Mod+Shift+C",
      section: "Organise",
      when: hasSelection,
      run: () => copyPaths(selectedIds()),
    },
    {
      id: "sample.reanalyze",
      title: "Re-analyse BPM and key",
      section: "Organise",
      when: canEdit,
      run: () => api.reanalyze(selectedIds()),
    },
    {
      id: "edit.undo",
      title: "Undo",
      keys: "Mod+Z",
      section: "Edit",
      run: undo,
    },
    {
      id: "edit.redo",
      title: "Redo",
      keys: ["Mod+Shift+Z", "Mod+Y"],
      section: "Edit",
      run: redo,
    },
    {
      id: "omnibar.focus",
      title: "Search or filter",
      keys: ["/", "Mod+F"],
      section: "View",
      run: focusOmnibar,
    },
    {
      id: "view.facets",
      title: "Toggle filter strip",
      keys: "Mod+Shift+F",
      section: "View",
      run: () => {
        view.facetsOpen = !view.facetsOpen;
      },
    },
    {
      id: "view.clear",
      title: "Clear filters",
      section: "View",
      when: () => view.query !== "",
      run: () => setQuery(""),
    },
    {
      id: "view.save",
      title: "Save view",
      keys: "Mod+S",
      section: "View",
      allowInInput: true,
      when: () => libraryUsable() && view.query.trim() !== "",
      run: saveView,
    },
    ...sorts(),
    {
      id: "view.sidebar",
      title: "Toggle sidebar",
      keys: "Mod+\\",
      section: "View",
      run: () => {
        // Below 900px the sidebar is hidden whatever the setting says; flipping
        // the setting unseen would leave it hidden once the window is wide again.
        if (app.narrow) pushToast("The sidebar is hidden while the window is this narrow", { key: "sidebar-narrow" });
        else void updateSettings({ sidebarOpen: !settings().sidebarOpen });
      },
    },
    {
      id: "view.favorites",
      title: "Show favourites",
      section: "View",
      run: () => setQuery("is:fav"),
    },
    {
      id: "view.untagged",
      title: "Show untagged",
      section: "View",
      run: () => setQuery("is:untagged"),
    },
    {
      id: "view.similarKey",
      title: "Filter to compatible keys",
      section: "View",
      when: () => Boolean(selection.focusSample?.key),
      run: () => setQuery(addToken(view.query, { kind: "key", value: `compatible:${selection.focusSample?.key}` })),
    },
    {
      id: "library.addFiles",
      title: "Add files…",
      section: "Library",
      when: libraryUsable,
      run: addFiles,
    },
    {
      id: "library.importSplice",
      title: "Import Splice folder…",
      section: "Library",
      when: libraryUsable,
      run: () => openOverlay("splice"),
    },
    {
      id: "library.rescan",
      title: "Rescan library folder",
      section: "Library",
      when: libraryUsable,
      run: () => api.rescan(),
    },
    {
      id: "library.newCollection",
      title: "New collection",
      section: "Library",
      when: libraryUsable,
      run: async () => {
        if (app.narrow) return promptNewCollection();
        if (!settings().sidebarOpen) await updateSettings({ sidebarOpen: true });
        startCreateCollection();
      },
    },
    {
      id: "overlay.palette",
      title: "Command palette",
      keys: "Mod+K",
      section: "App",
      allowInInput: true,
      hidden: true,
      run: () => toggleOverlay("palette"),
    },
    {
      id: "overlay.settings",
      title: "Settings",
      keys: "Mod+,",
      section: "App",
      run: () => toggleOverlay("settings"),
    },
    {
      id: "overlay.shortcuts",
      title: "Keyboard shortcuts",
      keys: "?",
      section: "App",
      run: () => toggleOverlay("shortcuts"),
    },
    {
      id: "app.escape",
      title: "Close or clear",
      keys: "Escape",
      section: "App",
      hidden: true,
      allowInInput: true,
      run: escape,
    },
  ];
}

/** Registers everything above; returns the function that removes it again. */
export function registerShellCommands(): () => void {
  setErrorHandler((_command, error) => void pushToast(errorMessage(error), { level: "error" }));
  return registerAll(commands());
}
