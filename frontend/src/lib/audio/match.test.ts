import { describe, expect, it } from "vitest";
import { formatKey, parseKey, relativeKey } from "./keys";
import { computeMatch, formatBpm, formatSemitones, isAltered, keyShift, tempoRate } from "./match";

describe("parseKey", () => {
  it.each([
    ["Am", "Am"],
    ["a minor", "Am"],
    ["AM", "Am"],
    ["C", "C"],
    ["cmaj", "C"],
    ["Bb", "A#"],
    ["bbm", "A#m"],
    ["Db min", "C#m"],
    ["F#", "F#"],
    ["Cb", "B"],
    ["e♭ major", "D#"],
  ])("%s -> %s", (input, want) => {
    expect(formatKey(parseKey(input)!)).toBe(want);
  });

  it.each(["", "H", "124", "Am7", "A dorian"])("rejects %j", (input) => {
    expect(parseKey(input)).toBeNull();
  });

  it("relative keys", () => {
    expect(formatKey(relativeKey(parseKey("Am")!))).toBe("C");
    expect(formatKey(relativeKey(parseKey("C")!))).toBe("Am");
    expect(formatKey(relativeKey(parseKey("F#m")!))).toBe("A");
  });
});

describe("tempoRate", () => {
  it.each([
    [120, 120, 1],
    [100, 124, 1.24],
    [124, 100, 100 / 124],
    [70, 140, 1], // half time
    [174, 87, 1], // double time
    [80, 124, 0.775], // 1.55 folds down
    [100, 150, 1.5], // upper bound is inclusive
    [150, 100, 100 / 150 * 2], // 0.667 is just under 0.67, folds up
    [60, 174, 1.45],
    [200, 60, 1.2],
  ])("%d in a %d project -> %f", (bpm, project, want) => {
    expect(tempoRate(bpm, project)).toBeCloseTo(want, 6);
  });

  it("always lands in range", () => {
    for (let bpm = 40; bpm <= 240; bpm += 7) {
      for (let project = 40; project <= 240; project += 11) {
        const r = tempoRate(bpm, project);
        expect(r).toBeGreaterThanOrEqual(0.67);
        expect(r).toBeLessThanOrEqual(1.5);
      }
    }
  });

  it("is 1 for unknown tempos", () => {
    expect(tempoRate(0, 120)).toBe(1);
    expect(tempoRate(120, 0)).toBe(1);
    expect(tempoRate(Number.NaN, 120)).toBe(1);
  });
});

describe("keyShift", () => {
  it.each([
    ["Am", "Am", 0],
    ["Am", "Bm", 2],
    ["Am", "Gm", -2],
    ["C", "F#", -6], // tritone goes down
    ["C", "F", 5],
    ["C", "G", -5],
    ["Am", "C", 0], // relative major
    ["C", "Am", 0], // relative minor
    ["Am", "D", 2], // D major's relative is Bm
    ["C", "Em", -5], // Em's relative is G
    ["F#m", "A#", 1], // A# major's relative is Gm
    ["B", "C", 1],
    ["C", "B", -1],
    ["Bbm", "db", 0],
  ])("%s -> %s = %d", (key, project, want) => {
    expect(keyShift(key, project)).toBe(want);
  });

  it("stays within -6..+5 for every pair", () => {
    const keys = ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"].flatMap((n) => [n, n + "m"]);
    for (const a of keys) {
      for (const b of keys) {
        const shift = keyShift(a, b)!;
        expect(shift).toBeGreaterThanOrEqual(-6);
        expect(shift).toBeLessThanOrEqual(5);
      }
    }
  });

  it("is null when a key is unknown", () => {
    expect(keyShift("", "Am")).toBeNull();
    expect(keyShift("Am", "")).toBeNull();
  });
});

describe("computeMatch", () => {
  const project = { matchTempo: true, matchKey: true, projectBpm: 124, projectKey: "Am" };

  it("matches a keyed loop", () => {
    const m = computeMatch({ type: "loop", bpm: 100, key: "Gm" }, project);
    expect(m.rate).toBeCloseTo(1.24);
    expect(m.semitones).toBe(2);
    expect(m.tempo).toBe(true);
    expect(m.key).toBe(true);
    expect(m.effectiveBpm).toBeCloseTo(124);
    expect(m.effectiveKey).toBe("Am");
    expect(isAltered(m)).toBe(true);
  });

  it("keeps a relative-key sample in its own key", () => {
    const m = computeMatch({ type: "loop", bpm: 124, key: "C" }, project);
    expect(m.semitones).toBe(0);
    expect(m.effectiveKey).toBe("C");
    expect(isAltered(m)).toBe(false);
  });

  it("never tempo matches a one-shot, but key matches it", () => {
    const m = computeMatch({ type: "oneshot", bpm: 100, key: "Gm" }, project);
    expect(m.canTempo).toBe(false);
    expect(m.rate).toBe(1);
    expect(m.semitones).toBe(2);
    expect(m.effectiveBpm).toBe(100);
  });

  it("matches a detected one-shot root to the tonic, whatever the project's mode", () => {
    // Analysis writes a one-shot's root note like a major key. Read as A
    // major it would be aimed at the relative major of Am, 3 semitones up.
    const detected = { type: "oneshot", bpm: 0, key: "A", keyRoot: true };
    expect(computeMatch(detected, project).semitones).toBe(0);
    expect(computeMatch(detected, { ...project, projectKey: "Cm" }).semitones).toBe(3);
    expect(computeMatch(detected, { ...project, projectKey: "C" }).semitones).toBe(3);
    expect(computeMatch(detected, { ...project, projectKey: "" }).canKey).toBe(false);
  });

  it("takes a named or user-set key at its word", () => {
    expect(computeMatch({ type: "oneshot", bpm: 0, key: "A", keyRoot: false }, project).semitones).toBe(3);
    expect(computeMatch({ type: "oneshot", bpm: 0, key: "A" }, project).semitones).toBe(3);
    // The flag decides, not the type: a loop on one note has only a root too.
    expect(computeMatch({ type: "loop", bpm: 0, key: "A", keyRoot: true }, project).semitones).toBe(0);
    expect(computeMatch({ type: "loop", bpm: 0, key: "A", keyRoot: false }, project).semitones).toBe(3);
  });

  it("leaves samples without metadata alone", () => {
    const m = computeMatch({ type: "loop", bpm: 0, key: "" }, project);
    expect(m).toMatchObject({ rate: 1, semitones: 0, tempo: false, key: false, effectiveBpm: 0, effectiveKey: "" });
  });

  it("reports what could match while the toggles are off", () => {
    const m = computeMatch({ type: "loop", bpm: 100, key: "Gm" }, { ...project, matchTempo: false, matchKey: false });
    expect(m).toMatchObject({ rate: 1, semitones: 0, tempo: false, key: false, canTempo: true, canKey: true });
    expect(m.effectiveKey).toBe("Gm");
  });

  it("shows half-time tempo for folded ratios", () => {
    const m = computeMatch({ type: "loop", bpm: 70, key: "" }, { ...project, projectBpm: 140 });
    expect(m.rate).toBe(1);
    expect(m.effectiveBpm).toBe(70);
  });

  it("needs a project key to key match", () => {
    const m = computeMatch({ type: "loop", bpm: 124, key: "Gm" }, { ...project, projectKey: "" });
    expect(m.canKey).toBe(false);
    expect(m.semitones).toBe(0);
  });
});

describe("formatting", () => {
  it("semitones", () => {
    expect(formatSemitones(2)).toBe("+2 st");
    expect(formatSemitones(-3)).toBe("−3 st");
    expect(formatSemitones(0)).toBe("0 st");
  });
  it("bpm", () => {
    expect(formatBpm(124)).toBe("124");
    expect(formatBpm(86.52)).toBe("86.5");
    expect(formatBpm(123.96)).toBe("124");
  });
});
