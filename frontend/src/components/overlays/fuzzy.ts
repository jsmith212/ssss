// Fuzzy matching for the tag/collection pickers and the command palette.
// Every whitespace-separated word of the query must match; a contiguous
// substring always beats a scattered subsequence.

export interface FuzzyMatch {
  /** Higher is better. */
  score: number;
  /** Indices into the text of the matched characters, ascending. */
  positions: number[];
}

const BOUNDARY = /[\s/_\-.:]/;

function isBoundary(text: string, index: number): boolean {
  return index === 0 || BOUNDARY.test(text[index - 1]);
}

function matchWord(word: string, text: string, lower: string): FuzzyMatch | null {
  // Prefer a substring that starts a word, then any substring.
  let at = -1;
  for (let from = 0; ; ) {
    const i = lower.indexOf(word, from);
    if (i < 0) break;
    if (at < 0) at = i;
    if (isBoundary(text, i)) {
      at = i;
      break;
    }
    from = i + 1;
  }
  if (at >= 0) {
    const positions = Array.from({ length: word.length }, (_, k) => at + k);
    const score = 100 + word.length * 4 + (at === 0 ? 40 : isBoundary(text, at) ? 25 : 0);
    return { score, positions };
  }

  const positions: number[] = [];
  let score = 0;
  let cursor = 0;
  for (const ch of word) {
    // A boundary hit is worth skipping ahead for; otherwise take the next one.
    let i = -1;
    for (let j = lower.indexOf(ch, cursor); j >= 0; j = lower.indexOf(ch, j + 1)) {
      if (i < 0) i = j;
      if (isBoundary(text, j)) {
        i = j;
        break;
      }
      if (j - cursor > 12) break;
    }
    if (i < 0) return null;
    const previous = positions.at(-1);
    score += 2;
    if (isBoundary(text, i)) score += 8;
    if (previous !== undefined && i === previous + 1) score += 5;
    else if (previous !== undefined) score -= Math.min(6, i - previous - 1);
    positions.push(i);
    cursor = i + 1;
  }
  return { score, positions };
}

/** Matches `query` against `text`; null when it does not match. */
export function fuzzyMatch(query: string, text: string): FuzzyMatch | null {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return { score: 0, positions: [] };
  const lower = text.toLowerCase();
  let score = 0;
  const positions = new Set<number>();
  for (const word of words) {
    const m = matchWord(word, text, lower);
    if (!m) return null;
    score += m.score;
    for (const p of m.positions) positions.add(p);
  }
  // Among equal matches the shorter text is the closer one.
  score -= Math.min(20, text.length / 8);
  return { score, positions: [...positions].sort((a, b) => a - b) };
}

export interface Ranked<T> {
  item: T;
  match: FuzzyMatch;
}

/**
 * Items matching `query`, best first; ties keep their input order. `bonus`
 * lets a caller lift one class of item (commands over destinations).
 */
export function rank<T>(
  query: string,
  items: readonly T[],
  text: (item: T) => string,
  options: { limit?: number; bonus?: (item: T) => number } = {},
): Ranked<T>[] {
  const out: (Ranked<T> & { order: number })[] = [];
  items.forEach((item, order) => {
    const match = fuzzyMatch(query, text(item));
    if (!match) return;
    out.push({ item, order, match: { ...match, score: match.score + (options.bonus?.(item) ?? 0) } });
  });
  out.sort((a, b) => b.match.score - a.match.score || a.order - b.order);
  return out.slice(0, options.limit ?? out.length);
}

export interface Segment {
  text: string;
  hit: boolean;
}

/** Cuts a run of segments in two at character offset `at`, keeping the highlighting. */
export function splitSegments(parts: readonly Segment[], at: number): [Segment[], Segment[]] {
  const head: Segment[] = [];
  const tail: Segment[] = [];
  let offset = 0;
  for (const part of parts) {
    const cut = Math.min(Math.max(at - offset, 0), part.text.length);
    if (cut > 0) head.push({ text: part.text.slice(0, cut), hit: part.hit });
    if (cut < part.text.length) tail.push({ text: part.text.slice(cut), hit: part.hit });
    offset += part.text.length;
  }
  return [head, tail];
}

/** Splits `text` into runs of matched and unmatched characters for highlighting. */
export function segments(text: string, positions: readonly number[]): Segment[] {
  if (positions.length === 0) return text ? [{ text, hit: false }] : [];
  const hits = new Set(positions);
  const out: Segment[] = [];
  for (let i = 0; i < text.length; i++) {
    const hit = hits.has(i);
    const last = out.at(-1);
    if (last && last.hit === hit) last.text += text[i];
    else out.push({ text: text[i], hit });
  }
  return out;
}
