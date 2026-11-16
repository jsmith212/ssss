import { describe, expect, it } from "vitest";
import { formatProject, nudgeBpm, parseProject } from "./project";

describe("parseProject", () => {
  it.each([
    ["124 am", { projectBpm: 124, projectKey: "Am" }],
    ["am 124", { projectBpm: 124, projectKey: "Am" }],
    ["124", { projectBpm: 124 }],
    ["  f#  ", { projectKey: "F#" }],
    ["128 F# minor", { projectBpm: 128, projectKey: "F#m" }],
    ["Bb major 90", { projectBpm: 90, projectKey: "A#" }],
    ["124 · Am", { projectBpm: 124, projectKey: "Am" }],
    ["87.5 dm", { projectBpm: 87.5, projectKey: "Dm" }],
    ["87,5", { projectBpm: 87.5 }],
    ["140bpm", { projectBpm: 140 }],
    ["140 bpm g", { projectBpm: 140, projectKey: "G" }],
    ["124 -", { projectBpm: 124, projectKey: "" }],
    ["none", { projectKey: "" }],
    ["", {}],
  ])("%j", (input, want) => {
    const got = parseProject(input);
    if (want === null) expect(got.ok).toBe(false);
    else expect(got).toEqual({ ok: true, patch: want });
  });

  it.each(["5", "999", "124 h", "hello", "124 130", "am 7"])("rejects %j", (input) => {
    const got = parseProject(input);
    expect(got.ok).toBe(false);
    if (!got.ok) expect(got.error).not.toBe("");
  });
});

describe("formatProject", () => {
  it("joins tempo and key", () => {
    expect(formatProject(124, "Am")).toBe("124 · Am");
    expect(formatProject(124, "")).toBe("124");
    expect(formatProject(87.5, "F#", " ")).toBe("87.5 F#");
  });
});

describe("nudgeBpm", () => {
  it("steps and clamps", () => {
    expect(nudgeBpm(124, 1)).toBe(125);
    expect(nudgeBpm(124.5, -1)).toBe(123.5);
    expect(nudgeBpm(20, -1)).toBe(20);
    expect(nudgeBpm(400, 1)).toBe(400);
  });
});
