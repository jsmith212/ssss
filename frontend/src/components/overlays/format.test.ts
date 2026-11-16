import { describe, expect, it } from "vitest";
import {
  baseName,
  formatBpm,
  formatBytes,
  formatDuration,
  formatRate,
  formatSummary,
  normaliseKey,
  normaliseTag,
  parseBpm,
} from "./format";

describe("format", () => {
  it.each([
    [512, "512 B"],
    [182044, "178 KB"],
    [1536, "1.5 KB"],
    [5 * 1024 * 1024, "5.0 MB"],
  ])("formatBytes(%d)", (bytes, want) => expect(formatBytes(bytes)).toBe(want));

  it.each([
    [0, "0:00"],
    [0.412, "412 ms"],
    [7.4, "0:07"],
    [205, "3:25"],
  ])("formatDuration(%d)", (s, want) => expect(formatDuration(s)).toBe(want));

  it.each([
    [44100, "44.1 kHz"],
    [48000, "48 kHz"],
    [0, ""],
  ])("formatRate(%d)", (hz, want) => expect(formatRate(hz)).toBe(want));

  it("formatSummary skips unknown parts", () => {
    expect(formatSummary({ ext: "wav", sr: 44100, ch: 2, dur: 7, size: 182044 })).toBe(
      "WAV · 44.1 kHz · Stereo · 0:07 · 178 KB",
    );
    expect(formatSummary({ ext: "mp3", sr: 0, ch: 0, dur: 61, size: 100 })).toBe("MP3 · 1:01 · 100 B");
  });

  it.each([
    [0, ""],
    [124, "124"],
    [127.94, "127.9"],
  ])("formatBpm(%d)", (bpm, want) => expect(formatBpm(bpm)).toBe(want));
});

describe("normaliseKey", () => {
  it.each([
    ["", ""],
    ["  ", ""],
    ["am", "Am"],
    ["A min", "Am"],
    ["a minor", "Am"],
    ["F#", "F#"],
    ["f♯m", "F#m"],
    ["Bb", "A#"],
    ["bbm", "A#m"],
    ["Eb maj", "D#"],
    ["C major", "C"],
    ["CM", "C"],
    ["Cb", "B"],
    ["E#m", "Fm"],
  ])("%j -> %j", (input, want) => expect(normaliseKey(input)).toBe(want));

  it.each(["H", "Am7", "124", "A##"])("rejects %j", (input) => expect(normaliseKey(input)).toBeNull());
});

describe("parseBpm", () => {
  it.each([
    ["", 0],
    ["124", 124],
    [" 127,5 ", 127.5],
    ["89.999", 90],
  ])("%j -> %d", (input, want) => expect(parseBpm(input)).toBe(want));

  it.each(["abc", "-3", "5", "1200", "12e1"])("rejects %j", (input) => expect(parseBpm(input)).toBeNull());
});

describe("tags and paths", () => {
  it.each([
    ["drums/kick", "drums/kick"],
    [" Drums / Kick/ ", "drums/kick"],
    ["#Kick", "kick"],
    ["//", ""],
    ["lo   fi", "lo fi"],
  ])("normaliseTag(%j)", (input, want) => expect(normaliseTag(input)).toBe(want));

  it.each([
    ["/home/you/Music/Samples", "Samples"],
    ["C:\\Users\\you\\Samples\\", "Samples"],
    ["Samples", "Samples"],
  ])("baseName(%j)", (path, want) => expect(baseName(path)).toBe(want));
});
