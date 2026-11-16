// How row values read.

import { leaf } from "../format";
import type { Sample } from "../types";

export { formatBpm, leaf, plural } from "../format";

/** "0.41s" under a second (one-shots), otherwise m:ss. */
export function formatLength(seconds: number): string {
  if (!(seconds > 0)) return "0:00";
  if (seconds < 0.995) return `${seconds.toFixed(2)}s`;
  const whole = Math.round(seconds);
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
}

/** The chips a row shows: up to `max` leaf names and how many tags remain. */
export function rowTags(tags: readonly string[], max = 2): { shown: string[]; more: number } {
  return { shown: tags.slice(0, max).map(leaf), more: Math.max(0, tags.length - max) };
}

/** Placeholder for an unknown BPM/key: "·" while analysis is pending, "–" after. */
export function unknownMark(sample: Pick<Sample, "analyzed">): string {
  return sample.analyzed ? "–" : "·";
}
