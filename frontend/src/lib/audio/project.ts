// Parsing of the project pill's free-form input: "124", "am", "124 am",
// "128 F# minor", "124 -" (clears the key).

import { formatKey, parseKey } from "./keys";

export const MIN_PROJECT_BPM = 20;
export const MAX_PROJECT_BPM = 400;

export interface ProjectPatch {
  projectBpm?: number;
  projectKey?: string;
}

export type ProjectParse = { ok: true; patch: ProjectPatch } | { ok: false; error: string };

const CLEAR_KEY = /^(-|–|—|none|off|no key)$/i;

/**
 * Reads a tempo, a key, or both in either order. Parts that are left out
 * stay unchanged, so typing just "128" keeps the key.
 */
export function parseProject(input: string): ProjectParse {
  const text = input
    .replace(/(\d),(\d)/g, "$1.$2")
    .replace(/[·,/]+/g, " ")
    .replace(/(\d)\s*bpm\b/gi, "$1 ")
    .trim();
  if (text === "") return { ok: true, patch: {} };

  const patch: ProjectPatch = {};
  let rest = text;
  const number = /(?:^|\s)(\d+(?:\.\d+)?)(?=\s|$)/.exec(rest);
  if (number) {
    const bpm = Number(number[1]);
    if (bpm < MIN_PROJECT_BPM || bpm > MAX_PROJECT_BPM) {
      return { ok: false, error: `Tempo must be ${MIN_PROJECT_BPM}–${MAX_PROJECT_BPM} BPM` };
    }
    patch.projectBpm = Math.round(bpm * 100) / 100;
    rest = (rest.slice(0, number.index) + " " + rest.slice(number.index + number[0].length)).trim();
  }

  if (rest !== "") {
    if (CLEAR_KEY.test(rest)) {
      patch.projectKey = "";
    } else {
      const key = parseKey(rest.replace(/\s+/g, " "));
      if (!key) return { ok: false, error: `“${rest}” is not a tempo or key` };
      patch.projectKey = formatKey(key);
    }
  }
  return { ok: true, patch };
}

/** The pill's text: "124 · Am", or just "124" without a key. */
export function formatProject(bpm: number, key: string, separator = " · "): string {
  const tempo = String(Math.round(bpm * 100) / 100);
  return key ? `${tempo}${separator}${key}` : tempo;
}

/** Project BPM after a nudge, kept in range. */
export function nudgeBpm(bpm: number, delta: number): number {
  const next = Math.round((bpm + delta) * 100) / 100;
  return Math.min(MAX_PROJECT_BPM, Math.max(MIN_PROJECT_BPM, next));
}
