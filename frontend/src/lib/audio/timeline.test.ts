import { describe, expect, it } from "vitest";
import { endTime, positionAt, wrap, type Timeline } from "./timeline";

const base: Timeline = { at: 10, input: 0, rate: 1, loop: null, duration: 2 };

describe("positionAt", () => {
  it("advances linearly and stops at the end", () => {
    expect(positionAt(base, 10)).toBe(0);
    expect(positionAt(base, 10.5)).toBeCloseTo(0.5);
    expect(positionAt(base, 99)).toBe(2);
  });

  it("holds before the start time", () => {
    expect(positionAt({ ...base, input: 0.7 }, 9.2)).toBe(0.7);
  });

  it("scales by rate", () => {
    expect(positionAt({ ...base, rate: 1.25 }, 11)).toBeCloseTo(1.25);
    expect(positionAt({ ...base, rate: 0.5, input: 1 }, 11)).toBeCloseTo(1.5);
  });

  it("wraps a whole-sample loop", () => {
    const t = { ...base, loop: { start: 0, end: 2 } };
    expect(positionAt(t, 11.5)).toBeCloseTo(1.5);
    expect(positionAt(t, 12.25)).toBeCloseTo(0.25);
    expect(positionAt(t, 10 + 2 * 50 + 0.5)).toBeCloseTo(0.5);
  });

  it("plays the lead-in once, then stays inside the loop region", () => {
    const t = { ...base, loop: { start: 0.5, end: 1.5 } };
    expect(positionAt(t, 10.25)).toBeCloseTo(0.25);
    expect(positionAt(t, 11.75)).toBeCloseTo(0.75);
    expect(positionAt(t, 13.6)).toBeCloseTo(0.6);
  });
});

describe("wrap", () => {
  it("ignores empty loops", () => {
    expect(wrap(3, { start: 1, end: 1 })).toBe(3);
    expect(wrap(3, null)).toBe(3);
  });
});

describe("endTime", () => {
  it("is when the sample runs out", () => {
    expect(endTime(base)).toBe(12);
    expect(endTime({ ...base, input: 1.5, rate: 0.5 })).toBe(11);
  });

  it("is never while looping", () => {
    expect(endTime({ ...base, loop: { start: 0, end: 2 } })).toBe(Number.POSITIVE_INFINITY);
  });

  it("is finite once the playhead is past the loop", () => {
    expect(endTime({ ...base, input: 1.8, loop: { start: 0.5, end: 1.5 } })).toBeCloseTo(10.2);
  });
});
