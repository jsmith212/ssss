import { afterEach, describe, expect, it, vi } from "vitest";
import {
  dispatchKey,
  formatKeys,
  isComposing,
  list,
  matchesBinding,
  parseBinding,
  register,
  reset,
  resolve,
  run,
  setMac,
} from "./commands";

interface KeyInit {
  key: string;
  code?: string;
  ctrl?: boolean;
  meta?: boolean;
  shift?: boolean;
  alt?: boolean;
  repeat?: boolean;
  target?: unknown;
  composing?: boolean;
  keyCode?: number;
}

function key(init: KeyInit): KeyboardEvent {
  const event = {
    key: init.key,
    code: init.code ?? "",
    ctrlKey: init.ctrl ?? false,
    metaKey: init.meta ?? false,
    shiftKey: init.shift ?? false,
    altKey: init.alt ?? false,
    repeat: init.repeat ?? false,
    isComposing: init.composing ?? false,
    keyCode: init.keyCode ?? 0,
    defaultPrevented: false,
    target: init.target ?? null,
    preventDefault() {
      event.defaultPrevented = true;
    },
  };
  return event as unknown as KeyboardEvent;
}

const input = { tagName: "INPUT", type: "text", isContentEditable: false };
const checkbox = { tagName: "INPUT", type: "checkbox", isContentEditable: false };

afterEach(() => {
  reset();
  setMac(false);
});

describe("parseBinding", () => {
  it.each([
    ["Mod+K", { mod: true, shift: false, alt: false, key: "k" }],
    ["Mod+Shift+Z", { mod: true, shift: true, alt: false, key: "z" }],
    ["Shift+ArrowUp", { mod: false, shift: true, alt: false, key: "arrowup" }],
    ["Space", { mod: false, shift: false, alt: false, key: "space" }],
    ["Mod+\\", { mod: true, shift: false, alt: false, key: "\\" }],
    ["+", { mod: false, shift: false, alt: false, key: "+" }],
    ["Mod++", { mod: true, shift: false, alt: false, key: "+" }],
    ["?", { mod: false, shift: false, alt: false, key: "?" }],
  ])("%s", (spec, want) => {
    expect(parseBinding(spec)).toEqual(want);
  });
});

describe("matchesBinding", () => {
  it.each([
    ["Mod+K", { key: "k", ctrl: true }, false, true],
    ["Mod+K", { key: "k", meta: true }, false, false],
    ["Mod+K", { key: "k", meta: true }, true, true],
    ["Mod+K", { key: "k", ctrl: true }, true, false],
    ["Mod+K", { key: "k" }, false, false],
    ["Mod+Shift+Z", { key: "Z", ctrl: true, shift: true }, false, true],
    ["Mod+Z", { key: "Z", ctrl: true, shift: true }, false, false],
    ["L", { key: "l" }, false, true],
    ["L", { key: "L", shift: true }, false, false],
    ["?", { key: "?", shift: true }, false, true],
    ["Space", { key: " ", code: "Space" }, false, true],
    ["ArrowUp", { key: "ArrowUp" }, false, true],
    ["ArrowUp", { key: "ArrowUp", shift: true }, false, false],
    ["Shift+ArrowUp", { key: "ArrowUp", shift: true }, false, true],
    ["F", { key: "f", alt: true }, false, false],
    // Cyrillic layout: the physical F key still triggers "F".
    ["F", { key: "а", code: "KeyF" }, false, true],
    // German macOS types "[" as Option+5, German Windows as AltGr+8 (Ctrl+Alt).
    ["[", { key: "[", code: "BracketLeft" }, false, true],
    ["[", { key: "[", code: "Digit5", alt: true }, true, true],
    ["[", { key: "[", code: "Digit8", ctrl: true, alt: true }, false, true],
    ["]", { key: "]", code: "Digit9", ctrl: true, alt: true }, false, true],
    // Option and AltGr are only forgiven for symbols.
    ["L", { key: "l", ctrl: true, alt: true }, false, false],
    ["Mod+K", { key: "k", ctrl: true, alt: true }, false, false],
    // AltGr+ß types a plain backslash; that is not Ctrl+\.
    ["Mod+\\", { key: "\\", code: "Minus", ctrl: true, alt: true }, false, false],
    ["Mod+\\", { key: "\\", code: "Backslash", ctrl: true }, false, true],
    // Where "\" needs AltGr, Ctrl plus the key in its US position stands in.
    ["Mod+\\", { key: "#", code: "Backslash", ctrl: true }, false, true],
    ["Mod+\\", { key: "#", code: "Backslash" }, false, false],
    ["Mod+\\", { key: "\\", code: "Digit7", meta: true, alt: true, shift: true }, true, true],
    ["Mod+,", { key: ",", code: "Comma", ctrl: true }, false, true],
  ] as [string, KeyInit, boolean, boolean][])("%s with %o (mac=%s) -> %s", (spec, init, mac, want) => {
    expect(matchesBinding(parseBinding(spec), key(init), mac)).toBe(want);
  });
});

describe("dispatch", () => {
  it("runs the matching command and consumes the event", () => {
    const fn = vi.fn();
    register({ id: "loop", title: "Loop", keys: "L", run: fn });
    const e = key({ key: "l" });
    expect(dispatchKey(e)).toBe(true);
    expect(fn).toHaveBeenCalledOnce();
    expect(e.defaultPrevented).toBe(true);
  });

  it("leaves unbound keys alone", () => {
    register({ id: "loop", title: "Loop", keys: "L", run: () => {} });
    const e = key({ key: "x" });
    expect(dispatchKey(e)).toBe(false);
    expect(e.defaultPrevented).toBe(false);
  });

  it("skips plain keys and text-editing combos while typing", () => {
    register({ id: "loop", title: "Loop", keys: "L", run: () => {} });
    register({ id: "play", title: "Play", keys: "Space", run: () => {} });
    register({ id: "all", title: "Select all", keys: "Mod+A", run: () => {} });
    register({ id: "undo", title: "Undo", keys: "Mod+Z", run: () => {} });
    register({ id: "palette", title: "Palette", keys: "Mod+K", run: () => {} });
    register({ id: "escape", title: "Escape", keys: "Escape", allowInInput: true, run: () => {} });

    expect(resolve(key({ key: "l", target: input }))).toBeUndefined();
    expect(resolve(key({ key: " ", code: "Space", target: input }))).toBeUndefined();
    expect(resolve(key({ key: "a", ctrl: true, target: input }))).toBeUndefined();
    expect(resolve(key({ key: "z", ctrl: true, target: input }))).toBeUndefined();
    expect(resolve(key({ key: "k", ctrl: true, target: input }))?.id).toBe("palette");
    expect(resolve(key({ key: "Escape", target: input }))?.id).toBe("escape");
    // A checkbox is not a text field.
    expect(resolve(key({ key: "l", target: checkbox }))?.id).toBe("loop");
  });

  it("honours when() and prefers the latest registration", () => {
    let overlayOpen = false;
    register({ id: "global", title: "Global", keys: "Enter", run: () => {} });
    register({ id: "overlay", title: "Overlay", keys: "Enter", when: () => overlayOpen, run: () => {} });
    expect(resolve(key({ key: "Enter" }))?.id).toBe("global");
    overlayOpen = true;
    expect(resolve(key({ key: "Enter" }))?.id).toBe("overlay");
  });

  it("ignores key repeat unless the command opts in", () => {
    register({ id: "play", title: "Play", keys: "Space", run: () => {} });
    register({ id: "down", title: "Down", keys: "ArrowDown", repeat: true, run: () => {} });
    expect(resolve(key({ key: " ", code: "Space", repeat: true }))).toBeUndefined();
    expect(resolve(key({ key: "ArrowDown", repeat: true }))?.id).toBe("down");
  });

  it("supports alternative bindings", () => {
    register({ id: "find", title: "Find", keys: ["/", "Mod+F"], run: () => {} });
    expect(resolve(key({ key: "/" }))?.id).toBe("find");
    expect(resolve(key({ key: "f", ctrl: true }))?.id).toBe("find");
  });
});

describe("registry", () => {
  it("lists visible, available commands and unregisters", () => {
    const off = register({ id: "a", title: "A", run: () => {} });
    register({ id: "b", title: "B", hidden: true, run: () => {} });
    register({ id: "c", title: "C", when: () => false, run: () => {} });
    expect(list().map((c) => c.id)).toEqual(["a"]);
    off();
    expect(list()).toEqual([]);
  });

  it("run() reports whether the command ran", () => {
    const fn = vi.fn();
    register({ id: "a", title: "A", run: fn });
    register({ id: "off", title: "Off", when: () => false, run: fn });
    expect(run("a")).toBe(true);
    expect(run("off")).toBe(false);
    expect(run("missing")).toBe(false);
    expect(fn).toHaveBeenCalledOnce();
  });
});

describe("formatKeys", () => {
  it.each([
    ["Mod+Shift+Z", true, "⌘⇧Z"],
    ["Mod+Shift+Z", false, "Ctrl+Shift+Z"],
    ["Space", false, "Space"],
    ["ArrowUp", true, "↑"],
    ["Mod+Backspace", true, "⌘⌫"],
    ["?", false, "?"],
  ])("%s (mac=%s) -> %s", (spec, mac, want) => {
    expect(formatKeys(spec, mac)).toBe(want);
  });
});

describe("focused controls", () => {
  const button = { tagName: "BUTTON", isContentEditable: false, closest: (sel: string) => (sel.includes("button") ? {} : null) };
  const plain = { tagName: "DIV", isContentEditable: false, closest: () => null };

  it("leaves Enter and Space to a focused button", () => {
    const fn = vi.fn();
    register({ id: "rename", title: "Rename", keys: "Enter", run: fn });
    register({ id: "play", title: "Play", keys: "Space", run: fn });
    expect(resolve(key({ key: "Enter", target: button }))).toBeUndefined();
    expect(resolve(key({ key: " ", code: "Space", target: button }))).toBeUndefined();
    expect(resolve(key({ key: "Enter", target: plain }))?.id).toBe("rename");
  });

  it("still runs other shortcuts and allowInInput commands there", () => {
    const fn = vi.fn();
    register({ id: "fav", title: "Favourite", keys: "F", run: fn });
    register({ id: "open", title: "Open", keys: "Mod+Enter", run: fn });
    register({ id: "force", title: "Force", keys: "Enter", allowInInput: true, run: fn });
    expect(resolve(key({ key: "f", target: button }))?.id).toBe("fav");
    expect(resolve(key({ key: "Enter", ctrl: true, target: button }))?.id).toBe("open");
    expect(resolve(key({ key: "Enter", target: button }))?.id).toBe("force");
  });
});

describe("IME composition", () => {
  it("recognises composing keys, including WebKit's late Enter", () => {
    expect(isComposing(key({ key: "Enter" }))).toBe(false);
    expect(isComposing(key({ key: "a", composing: true }))).toBe(true);
    // Sent after compositionend: isComposing is already false.
    expect(isComposing(key({ key: "Enter", keyCode: 229 }))).toBe(true);
  });

  it("does not run commands on them", () => {
    const fn = vi.fn();
    register({ id: "go", title: "Go", keys: "Enter", run: fn });
    expect(dispatchKey(key({ key: "Enter", keyCode: 229 }))).toBe(false);
    expect(dispatchKey(key({ key: "Enter", keyCode: 13 }))).toBe(true);
    expect(fn).toHaveBeenCalledOnce();
  });
});
