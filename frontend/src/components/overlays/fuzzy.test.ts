import { describe, expect, it } from "vitest";
import { fuzzyMatch, rank, segments, splitSegments } from "./fuzzy";

describe("fuzzyMatch", () => {
  it.each([
    ["", "anything", []],
    ["kick", "drums/kick", [6, 7, 8, 9]],
    ["KICK", "drums/kick", [6, 7, 8, 9]],
    ["dk", "drums/kick", [0, 6]],
    ["dr ki", "drums/kick", [0, 1, 6, 7]],
  ])("matches %j in %j", (query, text, positions) => {
    expect(fuzzyMatch(query, text)?.positions).toEqual(positions);
  });

  it.each([
    ["kcik", "kick"],
    ["snare", "drums/kick"],
    ["kick x", "drums/kick"],
  ])("rejects %j in %j", (query, text) => {
    expect(fuzzyMatch(query, text)).toBeNull();
  });

  it("prefers a word-start substring to an earlier mid-word one", () => {
    expect(fuzzyMatch("at", "bat/atmos")?.positions).toEqual([4, 5]);
  });
});

describe("rank", () => {
  const names = (query: string, items: string[]) => rank(query, items, (s) => s).map((r) => r.item);

  it("orders prefix, then word start, then substring, then subsequence", () => {
    expect(names("ba", ["tuba", "drums/bass", "bass", "bright/pad"])).toEqual([
      "bass",
      "drums/bass",
      "tuba",
      "bright/pad",
    ]);
  });

  it("keeps input order for an empty query and for ties", () => {
    expect(names("", ["b", "a", "c"])).toEqual(["b", "a", "c"]);
    expect(names("x", ["xa", "xb"])).toEqual(["xa", "xb"]);
  });

  it("prefers the shorter of two equal matches", () => {
    expect(names("kick", ["kick/808/long/sub", "kick"])).toEqual(["kick", "kick/808/long/sub"]);
  });

  it("applies the limit and the bonus", () => {
    const items = ["open settings", "settings/tag"];
    expect(rank("settings", items, (s) => s, { limit: 1, bonus: (s) => (s.startsWith("open") ? 100 : 0) })[0].item).toBe(
      "open settings",
    );
  });
});

describe("segments", () => {
  it.each([
    ["kick", [], [{ text: "kick", hit: false }]],
    ["", [], []],
    [
      "drums/kick",
      [0, 6, 7],
      [
        { text: "d", hit: true },
        { text: "rums/", hit: false },
        { text: "ki", hit: true },
        { text: "ck", hit: false },
      ],
    ],
  ])("splits %j at %j", (text, positions, want) => {
    expect(segments(text, positions)).toEqual(want);
  });
});

describe("splitSegments", () => {
  it("cuts a highlighted label in two, through a run if need be", () => {
    // "dr" + "ums/k" + "ick", hits on the outer runs; cut before "/kick".
    const parts = [
      { text: "dr", hit: true },
      { text: "ums/k", hit: false },
      { text: "ick", hit: true },
    ];
    expect(splitSegments(parts, 5)).toEqual([
      [
        { text: "dr", hit: true },
        { text: "ums", hit: false },
      ],
      [
        { text: "/k", hit: false },
        { text: "ick", hit: true },
      ],
    ]);
  });

  it("puts everything on one side at the ends", () => {
    const parts = [{ text: "kick", hit: false }];
    expect(splitSegments(parts, 0)).toEqual([[], parts]);
    expect(splitSegments(parts, 4)).toEqual([parts, []]);
  });
});
