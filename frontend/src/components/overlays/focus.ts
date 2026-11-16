// Focus management and anchoring shared by the overlay surfaces.

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

export function focusables(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(
    (el) => el.tabIndex >= 0 && el.getClientRects().length > 0 && !el.closest("[inert]"),
  );
}

/** Keeps Tab and Shift+Tab inside `root`. */
export function trapTab(event: KeyboardEvent, root: HTMLElement): void {
  const items = focusables(root);
  if (items.length === 0) {
    event.preventDefault();
    root.focus();
    return;
  }
  const first = items[0];
  const last = items[items.length - 1];
  const active = document.activeElement;
  if (event.shiftKey && (active === first || active === root)) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && active === last) {
    event.preventDefault();
    first.focus();
  }
}

/** The sample list, which is where focus goes when an overlay has nowhere better to return it. */
export function listElement(): HTMLElement | null {
  return document.querySelector<HTMLElement>('[role="listbox"][aria-multiselectable="true"]');
}

export interface Point {
  x: number;
  y: number;
}

/**
 * Where a popover about the selection should appear: just under the focus
 * row (or the first selected row) when it is on screen, else the top of the
 * list. Undefined when there is no list, which centres the popover.
 */
export function selectionAnchor(): Point | undefined {
  const list = listElement();
  if (!list) return undefined;
  const bounds = list.getBoundingClientRect();
  const row =
    list.querySelector<HTMLElement>('[data-focused="true"]') ??
    list.querySelector<HTMLElement>('[role="option"][aria-selected="true"]');
  if (row) {
    const r = row.getBoundingClientRect();
    if (r.bottom > bounds.top && r.top < bounds.bottom) return { x: r.left + 28, y: r.bottom + 2 };
  }
  return { x: bounds.left + 28, y: bounds.top + 8 };
}
