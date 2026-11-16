// Svelte action for the shell's small menus: while the menu is open, a press
// outside the node or Escape closes it (before the global Escape command
// sees the key).

export interface Dismissable {
  open: () => boolean;
  close: () => void;
}

export function dismissable(node: HTMLElement, options: Dismissable) {
  let current = options;
  const onpointerdown = (e: PointerEvent) => {
    if (current.open() && !node.contains(e.target as Node)) current.close();
  };
  const onkeydown = (e: KeyboardEvent) => {
    if (e.key !== "Escape" || !current.open()) return;
    e.preventDefault();
    e.stopPropagation();
    current.close();
  };
  window.addEventListener("pointerdown", onpointerdown, true);
  window.addEventListener("keydown", onkeydown, true);
  return {
    update(next: Dismissable) {
      current = next;
    },
    destroy() {
      window.removeEventListener("pointerdown", onpointerdown, true);
      window.removeEventListener("keydown", onkeydown, true);
    },
  };
}
