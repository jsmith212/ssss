import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./api", () => ({ api: { isMock: true } }));

const state = await import("./state.svelte");
const { toasts, pushToast, escape, view } = state;

const messages = () => toasts.list.map((t) => t.message);

beforeEach(() => {
  toasts.list = [];
  view.query = "";
});

describe("toasts", () => {
  it("replaces a toast that says the same thing instead of stacking it", () => {
    pushToast("disk full", { level: "error" });
    pushToast("copied");
    pushToast("disk full", { level: "error" });
    expect(messages()).toEqual(["copied", "disk full"]);
    // The same words at another level are a different toast.
    pushToast("copied", { level: "error" });
    expect(messages()).toEqual(["copied", "disk full", "copied"]);
  });

  it("replaces by key", () => {
    pushToast("Removed a", { key: "edit" });
    pushToast("Removed b", { key: "edit" });
    expect(messages()).toEqual(["Removed b"]);
  });

  it("shows at most three, newest last", () => {
    for (const m of ["a", "b", "c", "d"]) pushToast(m, { level: "error" });
    expect(messages()).toEqual(["b", "c", "d"]);
  });
});

describe("escape", () => {
  it("dismisses error toasts, newest first, before it clears the query", () => {
    view.query = "kick";
    pushToast("first", { level: "error" });
    pushToast("info");
    pushToast("second", { level: "error" });
    escape();
    expect(messages()).toEqual(["first", "info"]);
    escape();
    expect(messages()).toEqual(["info"]);
    expect(view.query).toBe("kick");
    // Info toasts leave by themselves and do not hold Escape up.
    escape();
    expect(view.query).toBe("");
  });

  it("closes an open overlay first", () => {
    pushToast("failed", { level: "error" });
    state.openOverlay("settings");
    escape();
    expect(state.overlays.stack).toEqual([]);
    expect(messages()).toEqual(["failed"]);
  });
});
