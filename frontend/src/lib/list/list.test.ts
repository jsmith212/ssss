import { describe, expect, it, vi } from "vitest";
import { DragTracker, LostRelease } from "./dragout";
import { formatBpmRange, formatKeyValue, parseBpmRange, parseKeyValue } from "./facets";
import { formatBpm, formatLength, leaf, plural, rowTags, unknownMark } from "./format";
import { decodeMini, miniBars } from "./mini";
import { navTarget, rangeBounds, scrollToReveal, visibleRange } from "./selection";
import { ancestors, buildTagTree, visibleTags } from "./tagtree";

describe("navTarget", () => {
  it.each([
    ["down", 3, 10, 5, 4],
    ["down", 9, 10, 5, 9],
    ["up", 0, 10, 5, 0],
    ["up", 4, 10, 5, 3],
    ["down", -1, 10, 5, 0],
    ["up", -1, 10, 5, 0],
    ["pageDown", 2, 100, 20, 21],
    ["pageDown", 95, 100, 20, 99],
    ["pageUp", 30, 100, 20, 11],
    ["pageUp", 5, 100, 20, 0],
    ["pageDown", 0, 100, 1, 1],
    ["home", 50, 100, 20, 0],
    ["end", 50, 100, 20, 99],
    ["end", -1, 100, 20, 99],
    ["down", 0, 0, 20, -1],
    ["end", 0, 0, 20, -1],
  ] as const)("%s from %i of %i (page %i) -> %i", (key, focus, total, page, expected) => {
    expect(navTarget(key, focus, total, page)).toBe(expected);
  });
});

describe("rangeBounds", () => {
  it("orders the two ends", () => {
    expect(rangeBounds(3, 8)).toEqual([3, 8]);
    expect(rangeBounds(8, 3)).toEqual([3, 8]);
    expect(rangeBounds(4, 4)).toEqual([4, 4]);
  });
});

describe("visibleRange", () => {
  it.each([
    [0, 640, 32, 50000, 12, 0, 31],
    [3200, 640, 32, 50000, 12, 88, 131],
    [3210, 640, 32, 50000, 12, 88, 132],
    [1599360, 640, 32, 50000, 12, 49968, 49999],
    [0, 640, 32, 5, 12, 0, 4],
    [0, 640, 32, 0, 12, 0, -1],
    [0, 0, 32, 100, 12, 0, 11],
  ])("scrollTop %i, viewport %i, row %i, total %i", (scrollTop, viewport, row, total, overscan, first, last) => {
    expect(visibleRange(scrollTop, viewport, row, total, overscan)).toEqual({ first, last });
  });

  it("accounts for the padding above the first row", () => {
    expect(visibleRange(36, 64, 32, 100, 0, 4)).toEqual({ first: 1, last: 2 });
  });
});

describe("scrollToReveal", () => {
  it.each([
    [5, 0, 320, 32, null],
    [9, 0, 320, 32, null],
    [10, 0, 320, 32, 32],
    [2, 100, 320, 32, 64],
    [3, 100, 320, 32, 96],
    [4, 100, 320, 32, null],
  ])("row %i at scrollTop %i", (index, scrollTop, viewport, row, expected) => {
    expect(scrollToReveal(index, scrollTop, viewport, row)).toBe(expected);
  });

  it("keeps the padding above the first and below the last row in view", () => {
    expect(scrollToReveal(0, 40, 320, 32, 4)).toBe(0);
    expect(scrollToReveal(10, 0, 320, 32, 4)).toBe(4 + 11 * 32 + 4 - 320);
    expect(scrollToReveal(3, 0, 320, 32, 4)).toBeNull();
  });
});

describe("DragTracker", () => {
  const at = (x: number, y: number, buttons = 1, button = 0) => ({ clientX: x, clientY: y, buttons, button });

  it("starts once after more than 5px of travel with the button held", () => {
    const begin = vi.fn();
    const tracker = new DragTracker<number>(begin);
    tracker.down(at(100, 100), 7);
    expect(tracker.move(at(103, 104))).toBe(false); // exactly 5px
    expect(begin).not.toHaveBeenCalled();
    expect(tracker.move(at(104, 104))).toBe(true);
    expect(begin).toHaveBeenCalledExactlyOnceWith(7);
    expect(tracker.move(at(150, 150))).toBe(false);
    expect(begin).toHaveBeenCalledTimes(1);
    expect(tracker.armed).toBe(false);
  });

  it("ignores other buttons, releases and moves without the button", () => {
    const begin = vi.fn();
    const tracker = new DragTracker<number>(begin);
    tracker.down(at(0, 0, 2, 2), 1);
    tracker.move(at(50, 50, 2));
    tracker.down(at(0, 0), 1);
    tracker.up();
    tracker.move(at(50, 50));
    tracker.down(at(0, 0), 1);
    tracker.move(at(50, 50, 0));
    tracker.move(at(60, 60, 1));
    tracker.down(at(0, 0), 1);
    tracker.move(at(50, 50, 3)); // a second button joined in
    expect(begin).not.toHaveBeenCalled();
  });
});

describe("LostRelease", () => {
  it("normally lets only pointerdown count as the press", () => {
    const lost = new LostRelease();
    expect(lost.role("pointerdown")).toBe("press");
    expect(lost.role("mousedown")).toBeNull();
    expect(lost.role("click")).toBeNull();
  });

  it("takes mousedown as the press when the webview withheld pointerdown (X11)", () => {
    const lost = new LostRelease();
    lost.dragStarted();
    expect(lost.role("mousedown")).toBe("press");
    // The rest of that press, and the next one, are ordinary again.
    expect(lost.role("click")).toBeNull();
    expect(lost.role("pointerdown")).toBe("press");
    expect(lost.role("mousedown")).toBeNull();
  });

  it("takes the click when no down event arrived at all (Wayland)", () => {
    const lost = new LostRelease();
    lost.dragStarted();
    expect(lost.role("click")).toBe("click");
    expect(lost.role("click")).toBeNull();
  });

  it("never handles a press twice when the webview did see the release", () => {
    const lost = new LostRelease();
    lost.dragStarted();
    expect(lost.role("pointerdown")).toBe("press");
    expect(lost.role("mousedown")).toBeNull();
    expect(lost.role("click")).toBeNull();
  });

  it("is settled by the lost press landing somewhere else", () => {
    const lost = new LostRelease();
    lost.dragStarted();
    lost.settle();
    expect(lost.role("mousedown")).toBeNull();
    expect(lost.role("click")).toBeNull();
  });
});

describe("row formatting", () => {
  it.each([
    [124, "124"],
    [127.5, "127.5"],
    [127.96, "128"],
    [89.94, "89.9"],
    [0, ""],
    [-3, ""],
    [Number.NaN, ""],
  ])("formatBpm(%d) = %j", (bpm, expected) => {
    expect(formatBpm(bpm)).toBe(expected);
  });

  it.each([
    [0.412, "0.41s"],
    [0.99, "0.99s"],
    [0.999, "0:01"],
    [7.4, "0:07"],
    [59.6, "1:00"],
    [64, "1:04"],
    [0, "0:00"],
  ])("formatLength(%d) = %j", (seconds, expected) => {
    expect(formatLength(seconds)).toBe(expected);
  });

  it("shows two leaf names and counts the rest", () => {
    expect(rowTags(["drums/kick", "808", "dark", "long"])).toEqual({ shown: ["kick", "808"], more: 2 });
    expect(rowTags([])).toEqual({ shown: [], more: 0 });
    expect(leaf("a/b/c")).toBe("c");
  });

  it("marks unknown values by analysis state", () => {
    expect(unknownMark({ analyzed: false })).toBe("·");
    expect(unknownMark({ analyzed: true })).toBe("–");
  });

  it("pluralises", () => {
    expect(plural(1, "sample")).toBe("1 sample");
    expect(plural(3, "sample")).toBe("3 samples");
  });
});

describe("mini waveform", () => {
  it("decodes base64 and tolerates rubbish", () => {
    expect([...decodeMini(btoa(String.fromCharCode(0, 128, 255)))]).toEqual([0, 128, 255]);
    expect(decodeMini("")).toHaveLength(0);
    expect(decodeMini("%%%")).toHaveLength(0);
  });

  it("takes the peak of each group of buckets", () => {
    const peaks = new Uint8Array(96);
    peaks[0] = 51;
    peaks[2] = 255;
    peaks[95] = 102;
    const bars = miniBars(peaks);
    expect(bars).toHaveLength(32);
    expect(bars[0]).toBe(1);
    expect(bars[1]).toBe(0);
    expect(bars[31]).toBeCloseTo(0.4);
    expect(miniBars(new Uint8Array(0))).toEqual([]);
    expect(miniBars(new Uint8Array([255, 0]), 4)).toEqual([1, 1, 0, 0]);
  });
});

describe("tag tree", () => {
  const tags = [
    { tag: "drums/kick", count: 20, color: "#e66" },
    { tag: "drums", count: 50, color: "" },
    { tag: "drums/snare", count: 10, color: "" },
    { tag: "fx/riser/long", count: 4, color: "" },
    { tag: "fx/impact", count: 3, color: "" },
    { tag: "Bass", count: 30, color: "" },
  ];

  it("nests, sorts and fills in unlisted parents", () => {
    const tree = buildTagTree(tags);
    expect(tree.map((n) => n.tag)).toEqual(["Bass", "drums", "fx"]);
    const drums = tree[1];
    expect(drums.count).toBe(50);
    expect(drums.children.map((n) => [n.name, n.depth, n.color])).toEqual([
      ["kick", 1, "#e66"],
      ["snare", 1, ""],
    ]);
    const fx = tree[2];
    expect(fx.count).toBe(7);
    expect(fx.children.map((n) => [n.tag, n.count])).toEqual([
      ["fx/impact", 3],
      ["fx/riser", 4],
    ]);
    expect(fx.children[1].children[0].depth).toBe(2);
  });

  it("lists only expanded branches", () => {
    const tree = buildTagTree(tags);
    expect(visibleTags(tree, new Set()).map((n) => n.tag)).toEqual(["Bass", "drums", "fx"]);
    expect(visibleTags(tree, new Set(["fx", "fx/riser"])).map((n) => n.tag)).toEqual([
      "Bass",
      "drums",
      "fx",
      "fx/impact",
      "fx/riser",
      "fx/riser/long",
    ]);
    // A collapsed parent hides an expanded child.
    expect(visibleTags(tree, new Set(["fx/riser"])).map((n) => n.tag)).toEqual(["Bass", "drums", "fx"]);
  });

  it("names ancestors", () => {
    expect(ancestors("a/b/c")).toEqual(["a", "a/b"]);
    expect(ancestors("a")).toEqual([]);
  });
});

describe("facet values", () => {
  it.each([
    ["120-128", { min: 120, max: 128 }],
    ["128-120", { min: 120, max: 128 }],
    [">100", { min: 100, max: null }],
    ["<100.5", { min: null, max: 100.5 }],
    ["124", { min: 124, max: 124 }],
    ["~124", { min: 124, max: 124 }],
    ["", { min: null, max: null }],
    ["fast", { min: null, max: null }],
  ])("parseBpmRange(%j)", (value, expected) => {
    expect(parseBpmRange(value)).toEqual(expected);
  });

  it.each([
    [{ min: 120, max: 128 }, "120-128"],
    [{ min: 128, max: 120 }, "120-128"],
    [{ min: 124, max: 124 }, "124"],
    [{ min: 100, max: null }, ">100"],
    [{ min: null, max: 100 }, "<100"],
    [{ min: null, max: null }, null],
    [{ min: 0, max: Number.NaN }, null],
  ])("formatBpmRange(%j) = %j", (range, expected) => {
    expect(formatBpmRange(range)).toBe(expected);
  });

  it("splits and joins the compatible key form", () => {
    expect(parseKeyValue("compatible:Am")).toEqual({ key: "Am", compatible: true });
    expect(parseKeyValue("F#")).toEqual({ key: "F#", compatible: false });
    expect(formatKeyValue("Am", true)).toBe("compatible:Am");
    expect(formatKeyValue("Am", false)).toBe("Am");
  });
});
