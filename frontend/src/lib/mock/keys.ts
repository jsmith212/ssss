// Musical key helpers for the mock backend, on top of the app's one key
// parser. Notation: sharps only, major = "C", minor = "Cm" (same as the Go
// side).

import { formatKey, parseKey, type Key } from "../audio/keys";

export { formatKey, parseKey };
export type ParsedKey = Key;

/** Canonical spelling, or "" when the input is not a key. */
export function normaliseKey(input: string): string {
  const k = parseKey(input);
  return k ? formatKey(k) : "";
}

/** Same key, relative major/minor, and one step either way on the circle of fifths. */
export function compatibleKeys(input: string): string[] {
  const k = parseKey(input);
  if (!k) return [];
  const relative: ParsedKey = { pc: (k.pc + (k.minor ? 3 : 9)) % 12, minor: !k.minor };
  return [
    k,
    relative,
    { pc: (k.pc + 7) % 12, minor: k.minor },
    { pc: (k.pc + 5) % 12, minor: k.minor },
  ].map(formatKey);
}

/** Sort rank: by pitch class, major before minor; unknown keys sort last. */
export function keyRank(key: string): number {
  const k = parseKey(key);
  return k ? k.pc * 2 + (k.minor ? 1 : 0) : Number.POSITIVE_INFINITY;
}

/** Frequency in Hz of the key's root in the given octave (A4 = 440). */
export function rootFrequency(key: string, octave: number): number {
  const k = parseKey(key);
  const pc = k ? k.pc : 0;
  return 440 * Math.pow(2, (pc - 9) / 12 + (octave - 4));
}
