// Translation between facet controls and query token values.

export interface BpmRange {
  min: number | null;
  max: number | null;
}

const NUM = String.raw`(\d+(?:\.\d+)?)`;

/** Reads a `bpm:` token value into the range the two facet inputs show. */
export function parseBpmRange(value: string): BpmRange {
  let m: RegExpExecArray | null;
  if ((m = new RegExp(`^${NUM}-${NUM}$`).exec(value))) {
    const a = Number(m[1]);
    const b = Number(m[2]);
    return { min: Math.min(a, b), max: Math.max(a, b) };
  }
  if ((m = new RegExp(`^>=?${NUM}$`).exec(value))) return { min: Number(m[1]), max: null };
  if ((m = new RegExp(`^<=?${NUM}$`).exec(value))) return { min: null, max: Number(m[1]) };
  if ((m = new RegExp(`^~?${NUM}$`).exec(value))) return { min: Number(m[1]), max: Number(m[1]) };
  return { min: null, max: null };
}

/** The `bpm:` value for a range; null when both ends are open. */
export function formatBpmRange(range: BpmRange): string | null {
  const { min, max } = range;
  const valid = (n: number | null): n is number => n !== null && Number.isFinite(n) && n > 0;
  if (valid(min) && valid(max)) return min === max ? String(min) : `${Math.min(min, max)}-${Math.max(min, max)}`;
  if (valid(min)) return `>${min}`;
  if (valid(max)) return `<${max}`;
  return null;
}

export interface LengthPreset {
  label: string;
  /** `len:` token value. */
  value: string;
}

export const LENGTH_PRESETS: readonly LengthPreset[] = [
  { label: "< 1s", value: "<1s" },
  { label: "1–4s", value: "1s-4s" },
  { label: "4–16s", value: "4s-16s" },
  { label: "> 16s", value: ">16s" },
];

const COMPATIBLE = "compatible:";

/** Splits a `key:` value into the key and whether it is the compatible form. */
export function parseKeyValue(value: string): { key: string; compatible: boolean } {
  const compatible = value.toLowerCase().startsWith(COMPATIBLE);
  return { key: compatible ? value.slice(COMPATIBLE.length) : value, compatible };
}

export function formatKeyValue(key: string, compatible: boolean): string {
  return compatible ? COMPATIBLE + key : key;
}
