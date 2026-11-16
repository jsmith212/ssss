// Sidebar UI state that is not part of the query: which sections and tag
// branches are open (remembered per machine) and what is being edited inline.

import { SvelteSet } from "svelte/reactivity";

export type SidebarSection = "views" | "collections" | "packs" | "tags";
export type EditableKind = "collection" | "pack" | "tag";

const STORAGE_KEY = "ssss.sidebar";

interface Stored {
  collapsed: string[];
  expandedTags: string[];
}

function load(): Stored {
  try {
    const raw = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? "{}") as Partial<Stored>;
    return {
      collapsed: Array.isArray(raw.collapsed) ? raw.collapsed.map(String) : [],
      expandedTags: Array.isArray(raw.expandedTags) ? raw.expandedTags.map(String) : [],
    };
  } catch {
    return { collapsed: [], expandedTags: [] };
  }
}

const stored = load();

export const sidebarUi = $state({
  collapsed: new SvelteSet<string>(stored.collapsed),
  expandedTags: new SvelteSet<string>(stored.expandedTags),
  /** The "new collection" input is showing. */
  creatingCollection: false,
  /** Item being renamed inline; `id` is the tag path for tags. */
  renaming: null as { kind: EditableKind; id: string } | null,
});

function save(): void {
  try {
    const value: Stored = {
      collapsed: [...sidebarUi.collapsed],
      expandedTags: [...sidebarUi.expandedTags],
    };
    localStorage.setItem(STORAGE_KEY, JSON.stringify(value));
  } catch {
    // Storage can be unavailable; the sidebar then simply forgets.
  }
}

export function toggleSection(section: SidebarSection): void {
  if (!sidebarUi.collapsed.delete(section)) sidebarUi.collapsed.add(section);
  save();
}

export function toggleTag(tag: string): void {
  if (!sidebarUi.expandedTags.delete(tag)) sidebarUi.expandedTags.add(tag);
  save();
}

export function expandTags(tags: Iterable<string>): void {
  let changed = false;
  for (const tag of tags) {
    if (sidebarUi.expandedTags.has(tag)) continue;
    sidebarUi.expandedTags.add(tag);
    changed = true;
  }
  if (changed) save();
}

/** Shows the inline "new collection" input, opening its section if needed. */
export function startCreateCollection(): void {
  if (sidebarUi.collapsed.delete("collections")) save();
  sidebarUi.renaming = null;
  sidebarUi.creatingCollection = true;
}

export function startRenameItem(kind: EditableKind, id: string): void {
  sidebarUi.creatingCollection = false;
  sidebarUi.renaming = { kind, id };
}

export function stopEditing(): void {
  sidebarUi.creatingCollection = false;
  sidebarUi.renaming = null;
}
