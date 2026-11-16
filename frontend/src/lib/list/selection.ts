// Pure index arithmetic behind the list's keyboard navigation and ranges.

export type NavKey = "up" | "down" | "pageUp" | "pageDown" | "home" | "end";

/**
 * The row a navigation key lands on, or -1 for an empty list. With no focus
 * yet, every key but End starts at the top.
 */
export function navTarget(key: NavKey, focus: number, total: number, pageRows: number): number {
  if (total <= 0) return -1;
  const last = total - 1;
  if (key === "home") return 0;
  if (key === "end") return last;
  if (focus < 0) return 0;
  const page = Math.max(1, pageRows - 1);
  const delta = { up: -1, down: 1, pageUp: -page, pageDown: page }[key];
  return Math.min(last, Math.max(0, focus + delta));
}

/** Inclusive [first, last] of a Shift-range between the anchor and a row. */
export function rangeBounds(anchor: number, index: number): [number, number] {
  return anchor <= index ? [anchor, index] : [index, anchor];
}

/**
 * First and last row to render for a scroll position, overscan included.
 * `pad` is the empty space above the first row.
 */
export function visibleRange(
  scrollTop: number,
  viewportHeight: number,
  rowHeight: number,
  total: number,
  overscan: number,
  pad = 0,
): { first: number; last: number } {
  if (total <= 0 || rowHeight <= 0) return { first: 0, last: -1 };
  const top = scrollTop - pad;
  const first = Math.max(0, Math.floor(top / rowHeight) - overscan);
  const last = Math.min(total - 1, Math.ceil((top + viewportHeight) / rowHeight) - 1 + overscan);
  return { first, last };
}

/**
 * The scrollTop that brings a row fully into view, or null if it already
 * is. `pad` is the empty space above the first and below the last row; it
 * is kept in view with the row, so the list never rests with a row glued to
 * an edge.
 */
export function scrollToReveal(
  index: number,
  scrollTop: number,
  viewportHeight: number,
  rowHeight: number,
  pad = 0,
): number | null {
  const top = pad + index * rowHeight;
  if (top - pad < scrollTop) return top - pad;
  if (top + rowHeight + pad > scrollTop + viewportHeight) return top + rowHeight + pad - viewportHeight;
  return null;
}
