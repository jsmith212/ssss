import { describe, expect, it } from "vitest";
import { compatibleKeys, keyRank, normaliseKey } from "./keys";

describe("normaliseKey", () => {
  it.each([
    ["Am", "Am"],
    ["am", "Am"],
    ["a minor", "Am"],
    ["Amin", "Am"],
    ["A", "A"],
    ["Amaj", "A"],
    ["AM", "Am"], // case-insensitive, as in Go: a lone m is minor
    ["Bb", "A#"],
    ["bbm", "A#m"],
    ["Db major", "C#"],
    ["F#m", "F#m"],
    ["Cb", "B"],
    ["E#", "F"],
    ["b", "B"],
    ["H", ""],
    ["Amix", ""],
    ["", ""],
  ])("%s -> %s", (input, want) => {
    expect(normaliseKey(input)).toBe(want);
  });
});

describe("compatibleKeys", () => {
  it.each([
    ["Am", ["Am", "C", "Em", "Dm"]],
    ["C", ["C", "Am", "G", "F"]],
    ["F#m", ["F#m", "A", "C#m", "Bm"]],
    ["nope", []],
  ])("%s", (input, want) => {
    expect(compatibleKeys(input)).toEqual(want);
  });
});

describe("keyRank", () => {
  it("orders by pitch, major before minor, unknown last", () => {
    const keys = ["", "Am", "C", "Cm", "A"];
    expect([...keys].sort((a, b) => keyRank(a) - keyRank(b) || 0)).toEqual(["C", "Cm", "A", "Am", ""]);
  });
});
