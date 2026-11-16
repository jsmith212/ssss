// The list controller against a scripted backend: what happens to the focus,
// the selection and an inline rename when rows change underneath them.

import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Sample } from "../types";

const db = { rows: [] as Sample[], idCalls: 0, renamed: [] as [string, string][] };

vi.mock("../api", () => ({
  api: {
    isMock: true,
    // Later pages answer later, as they do when the list asks for several.
    search: async (req: { offset: number; limit: number }) => {
      const snapshot = db.rows;
      await new Promise((r) => setTimeout(r, 2 + (req.offset / 200) * 6));
      return { total: snapshot.length, rows: snapshot.slice(req.offset, req.offset + req.limit), error: "" };
    },
    searchIds: async () => {
      db.idCalls++;
      return db.rows.map((r) => r.id);
    },
    renameSample: async (id: string, name: string) => {
      db.renamed.push([id, name]);
    },
  },
}));

const sample = (id: string): Sample => ({ id, name: id, tags: [], missing: false, type: "oneshot" }) as unknown as Sample;
const added = (n: number) => Array.from({ length: n }, (_, i) => sample(`new${i}`));
const settle = () => new Promise((r) => setTimeout(r, 40));

async function load() {
  vi.resetModules();
  const controller = await import("./controller.svelte");
  const state = await import("../state.svelte");
  // An opened library, so editing commands are allowed.
  state.app.state = { status: "ok", settings: state.settings() } as typeof state.app.state;
  controller.sync("", "added", true, 0);
  await settle();
  return { ...controller, selection: state.selection, selectOnly: state.selectOnly };
}

beforeEach(() => {
  db.rows = Array.from({ length: 1000 }, (_, i) => sample(`s${i}`));
  db.idCalls = 0;
  db.renamed = [];
});

describe("focus when rows change underneath it", () => {
  it("follows a sample pushed onto a page that is not loaded", async () => {
    const c = await load();
    c.moveTo(150);
    // An import adds 60 rows on top (Recent sort): s150 is now row 210, on
    // page 1, and the list only refetches page 0, which it is showing.
    db.rows = [...added(60), ...db.rows];
    c.sync("", "added", true, 1);
    c.ensureRange(138, 175);
    await settle();
    expect(c.selection.focusSample?.id).toBe("s150");
    expect(c.selection.focusIndex).toBe(210);
    expect(c.selection.anchorIndex).toBe(210);
    expect([...c.selection.ids]).toEqual(["s150"]);
    // The page it moved to gets loaded, so the focus sample stays current.
    await settle();
    expect(c.rowAt(210)?.id).toBe("s150");
  });

  it("follows a sample across a page boundary when the pages arrive separately", async () => {
    const c = await load();
    c.moveTo(190);
    db.rows = [...added(20), ...db.rows];
    c.sync("", "added", true, 1);
    c.ensureRange(178, 215);
    await settle();
    await settle();
    expect(c.selection.focusSample?.id).toBe("s190");
    expect(c.selection.focusIndex).toBe(210);
    expect([...c.selection.ids]).toEqual(["s190"]);
  });

  it("scrolls with the focused row, unless the list rests at its top", async () => {
    const c = await load();
    c.list.viewportHeight = 640;
    c.moveTo(150);
    c.list.scrollTop = 4600; // row 150 is in view
    db.rows = [...added(60), ...db.rows];
    c.sync("", "added", true, 1);
    c.ensureRange(138, 175);
    await settle();
    expect(c.selection.focusIndex).toBe(210);
    expect(c.list.scrollTop).toBe(4600 + 60 * c.rowHeight());

    c.moveTo(3);
    c.list.scrollTop = 0;
    db.rows = [...added(5).map((s) => sample(`${s.id}b`)), ...db.rows];
    c.sync("", "added", true, 2);
    c.ensureRange(0, 40);
    await settle();
    expect(c.selection.focusIndex).toBe(8);
    expect(c.list.scrollTop).toBe(0);
  });

  it("keeps a multi-selection and moves only the focus", async () => {
    const c = await load();
    c.moveTo(150);
    c.pick(151, { range: false, toggle: true });
    db.rows = [...added(60), ...db.rows];
    c.sync("", "added", true, 1);
    c.ensureRange(138, 175);
    await settle();
    expect(c.selection.focusSample?.id).toBe("s151");
    expect(c.selection.focusIndex).toBe(211);
    expect([...c.selection.ids].sort()).toEqual(["s150", "s151"]);
  });

  it("moves to the row that took its place once the sample is really gone", async () => {
    const c = await load();
    c.moveTo(150);
    db.rows = db.rows.filter((r) => r.id !== "s150");
    c.sync("", "added", true, 1);
    c.ensureRange(138, 175);
    await settle();
    expect(c.selection.focusSample?.id).toBe("s151");
    expect(c.selection.focusIndex).toBe(150);
    expect([...c.selection.ids]).toEqual(["s151"]);
  });

  it("asks where the sample went once per change, not once per page", async () => {
    const c = await load();
    c.moveTo(150);
    db.rows = [...added(260), ...db.rows];
    c.sync("", "added", true, 1);
    c.ensureRange(0, 599);
    await settle();
    await settle();
    expect(c.selection.focusIndex).toBe(410);
    expect(db.idCalls).toBe(1);
  });

  it("lets a pick made while the lookup is in flight win", async () => {
    const c = await load();
    c.moveTo(150);
    db.rows = [...added(60), ...db.rows];
    c.sync("", "added", true, 1);
    c.ensureRange(0, 199);
    await new Promise((r) => setTimeout(r, 5));
    c.pick(3);
    await settle();
    expect(c.selection.focusSample?.id).toBe("new3");
    expect(c.selection.focusIndex).toBe(3);
  });
});

describe("inline rename", () => {
  it("keeps the draft while the row moves, and renames the sample it started on", async () => {
    const c = await load();
    c.moveTo(5);
    c.startRename();
    expect(c.list.renamingId).toBe("s5");
    expect(c.list.renameDraft).toBe("s5");
    c.list.renameDraft = "My_New_";
    // Rows shift under the field (an import), twice.
    for (let revision = 1; revision <= 2; revision++) {
      db.rows = [...added(3).map((s) => sample(`${s.id}r${revision}`)), ...db.rows];
      c.sync("", "added", true, revision);
      c.ensureRange(0, 40);
      await settle();
      c.settleRename();
    }
    expect(c.list.renamingId).toBe("s5");
    expect(c.selection.focusIndex).toBe(11);
    expect(c.list.renameDraft).toBe("My_New_");
    c.list.renameDraft = "My_New_Name";
    await c.commitRename();
    expect(db.renamed).toEqual([["s5", "My_New_Name"]]);
    expect(c.list.renamingId).toBeNull();
    // A late blur after the commit must not rename again.
    await c.commitRename();
    expect(db.renamed).toHaveLength(1);
  });

  it("does not call the backend for an unchanged or empty name, or after Escape", async () => {
    const c = await load();
    c.moveTo(5);
    c.startRename();
    await c.commitRename();
    c.startRename();
    c.list.renameDraft = "   ";
    await c.commitRename();
    c.startRename();
    c.list.renameDraft = "other";
    c.cancelRename();
    await c.commitRename();
    expect(db.renamed).toEqual([]);
    expect(c.list.renamingId).toBeNull();
  });

  it("applies the draft when the focus moves to another sample", async () => {
    const c = await load();
    c.moveTo(5);
    c.startRename();
    c.list.renameDraft = "renamed";
    c.pick(8);
    c.settleRename();
    await settle();
    expect(db.renamed).toEqual([["s5", "renamed"]]);
    expect(c.list.renamingId).toBeNull();
  });

  it("ends, without renaming, when the sample leaves the list or the query changes", async () => {
    const c = await load();
    c.moveTo(5);
    c.startRename();
    c.list.renameDraft = "typed";
    db.rows = db.rows.filter((r) => r.id !== "s5");
    c.sync("", "added", true, 1);
    c.ensureRange(0, 40);
    await settle();
    expect(c.list.renamingId).toBeNull();
    expect(c.selection.focusSample?.id).toBe("s6");

    c.startRename();
    c.sync("kick", "added", true, 1);
    expect(c.list.renamingId).toBeNull();
    c.settleRename();
    await settle();
    expect(db.renamed).toEqual([]);
  });
});
