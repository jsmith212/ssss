import { describe, expect, it } from "vitest";
import { barCount, barsFromMini, barsFromPeaks, beatTicks } from "./waveform";

describe("barCount", () => {
  it.each([
    [0, 0],
    [1, 0],
    [2, 1],
    [4, 1],
    [5, 2],
    [300, 100],
    [299, 100],
    [298, 99],
  ])("%d px -> %d bars", (width, want) => {
    expect(barCount(width)).toBe(want);
  });
});

describe("barsFromPeaks", () => {
  it("takes the larger side of each min/max pair", () => {
    const peaks = new Int8Array([-127, 10, -5, 64, 0, 0, -32, 16]);
    expect([...barsFromPeaks(peaks, 4)].map((v) => Number(v.toFixed(3)))).toEqual([1, 0.504, 0, 0.252]);
  });

  it("keeps the peak when reducing", () => {
    const peaks = new Int8Array(2000 * 2);
    peaks[2 * 1234 + 1] = 127;
    const bars = barsFromPeaks(peaks, 100);
    expect(bars[61]).toBe(1);
    expect(bars.reduce((a, b) => a + b, 0)).toBe(1);
  });

  it("stretches when there are more bars than buckets", () => {
    const peaks = new Int8Array([0, 127, 0, 0]);
    expect([...barsFromPeaks(peaks, 4)]).toEqual([1, 1, 0, 0]);
  });

  it("handles no peaks", () => {
    expect([...barsFromPeaks(new Int8Array(0), 3)]).toEqual([0, 0, 0]);
  });
});

describe("barsFromMini", () => {
  it("decodes base64 buckets", () => {
    const mini = btoa(String.fromCharCode(255, 0, 51, 102));
    expect([...barsFromMini(mini, 2)].map((v) => Number(v.toFixed(2)))).toEqual([1, 0.4]);
  });

  it("draws flat for empty or malformed input", () => {
    expect([...barsFromMini("", 2)]).toEqual([0, 0]);
    expect([...barsFromMini("%%%", 2)]).toEqual([0, 0]);
  });
});

describe("beatTicks", () => {
  it("marks beats, bar starts strong", () => {
    // 2 bars at 120 BPM = 4 s.
    const ticks = beatTicks(120, 4, 800);
    expect(ticks).toHaveLength(7);
    expect(ticks[0]).toEqual({ x: 0.125, strong: false });
    expect(ticks[3]).toEqual({ x: 0.5, strong: true });
    expect(ticks.filter((t) => t.strong)).toHaveLength(1);
  });

  it("falls back to bars when beats are too dense", () => {
    // 16 bars in 400 px: beats 6.25 px apart at 120 BPM over 32 s.
    const ticks = beatTicks(120, 32, 300);
    expect(ticks.every((t) => t.strong)).toBe(true);
    expect(ticks).toHaveLength(15);
  });

  it("gives up when even bars are too dense", () => {
    expect(beatTicks(120, 600, 300)).toEqual([]);
  });

  it("has nothing to mark without a tempo", () => {
    expect(beatTicks(0, 4, 800)).toEqual([]);
    expect(beatTicks(120, 0, 800)).toEqual([]);
  });

  it("does not mark the very end", () => {
    const ticks = beatTicks(120, 2, 800);
    expect(ticks.map((t) => t.x)).toEqual([0.25, 0.5, 0.75]);
  });
});
