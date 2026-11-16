// Context menu contents for sample rows and sidebar items, and the actions
// they share with the overlay commands. The shell calls `openSampleMenu` /
// `openSidebarMenu` from its `contextmenu` handlers.

import { api } from "../../lib/api";
import { audition } from "../../lib/audio/player.svelte";
import { run as runCommand } from "../../lib/commands";
import {
  copyPaths as copyFilePaths,
  removeFromCollection,
  removeSamples as removeFromLibrary,
  shownCollection,
} from "../../lib/list/commands";
import { formatToken } from "../../lib/query";
import {
  announceUndoable,
  libraryReadable,
  libraryUsable,
  openOverlay,
  selectedIds,
  selection,
  setQuery,
  sidebar,
} from "../../lib/state.svelte";
import type { Sample } from "../../lib/types";
import { selectionAnchor, type Point } from "./focus";
import { attempt } from "./targets.svelte";

export interface MenuItem {
  label: string;
  /** Shortcut hint in `commands.ts` key syntax, e.g. "Mod+I". */
  keys?: string;
  danger?: boolean;
  disabled?: boolean;
  run: () => void | Promise<void>;
}

export interface Swatch {
  label: string;
  /** CSS colour; "" is the "no colour" choice. */
  color: string;
}

/** A row of colour choices, one of which may be current. */
export interface MenuSwatches {
  swatches: Swatch[];
  current: string;
  disabled?: boolean;
  pick: (color: string) => void | Promise<void>;
}

export type MenuEntry = MenuItem | MenuSwatches | "separator";

/** Tag colours: decoration only, chosen to read on both themes. */
const TAG_COLORS: Swatch[] = [
  { label: "No colour", color: "" },
  { label: "Red", color: "#e5484d" },
  { label: "Orange", color: "#f76b15" },
  { label: "Yellow", color: "#e2a336" },
  { label: "Green", color: "#30a46c" },
  { label: "Teal", color: "#12a594" },
  { label: "Blue", color: "#3e83dd" },
  { label: "Purple", color: "#8e4ec6" },
  { label: "Pink", color: "#d6409f" },
];

/** The shell's inline rename; without it Rename falls back to the rename popover. */
const RENAME_COMMAND = "sample.rename";

function pointOf(at: Point | MouseEvent): Point {
  return at instanceof MouseEvent ? { x: at.clientX, y: at.clientY } : at;
}

/** Opens the shared context menu; a MouseEvent has its native menu suppressed. */
export function openMenu(at: Point | MouseEvent, items: MenuEntry[], label = "Actions"): void {
  if (at instanceof MouseEvent) at.preventDefault();
  openOverlay("context", { anchor: pointOf(at), props: { items, label } });
}

/** Asks for a new name in a small popover and commits it; cancelled by Escape. */
export function promptRename(
  at: Point | undefined,
  title: string,
  value: string,
  commit: (value: string) => Promise<void>,
): void {
  openOverlay("rename", { anchor: at, props: { title, value, commit } });
}

// --- actions shared by menus and commands ---------------------------------------

export function openTags(ids?: string[], at?: Point): void {
  openOverlay("tags", { anchor: at ?? selectionAnchor(), props: ids ? { ids } : undefined });
}

export function openCollections(ids?: string[], at?: Point): void {
  openOverlay("collections", { anchor: at ?? selectionAnchor(), props: ids ? { ids } : undefined });
}

export function openInfo(id?: string, at?: Point): void {
  openOverlay("info", { anchor: at ?? selectionAnchor(), props: id ? { id } : undefined });
}

export async function removeSamples(ids: string[]): Promise<void> {
  await attempt(() => removeFromLibrary(ids));
}

export async function copyPaths(ids: string[]): Promise<void> {
  await attempt(() => copyFilePaths(ids));
}

// --- sample rows -----------------------------------------------------------------

/**
 * The row menu. It applies to the whole selection when the row is part of
 * it, otherwise to that row alone.
 */
export function sampleMenu(sample: Sample, at?: Point): MenuEntry[] {
  const ids = selection.ids.has(sample.id) ? selectedIds() : [sample.id];
  const single = ids.length === 1;
  const usable = libraryUsable();
  // Playing and revealing only read the files, which a read-only library allows.
  const readable = libraryReadable();
  const many = single ? "" : ` ${ids.length.toLocaleString()} samples`;
  const collection = shownCollection();

  return [
    { label: "Play", disabled: !readable || sample.missing, run: () => audition(sample) },
    "separator",
    { label: "Tag…", keys: "T", disabled: !usable, run: () => openTags(ids, at) },
    { label: "Add to collection…", keys: "C", disabled: !usable, run: () => openCollections(ids, at) },
    {
      label: sample.fav ? "Remove from favourites" : "Add to favourites",
      keys: "F",
      disabled: !usable,
      run: () => void attempt(() => api.setFavorite(ids, !sample.fav)),
    },
    {
      label: "Rename",
      keys: "Enter",
      disabled: !usable || !single,
      run: () => {
        // The inline rename edits the focus row, which is this one unless
        // the menu was opened on a selected row the focus has since left.
        if (selection.focusSample?.id === sample.id && runCommand(RENAME_COMMAND)) return;
        promptRename(at, "Rename sample", sample.name, (name) => api.renameSample(sample.id, name));
      },
    },
    "separator",
    { label: "Info", keys: "Mod+I", run: () => openInfo(sample.id, at) },
    {
      label: "Reveal in file manager",
      keys: "Mod+Shift+R",
      disabled: !readable,
      run: () => void attempt(() => api.reveal(sample.id)),
    },
    { label: single ? "Copy path" : "Copy paths", keys: "Mod+Shift+C", run: () => copyPaths(ids) },
    "separator",
    // In a collection's view "remove" could mean either; offer both by name.
    ...(collection
      ? [
          {
            label: `Remove${many} from “${collection.name}”`,
            disabled: !usable,
            run: () => void attempt(() => removeFromCollection(collection, ids)),
          },
        ]
      : []),
    {
      label: `Remove${many} from library`,
      keys: "Mod+Backspace",
      danger: true,
      disabled: !usable,
      run: () => removeSamples(ids),
    },
  ];
}

export function openSampleMenu(event: MouseEvent, sample: Sample): void {
  const at = pointOf(event);
  openMenu(event, sampleMenu(sample, at), sample.name);
}

// --- sidebar items ---------------------------------------------------------------

export type SidebarTarget =
  | { kind: "tag"; tag: string }
  | { kind: "collection"; id: string; name: string }
  | { kind: "pack"; id: string; name: string }
  | { kind: "view"; id: string; name: string; query: string };

export function sidebarMenu(target: SidebarTarget, at?: Point): MenuEntry[] {
  const usable = libraryUsable();
  switch (target.kind) {
    case "tag":
      return [
        { label: "Show samples", run: () => setQuery(formatToken({ kind: "tag", value: target.tag })) },
        "separator",
        {
          label: "Rename…",
          disabled: !usable,
          run: () => promptRename(at, "Rename tag", target.tag, (name) => api.renameTag(target.tag, name)),
        },
        {
          swatches: TAG_COLORS,
          current: sidebar.data?.tags.find((t) => t.tag === target.tag)?.color ?? "",
          disabled: !usable,
          pick: (color) => void attempt(() => api.setTagColor(target.tag, color)),
        },
        "separator",
        {
          label: "Delete tag",
          danger: true,
          disabled: !usable,
          run: async () => {
            if (await attempt(() => api.deleteTag(target.tag))) announceUndoable(`Deleted tag ${target.tag}`);
          },
        },
      ];
    case "collection":
      return [
        { label: "Show samples", run: () => setQuery(formatToken({ kind: "in", value: target.id })) },
        "separator",
        {
          label: "Rename…",
          disabled: !usable,
          run: () =>
            promptRename(at, "Rename collection", target.name, (name) => api.renameCollection(target.id, name)),
        },
        {
          label: "Delete collection",
          danger: true,
          disabled: !usable,
          run: async () => {
            if (await attempt(() => api.deleteCollection(target.id))) {
              announceUndoable(`Deleted collection ${target.name}`);
            }
          },
        },
      ];
    case "pack":
      return [
        { label: "Show samples", run: () => setQuery(formatToken({ kind: "pack", value: target.id })) },
        "separator",
        {
          label: "Rename…",
          disabled: !usable,
          run: () => promptRename(at, "Rename pack", target.name, (name) => api.renamePack(target.id, name)),
        },
      ];
    case "view":
      return [
        { label: "Show samples", run: () => setQuery(target.query) },
        "separator",
        {
          label: "Delete view",
          danger: true,
          disabled: !usable,
          run: async () => {
            if (await attempt(() => api.deleteView(target.id))) announceUndoable(`Deleted view ${target.name}`);
          },
        },
      ];
  }
}

export function openSidebarMenu(event: MouseEvent, target: SidebarTarget): void {
  const at = pointOf(event);
  const label = target.kind === "tag" ? target.tag : target.name;
  openMenu(event, sidebarMenu(target, at), label);
}
