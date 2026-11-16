// Browser stand-in for Wails' native file-drop handling, used with the mock
// backend only. Inside Wails the runtime (or Go, on Linux and macOS) marks
// the hovered `[data-file-drop-target]` with `file-drop-target-active` and Go
// receives the drop; here the DOM drag events do the same and the drop goes
// to the mock, so dragging real files onto `npm run dev` behaves alike.

import { report } from "./targets.svelte";

const TARGET = "[data-file-drop-target]";
const ACTIVE = "file-drop-target-active";

type DropKind = "pack" | "tag" | "collection" | "list";

interface MockDropBackend {
  drop(paths: string[], kind?: DropKind, id?: string): Promise<void>;
}

function backend(): MockDropBackend | undefined {
  return (window as unknown as { __ssssMock?: MockDropBackend }).__ssssMock;
}

function hasFiles(event: DragEvent): boolean {
  return event.dataTransfer?.types.includes("Files") ?? false;
}

/** Installs the drag listeners; returns the function that removes them. */
export function installMockDrop(): () => void {
  let current: Element | null = null;

  const mark = (target: Element | null) => {
    if (current === target) return;
    current?.classList.remove(ACTIVE);
    target?.classList.add(ACTIVE);
    current = target;
  };

  const over = (event: DragEvent) => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    const target = (event.target as Element | null)?.closest(TARGET) ?? null;
    if (event.dataTransfer) event.dataTransfer.dropEffect = target ? "copy" : "none";
    mark(target);
  };

  const leave = (event: DragEvent) => {
    // Leaving the window, not moving between elements inside it.
    if (event.relatedTarget === null) mark(null);
  };

  const dropped = (event: DragEvent) => {
    if (!hasFiles(event)) return;
    event.preventDefault();
    const target = current as HTMLElement | null;
    mark(null);
    if (!target) return;
    // A browser exposes names, not paths; the mock only needs the names.
    const names = [...(event.dataTransfer?.files ?? [])].map((f) => f.name);
    const kind = (target.dataset.dropKind ?? "list") as DropKind;
    backend()?.drop(names, kind, target.dataset.dropId ?? "").catch(report);
  };

  document.addEventListener("dragenter", over);
  document.addEventListener("dragover", over);
  document.addEventListener("dragleave", leave);
  document.addEventListener("drop", dropped);
  return () => {
    mark(null);
    document.removeEventListener("dragenter", over);
    document.removeEventListener("dragover", over);
    document.removeEventListener("dragleave", leave);
    document.removeEventListener("drop", dropped);
  };
}
