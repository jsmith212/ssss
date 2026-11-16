// Turns the flat tag list of the sidebar ("drums", "drums/kick") into a tree.

import type { TagCount } from "../types";

export interface TagNode {
  /** Full path, e.g. "drums/kick". */
  tag: string;
  /** Last segment. */
  name: string;
  /** Includes descendants. */
  count: number;
  color: string;
  depth: number;
  children: TagNode[];
}

/**
 * Builds the tree, sorted by name. A parent that is not listed itself (only
 * "drums/kick" exists) is created with the sum of its children.
 */
export function buildTagTree(tags: readonly TagCount[]): TagNode[] {
  const roots: TagNode[] = [];
  const byPath = new Map<string, TagNode>();
  const listed = new Set<string>();

  const node = (path: string): TagNode => {
    const existing = byPath.get(path);
    if (existing) return existing;
    const slash = path.lastIndexOf("/");
    const created: TagNode = {
      tag: path,
      name: path.slice(slash + 1),
      count: 0,
      color: "",
      depth: path.split("/").length - 1,
      children: [],
    };
    byPath.set(path, created);
    if (slash < 0) roots.push(created);
    else node(path.slice(0, slash)).children.push(created);
    return created;
  };

  for (const t of tags) {
    const path = t.tag.replace(/^\/+|\/+$/g, "");
    if (path === "") continue;
    const n = node(path);
    n.count = t.count;
    n.color = t.color;
    listed.add(path);
  }

  const finish = (nodes: TagNode[]): void => {
    nodes.sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base", numeric: true }));
    for (const n of nodes) {
      finish(n.children);
      if (!listed.has(n.tag)) n.count = n.children.reduce((sum, c) => sum + c.count, 0);
    }
  };
  finish(roots);
  return roots;
}

/** The rows to render: every root, and children of expanded nodes, depth-first. */
export function visibleTags(roots: readonly TagNode[], expanded: ReadonlySet<string>): TagNode[] {
  const out: TagNode[] = [];
  const walk = (nodes: readonly TagNode[]): void => {
    for (const n of nodes) {
      out.push(n);
      if (n.children.length > 0 && expanded.has(n.tag)) walk(n.children);
    }
  };
  walk(roots);
  return out;
}

/** Ancestors of a tag path, nearest last: "a/b/c" -> ["a", "a/b"]. */
export function ancestors(tag: string): string[] {
  const parts = tag.split("/");
  return parts.slice(0, -1).map((_, i) => parts.slice(0, i + 1).join("/"));
}
