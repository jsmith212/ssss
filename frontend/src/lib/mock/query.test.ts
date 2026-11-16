import { describe, expect, it } from "vitest";
import type { Sample } from "../types";
import { matches, parseDuration, parseQuery, tokenize, type QueryContext } from "./query";

function sample(over: Partial<Sample>): Sample {
  return {
    id: "00", packId: "pk_1", packName: "Deep House Drums", name: "DH_Kick_Deep_01", ext: "wav",
    relPath: "Drums/Kicks/DH_Kick_Deep_01.wav", size: 1, dur: 0.4, sr: 44100, ch: 2, type: "oneshot",
    bpm: 0, bpmSrc: "", key: "", keySrc: "", keyRoot: false, tags: ["drums/kick"], fav: false, added: 0,
    missing: false, analyzed: true, mini: "",
    ...over,
  };
}

const kick = sample({});
const bassLoop = sample({
  id: "01", packId: "pk_2", packName: "Techno Toolkit", name: "TK_124_Am_Bass_Loop_02",
  relPath: "Bass/TK_124_Am_Bass_Loop_02.flac", ext: "flac", type: "loop", bpm: 124, key: "Am",
  tags: ["bass"], dur: 7.74, fav: true,
});
const hat = sample({
  id: "02", name: "Hat_Closed_Tight", relPath: "Drums/Hats/Hat_Closed_Tight.aif", ext: "aif",
  tags: ["drums/hat/closed"], dur: 0.12,
});
const halfTime = sample({
  id: "03", name: "Slow_Break", relPath: "Loops/Slow_Break.wav", type: "loop", bpm: 62, tags: [], dur: 15.5,
  analyzed: false,
});
const all = [kick, bassLoop, hat, halfTime];

describe("tokenize escapes", () => {
  it("groups with quotes and reads escapes inside them", () => {
    expect(tokenize('kick pack:"A B" x')).toEqual(["kick", "pack:A B", "x"]);
    expect(tokenize('tag:"12\\"" pack:"AC\\\\DC live\\\\" a\\b')).toEqual(['tag:12"', "pack:AC\\DC live\\", "a\\b"]);
  });
});

describe("root-only keys", () => {
  it("fit every key on their tonic, as in internal/index", () => {
    const root = sample({ id: "r", key: "A", keySrc: "detected", keyRoot: true });
    const major = sample({ id: "m", key: "A", keySrc: "filename" });
    const hits = (q: string) => [root, major].filter((s) => matches(parseQuery(q, ctx), s)).map((s) => s.id);
    expect(hits("key:A")).toEqual(["r", "m"]);
    expect(hits("key:Am")).toEqual(["r"]);
    expect(hits("key:Cm")).toEqual([]);
    expect(hits("key:compatible:Dm")).toEqual(["r"]);
    expect(hits("-key:Am")).toEqual(["m"]);
  });
});

const ctx: QueryContext = {
  packIds: (v) => new Set(v.toLowerCase().includes("techno") || v === "pk_2" ? ["pk_2"] : []),
  collectionSampleIds: (v) => new Set(v === "Best kicks" ? ["00"] : []),
  recentIds: () => new Set(["02"]),
};

function ids(query: string): string[] {
  const parsed = parseQuery(query, ctx);
  return all.filter((s) => matches(parsed, s)).map((s) => s.id);
}

describe("tokenize", () => {
  it.each([
    ["kick  deep", ["kick", "deep"]],
    ['pack:"KSHMR Vol 4" loop', ["pack:KSHMR Vol 4", "loop"]],
    ['-pack:"a b"', ["-pack:a b"]],
    ['"two words"', ["two words"]],
    ['unterminated "quote here', ["unterminated", "quote here"]],
    ["", []],
    ["   ", []],
  ])("%s", (query, want) => {
    expect(tokenize(query)).toEqual(want);
  });
});

describe("parseDuration", () => {
  it.each([
    ["2s", 2],
    ["250ms", 0.25],
    ["1.5m", 90],
    ["3", 3],
    ["abc", null],
  ])("%s -> %s", (input, want) => {
    expect(parseDuration(input)).toBe(want);
  });
});

describe("query matching", () => {
  it.each([
    ["", ["00", "01", "02", "03"]],
    ["kick", ["00"]],
    ["KICK deep", ["00"]],
    ["-kick", ["01", "02", "03"]],
    ["techno", ["01"]],
    ["tag:drums", ["00", "02"]],
    ["#drums/hat", ["02"]],
    ["#closed", ["02"]],
    ["#kick", ["00"]],
    ["-#drums", ["01", "03"]],
    ["pack:pk_2", ["01"]],
    ['pack:"Techno Toolkit"', ["01"]],
    ["@techno", ["01"]],
    ['in:"Best kicks"', ["00"]],
    ["bpm:124", ["01"]],
    ["bpm:120-128", ["01"]],
    ["bpm:~124", ["01", "03"]],
    ["bpm:>100", ["01"]],
    ["bpm:<100", ["03"]],
    ["key:Am", ["01"]],
    ["key:Bbm", []],
    ["key:a", []],
    ["key:compatible:C", ["01"]],
    ["key:compatible:Em", ["01"]],
    ["key:compatible:F#", []],
    ["type:loop", ["01", "03"]],
    ["type:oneshot", ["00", "02"]],
    ["len:<200ms", ["02"]],
    ["len:>10s", ["03"]],
    ["len:1s-8s", ["01"]],
    ["len:0.3-0.5", ["00"]],
    ["fmt:flac", ["01"]],
    ["fmt:aiff", ["02"]],
    ["is:fav", ["01"]],
    ["is:untagged", ["03"]],
    ["is:recent", ["02"]],
    ["is:unanalyzed", ["03"]],
    ["-is:fav type:loop", ["03"]],
    ["foo:bar", []],
    ["type:loop bass", ["01"]],
  ])("%s", (query, want) => {
    expect(ids(query)).toEqual(want);
  });

  it("reports malformed values and keeps the rest of the query", () => {
    const parsed = parseQuery("bpm:fast kick", ctx);
    expect(parsed.error).toContain("bpm:fast");
    expect(all.filter((s) => matches(parsed, s)).map((s) => s.id)).toEqual(["00"]);
  });

  it("collects positive free-text terms for relevance", () => {
    expect(parseQuery("Kick -snare #drums weird:token", ctx).text).toEqual(["kick", "weird:token"]);
  });

  it.each(['"', "-", "#", "@", "bpm:", ":", "--", '-"', "key:compatible:"])("does not throw on %s", (query) => {
    expect(() => parseQuery(query, ctx)).not.toThrow();
  });
});
