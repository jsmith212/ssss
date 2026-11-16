// Text helpers shared by the shell, the player and the overlays.

/** "1 sample", "3 samples", "1,204 samples". */
export function plural(n: number, one: string, many = `${one}s`): string {
  return `${n.toLocaleString()} ${n === 1 ? one : many}`;
}

/** Integer unless the tempo is fractional: 124, 127.5; "" when unknown. */
export function formatBpm(bpm: number): string {
  if (!(bpm > 0)) return "";
  const rounded = Math.round(bpm * 10) / 10;
  return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
}

/** Last path segment of a tag: "drums/kick" -> "kick". */
export function leaf(tag: string): string {
  return tag.slice(tag.lastIndexOf("/") + 1);
}

/**
 * A typed tag path as the library stores it: lower case, segments trimmed,
 * empty segments dropped ("Drums / Kick/" -> "drums/kick"); "" when nothing
 * is left. Mirrors library.NormalizeTag, so what the UI shows as pending is
 * what the backend reports back.
 */
export function normaliseTag(input: string): string {
  return input
    .normalize("NFC")
    .toLowerCase()
    .split("/")
    .map((part) => part.trim().replace(/\s+/g, " ").replace(/^#+/, ""))
    .filter(Boolean)
    .join("/");
}

/** Last path component, for either separator. */
export function baseName(path: string): string {
  return path.replace(/[\\/]+$/, "").split(/[\\/]/).pop() ?? path;
}
