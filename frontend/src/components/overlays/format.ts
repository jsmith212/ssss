// Small text helpers shared by the overlays.

import type { MetaSource, Sample } from "../../lib/types";

export { baseName, formatBpm, normaliseTag, plural } from "../../lib/format";

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value >= 100 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`;
}

/** "0:07", "3:25"; sub-second lengths keep their milliseconds ("412 ms"). */
export function formatDuration(seconds: number): string {
  if (seconds > 0 && seconds < 1) return `${Math.round(seconds * 1000)} ms`;
  const whole = Math.round(seconds);
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
}

export function formatRate(hz: number): string {
  if (!hz) return "";
  const khz = hz / 1000;
  return `${Number.isInteger(khz) ? khz : khz.toFixed(1)} kHz`;
}

export function formatChannels(ch: number): string {
  return ch === 1 ? "Mono" : ch === 2 ? "Stereo" : ch > 0 ? `${ch} ch` : "";
}

/** "WAV · 44.1 kHz · Stereo · 0:07 · 178 KB" */
export function formatSummary(s: Pick<Sample, "ext" | "sr" | "ch" | "dur" | "size">): string {
  return [s.ext.toUpperCase(), formatRate(s.sr), formatChannels(s.ch), formatDuration(s.dur), formatBytes(s.size)]
    .filter(Boolean)
    .join(" · ");
}

const SOURCE_LABELS: Record<MetaSource, string> = {
  user: "set by you",
  splice: "from Splice",
  filename: "from the file name",
  detected: "detected",
  "": "",
};

export function sourceLabel(source: MetaSource): string {
  return SOURCE_LABELS[source] ?? "";
}

const FLATS: Record<string, string> = { Db: "C#", Eb: "D#", Gb: "F#", Ab: "G#", Bb: "A#", Cb: "B", Fb: "E" };
const SHARP_FIXES: Record<string, string> = { "E#": "F", "B#": "C" };

/**
 * Normalises a typed key to the library notation (sharps only, "Am", "F#").
 * Returns "" for empty input and null when the text is not a key.
 */
export function normaliseKey(input: string): string | null {
  const text = input.trim();
  if (text === "") return "";
  const m = /^([A-Ga-g])\s*([#♯b♭]?)\s*(m|min|minor|Min|Minor|maj|major|Maj|Major|M)?$/.exec(text);
  if (!m) return null;
  const quality = m[3] ?? "";
  // "M" alone is major; any other spelling starting with "m" but "maj" is minor.
  const minor = quality !== "M" && /^m(?!aj)/i.test(quality);
  const accidental = m[2] === "♯" ? "#" : m[2] === "♭" ? "b" : m[2];
  let note = m[1].toUpperCase() + accidental;
  note = FLATS[note] ?? SHARP_FIXES[note] ?? note;
  return note + (minor ? "m" : "");
}

/** Parses a typed BPM; 0 for empty (unknown), null when it is not a tempo. */
export function parseBpm(input: string): number | null {
  const text = input.trim().replace(",", ".");
  if (text === "") return 0;
  if (!/^\d+(\.\d+)?$/.test(text)) return null;
  const bpm = Number(text);
  return bpm >= 20 && bpm <= 999 ? Math.round(bpm * 100) / 100 : null;
}
