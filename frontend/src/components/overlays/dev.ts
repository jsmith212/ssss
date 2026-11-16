// Development-only driver for reviewing overlays against the mock backend
// (it is never loaded by a production build):
//
//   ?overlay=tags|collections|info|palette|settings|shortcuts|splice|
//            context|sidebar-menu|rename|toasts|jobs|drop|drop-pack|
//            drag-tag|drag-pack (our own drag-out over a sidebar item)
//   &sel=3        number of rows to select first (default 1)
//   &theme=dark   force a theme
//   &type=kick    text to type into the overlay's filter
//
// First run needs no driver: ?state=none shows it.

import { tick } from "svelte";
import { api } from "../../lib/api";
import { announceUndoable, openOverlay, pushToast, selectOnly, setSelection, updateSettings } from "../../lib/state.svelte";
import { list } from "../../lib/list/controller.svelte";
import type { Theme } from "../../lib/types";
import { selectionAnchor } from "./focus";
import { openCollections, openInfo, openSampleMenu, openSidebarMenu, openTags, promptRename } from "./menus";
import { jobsUi } from "./ui.svelte";

interface MockHooks {
  drop(paths: string[], kind?: string, id?: string): Promise<void>;
}

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

function typeInto(text: string): void {
  const input = document.querySelector<HTMLInputElement>("[data-surface] input[type=text]");
  if (!input) return;
  input.value = text;
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

function fakeContextEvent(x: number, y: number): MouseEvent {
  return new MouseEvent("contextmenu", { clientX: x, clientY: y, cancelable: true });
}

export async function runDevDriver(): Promise<void> {
  const params = new URLSearchParams(location.search);
  const theme = params.get("theme");
  if (theme) await updateSettings({ theme: theme as Theme });
  const name = params.get("overlay");
  if (!name) return;

  const { rows } = await api.search({ query: "", sort: "added", desc: true, offset: 0, limit: 20 });
  const count = Math.max(1, Number(params.get("sel")) || 1);
  if (rows.length > 0) {
    selectOnly(rows[0], 0);
    setSelection(rows.slice(0, count).map((r) => r.id));
  }
  // Let the list render the selection so popovers can anchor to it.
  await sleep(150);
  await tick();
  const mock = (window as unknown as { __ssssMock?: MockHooks }).__ssssMock;
  const at = selectionAnchor() ?? { x: 400, y: 200 };

  switch (name) {
    case "tags":
      openTags();
      break;
    case "collections":
      openCollections();
      break;
    case "info":
      openInfo();
      break;
    case "context":
      if (rows[0]) openSampleMenu(fakeContextEvent(at.x + 80, at.y), rows[0]);
      break;
    case "sidebar-menu":
      openSidebarMenu(fakeContextEvent(60, 320), { kind: "tag", tag: "drums/kick" });
      break;
    case "rename":
      if (rows[0]) promptRename(at, "Rename sample", rows[0].name, (v) => api.renameSample(rows[0].id, v));
      break;
    case "toasts":
      pushToast("Copied 3 paths");
      announceUndoable("Removed 3 samples");
      pushToast("1,032 added · 41 already in library", { action: { label: "Show", run: () => {} }, timeout: 0 });
      pushToast("Kick_Deep_01.wav could not be read: unexpected end of file", { level: "error" });
      break;
    case "jobs":
      await mock?.drop([..."ABCDEFGH"].map((n) => `/home/you/Downloads/Dusty Breaks ${n}`));
      await sleep(400);
      jobsUi.open = true;
      break;
    case "drop":
      document.body.classList.add("file-drop-target-active");
      break;
    case "drop-pack":
      document.querySelector('[data-drop-kind="pack"]')?.classList.add("file-drop-target-active");
      break;
    case "drag-tag":
    case "drag-pack":
      list.dragging = true;
      document
        .querySelector(`[data-drop-kind="${name === "drag-tag" ? "tag" : "pack"}"]`)
        ?.classList.add("file-drop-target-active");
      break;
    default:
      openOverlay(name as Parameters<typeof openOverlay>[0]);
  }

  const text = params.get("type");
  if (text) {
    await sleep(100);
    typeInto(text);
  }
}
