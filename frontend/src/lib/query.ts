// Client-side view of the omnibar query language (SPEC section 4). The real
// parser lives in Go (internal/index); this one only needs to recognise
// tokens well enough to show them as chips, complete them and add, remove
// or swap them when the sidebar and the facet strip are used.

import type { CollectionInfo, PackInfo, SavedView, TagCount } from "./types";

export type TokenKind =
  | "text"
  | "tag"
  | "pack"
  | "in"
  | "bpm"
  | "key"
  | "type"
  | "len"
  | "fmt"
  | "is";

export type FilterKind = Exclude<TokenKind, "text">;

export interface TokenSpec {
  kind: TokenKind;
  /** Without prefix and quotes, e.g. "drums/kick", "120-128". */
  value: string;
  negated?: boolean;
}

export interface Token extends TokenSpec {
  negated: boolean;
  /** The token as written, quotes included. */
  raw: string;
}

const KINDS: readonly FilterKind[] = ["tag", "pack", "in", "bpm", "key", "type", "len", "fmt", "is"];

function isFilterKind(name: string): name is FilterKind {
  return (KINDS as readonly string[]).includes(name);
}

/** Inside quotes a backslash escapes a quote or a backslash, as in the Go lexer. */
function escapeAt(text: string, i: number): boolean {
  return text[i] === "\\" && (text[i + 1] === '"' || text[i + 1] === "\\");
}

/** Splits on whitespace outside double quotes; quotes and escapes stay in the words. */
function words(query: string): string[] {
  const out: string[] = [];
  let current = "";
  let quoted = false;
  for (let i = 0; i < query.length; i++) {
    const ch = query[i];
    if (quoted && escapeAt(query, i)) {
      current += ch + query[++i];
      continue;
    }
    if (ch === '"') quoted = !quoted;
    if (!quoted && /\s/.test(ch)) {
      if (current !== "") out.push(current);
      current = "";
    } else {
      current += ch;
    }
  }
  if (current !== "") out.push(current);
  return out;
}

/** The value a written word stands for: quotes dropped, escapes inside them decoded. */
function unquote(s: string): string {
  let out = "";
  let quoted = false;
  for (let i = 0; i < s.length; i++) {
    if (quoted && escapeAt(s, i)) out += s[++i];
    else if (s[i] === '"') quoted = !quoted;
    else out += s[i];
  }
  return out;
}

function classify(raw: string): Token {
  let body = raw;
  let negated = false;
  if (body.length > 1 && body.startsWith("-")) {
    negated = true;
    body = body.slice(1);
  }
  let kind: TokenKind = "text";
  let value = body;
  if (body.startsWith("#")) {
    kind = "tag";
    value = body.slice(1);
  } else if (body.startsWith("@")) {
    kind = "pack";
    value = body.slice(1);
  } else {
    const colon = body.indexOf(":");
    const name = colon > 0 ? body.slice(0, colon).toLowerCase() : "";
    if (isFilterKind(name)) {
      kind = name;
      value = body.slice(colon + 1);
    }
  }
  value = unquote(value);
  if (kind === "tag") value = value.replace(/^\/+|\/+$/g, "");
  // A prefix with nothing after it ("bpm:", "#") is still being typed.
  if (kind !== "text" && value === "") return { kind: "text", value: unquote(body), negated, raw };
  return { kind, value, negated, raw };
}

export function tokenize(query: string): Token[] {
  return words(query).map(classify);
}

/** Writes a value so that the lexer reads it back unchanged. */
function quoteIfNeeded(value: string): string {
  return /[\s"\\]/.test(value) ? `"${value.replace(/["\\]/g, "\\$&")}"` : value;
}

/** The canonical written form of a token: `tag:drums/kick`, `-pack:"A B"`. */
export function formatToken(spec: TokenSpec): string {
  const sign = spec.negated ? "-" : "";
  const value = quoteIfNeeded(spec.value.trim());
  return spec.kind === "text" ? sign + value : `${sign}${spec.kind}:${value}`;
}

function identity(spec: TokenSpec): string {
  return `${spec.negated ? "-" : ""}${spec.kind}:${spec.value.trim().toLowerCase()}`;
}

function join(tokens: Token[]): string {
  return tokens.map((t) => t.raw).join(" ");
}

export function hasToken(query: string, spec: TokenSpec): boolean {
  const id = identity(spec);
  return tokenize(query).some((t) => identity(t) === id);
}

/** A membership test for many tokens against one query (sidebar highlighting). */
export function tokenMatcher(query: string): (spec: TokenSpec) => boolean {
  const present = new Set(tokenize(query).map(identity));
  return (spec) => present.has(identity(spec));
}

/** Appends the token unless the query already has it. */
export function addToken(query: string, spec: TokenSpec): string {
  if (hasToken(query, spec)) return query.trim();
  const base = query.trim();
  return base === "" ? formatToken(spec) : `${base} ${formatToken(spec)}`;
}

export function removeToken(query: string, spec: TokenSpec): string {
  const id = identity(spec);
  return join(tokenize(query).filter((t) => identity(t) !== id));
}

export function toggleToken(query: string, spec: TokenSpec): string {
  return hasToken(query, spec) ? removeToken(query, spec) : addToken(query, spec);
}

/** Positive (not negated) tokens of one kind. */
export function tokensOf(query: string, kind: TokenKind): Token[] {
  return tokenize(query).filter((t) => t.kind === kind && !t.negated);
}

/**
 * Replaces every positive token of `kind` with one holding `value`, or just
 * removes them when `value` is null or empty. For single-valued facets such
 * as the BPM range.
 */
export function setKind(query: string, kind: FilterKind, value: string | null): string {
  const kept = join(tokenize(query).filter((t) => t.kind !== kind || t.negated));
  return value === null || value.trim() === "" ? kept : addToken(kept, { kind, value });
}

/** True when the query contains a positive free-text term (relevance sort applies). */
export function hasFreeText(query: string): boolean {
  return tokenize(query).some((t) => t.kind === "text" && !t.negated);
}

/** The pack the query is restricted to, when it names exactly one. */
export function singlePack(query: string): string | null {
  const packs = tokensOf(query, "pack");
  return packs.length === 1 ? packs[0].value : null;
}

export interface SplitQuery {
  /** Filter tokens, shown as chips. */
  chips: Token[];
  /** Free-text terms as written, shown in the input. */
  text: string;
}

export function splitQuery(query: string): SplitQuery {
  const tokens = tokenize(query);
  return {
    chips: tokens.filter((t) => t.kind !== "text"),
    text: join(tokens.filter((t) => t.kind === "text")),
  };
}

export function joinQuery(chips: readonly Token[], text: string): string {
  return [...chips.map((t) => t.raw), text.trim()].filter((s) => s !== "").join(" ");
}

// --- packs and collections ----------------------------------------------------------
//
// `pack:` and `in:` take an id or a name. Names are neither unique nor
// stable, so everything the UI writes uses the id; a name the user typed
// selects every pack or collection called that, as it does in the backend.

export interface Named {
  id: string;
  name: string;
}

export type RefKind = "pack" | "in";

export interface NameSources {
  packs: readonly Named[];
  collections: readonly Named[];
}

function selects(value: string, item: Named): boolean {
  return value === item.id || value.toLowerCase() === item.name.toLowerCase();
}

/** The packs or collections a `pack:` / `in:` value selects. */
export function referents<T extends Named>(value: string, items: readonly T[]): T[] {
  return items.filter((item) => selects(value, item));
}

/** A test for "does a positive token of this kind select the item" (sidebar highlighting). */
export function refMatcher(query: string, kind: RefKind): (item: Named) => boolean {
  const values = tokensOf(query, kind).map((t) => t.value);
  return (item) => values.some((value) => selects(value, item));
}

/** Adds the item's id token, or removes every positive token that selects the item. */
export function toggleRef(query: string, kind: RefKind, item: Named): string {
  const tokens = tokenize(query);
  const kept = tokens.filter((t) => t.kind !== kind || t.negated || !selects(t.value, item));
  return kept.length < tokens.length ? join(kept) : addToken(query, { kind, value: item.id });
}

/**
 * The one collection the query is confined to: it has exactly one positive
 * `in:` token and that token selects exactly one collection.
 */
export function singleCollection<T extends Named>(query: string, collections: readonly T[]): T | null {
  const tokens = tokensOf(query, "in");
  if (tokens.length !== 1) return null;
  const found = referents(tokens[0].value, collections);
  return found.length === 1 ? found[0] : null;
}

/** A token's value as people read it: the name behind a pack or collection id. */
function displayValue(token: TokenSpec, names?: NameSources): string {
  if (!names || (token.kind !== "pack" && token.kind !== "in")) return token.value;
  const items = token.kind === "pack" ? names.packs : names.collections;
  return items.find((item) => item.id === token.value)?.name ?? token.value;
}

/**
 * How a chip reads: `#drums/kick`, `@KSHMR Vol 4`, `bpm:120-128`. With
 * `names`, pack and collection ids read as their names.
 */
export function chipLabel(token: TokenSpec, names?: NameSources): string {
  const sign = token.negated ? "-" : "";
  const value = displayValue(token, names);
  if (token.kind === "tag") return `${sign}#${value}`;
  if (token.kind === "pack") return `${sign}@${value}`;
  if (token.kind === "text") return sign + value;
  return `${sign}${token.kind}:${value}`;
}

const IS_WORDS: Record<string, string> = {
  fav: "Favourites",
  untagged: "Untagged",
  recent: "Recent",
  missing: "Missing",
  unanalyzed: "Unanalysed",
};

/** One token in plain words: "Loops", "120-128 BPM", "kick", "not bass". */
function describeToken(token: TokenSpec, names?: NameSources): string {
  const value = displayValue(token, names);
  const lower = value.toLowerCase();
  let words: string;
  switch (token.kind) {
    case "tag":
      words = value.slice(value.lastIndexOf("/") + 1);
      break;
    case "type":
      words = lower === "loop" ? "Loops" : lower === "oneshot" ? "One-shots" : value;
      break;
    case "bpm":
      words = `${value} BPM`;
      break;
    case "key":
      words = lower.startsWith("compatible:") ? `fits ${value.slice("compatible:".length)}` : value;
      break;
    case "is":
      words = IS_WORDS[lower] ?? value;
      break;
    case "len":
      words = `length ${value}`;
      break;
    case "fmt":
      words = value.toUpperCase();
      break;
    default:
      words = value;
  }
  return token.negated ? `not ${words}` : words;
}

/** A query as a short readable line, e.g. "Loops · 120-128 BPM · Am · kick"; a default name for a saved view. */
export function describeQuery(query: string, names?: NameSources): string {
  return tokenize(query)
    .map((token) => describeToken(token, names))
    .join(" · ");
}

/**
 * The name offered when saving a query as a view: the one it is already
 * saved under, else its description, numbered if another view has taken
 * that name (saving under an existing name replaces that view).
 */
export function suggestViewName(query: string, views: readonly SavedView[], names?: NameSources): string {
  const same = views.find((v) => v.query.trim() === query.trim());
  if (same) return same.name;
  const taken = new Set(views.map((v) => v.name.toLowerCase()));
  const summary = describeQuery(query, names);
  let name = summary;
  for (let n = 2; taken.has(name.toLowerCase()); n++) name = `${summary} ${n}`;
  return name;
}

// --- autocomplete -----------------------------------------------------------------

export interface WordRange {
  word: string;
  start: number;
  end: number;
}

/** The whitespace-delimited word around the caret (quotes group). */
export function wordAt(text: string, caret: number): WordRange {
  let start = 0;
  let end = text.length;
  let quoted = false;
  for (let i = 0; i < text.length; i++) {
    if (quoted && escapeAt(text, i)) {
      i++;
      continue;
    }
    const ch = text[i];
    if (ch === '"') quoted = !quoted;
    else if (!quoted && /\s/.test(ch)) {
      if (i >= caret) {
        end = i;
        break;
      }
      start = i + 1;
    }
  }
  return { word: text.slice(start, end), start, end };
}

/** True when the word is (the beginning of) a filter token rather than free text. */
export function isFilterWord(word: string): boolean {
  const body = word.startsWith("-") ? word.slice(1) : word;
  if (body.startsWith("#") || body.startsWith("@")) return true;
  const colon = body.indexOf(":");
  return colon > 0 && isFilterKind(body.slice(0, colon).toLowerCase());
}

export interface Suggestion {
  kind: FilterKind;
  /** Chip-style label. */
  label: string;
  /** Sample count, when known. */
  count?: number;
  /** The token to put into the query. */
  token: string;
}

export interface SuggestSources {
  tags: readonly TagCount[];
  packs: readonly PackInfo[];
  collections: readonly CollectionInfo[];
}

const NOTES = ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"];
/** Every key in the library's notation: sharps only, minor with an "m" suffix. */
export const KEYS: readonly string[] = [...NOTES, ...NOTES.map((n) => n + "m")];

const FIXED: Partial<Record<FilterKind, readonly string[]>> = {
  key: KEYS,
  type: ["loop", "oneshot"],
  is: ["fav", "untagged", "recent", "missing", "unanalyzed"],
  fmt: ["wav", "aiff", "flac", "mp3", "ogg"],
};

const MAX_SUGGESTIONS = 8;

/** 0 = no match; higher = better. Prefix of the last path segment ranks first. */
function score(candidate: string, needle: string): number {
  if (needle === "") return 1;
  const c = candidate.toLowerCase();
  const leaf = c.slice(c.lastIndexOf("/") + 1);
  if (leaf === needle || c === needle) return 5;
  if (leaf.startsWith(needle)) return 4;
  if (c.startsWith(needle)) return 3;
  if (c.includes(needle)) return 2;
  return 0;
}

function rank<T>(items: readonly T[], text: (item: T) => string, needle: string, count: (item: T) => number): T[] {
  return items
    .map((item) => ({ item, s: score(text(item), needle) }))
    .filter((x) => x.s > 0)
    .sort((a, b) => b.s - a.s || count(b.item) - count(a.item))
    .map((x) => x.item);
}

function suggestion(kind: FilterKind, value: string, negated: boolean, count?: number): Suggestion {
  const spec = { kind, value, negated };
  return { kind, label: chipLabel(spec), count, token: formatToken(spec) };
}

/** A pack or collection: labelled by name, written by id. */
function refSuggestion(kind: RefKind, item: Named, negated: boolean, count: number): Suggestion {
  return {
    kind,
    label: chipLabel({ kind, value: item.name, negated }),
    count,
    token: formatToken({ kind, value: item.id, negated }),
  };
}

/**
 * Completions for the word being typed. A word with a filter prefix
 * (`#ki`, `pack:ks`, `key:`) completes values of that kind; plain text of
 * two or more characters offers matching tags, packs and collections.
 */
export function suggest(word: string, sources: SuggestSources): Suggestion[] {
  const negated = word.length > 1 && word.startsWith("-");
  const body = negated ? word.slice(1) : word;
  let kind: FilterKind | null = null;
  let needle = body;
  if (body.startsWith("#")) {
    kind = "tag";
    needle = body.slice(1);
  } else if (body.startsWith("@")) {
    kind = "pack";
    needle = body.slice(1);
  } else {
    const colon = body.indexOf(":");
    const name = colon > 0 ? body.slice(0, colon).toLowerCase() : "";
    if (isFilterKind(name)) {
      kind = name;
      needle = body.slice(colon + 1);
    }
  }
  needle = unquote(needle).toLowerCase();

  const tags = () =>
    rank(sources.tags, (t) => t.tag, needle, (t) => t.count).map((t) => suggestion("tag", t.tag, negated, t.count));
  const packs = () =>
    rank(sources.packs, (p) => p.name, needle, (p) => p.count).map((p) => refSuggestion("pack", p, negated, p.count));
  const collections = () =>
    rank(sources.collections, (c) => c.name, needle, (c) => c.count).map((c) =>
      refSuggestion("in", c, negated, c.count),
    );

  if (kind === "tag") return tags().slice(0, MAX_SUGGESTIONS);
  if (kind === "pack") return packs().slice(0, MAX_SUGGESTIONS);
  if (kind === "in") return collections().slice(0, MAX_SUGGESTIONS);
  if (kind !== null) {
    const fixed = FIXED[kind];
    if (!fixed) return [];
    const k = kind;
    return fixed
      .filter((v) => v.toLowerCase().startsWith(needle))
      .slice(0, kind === "key" ? 24 : MAX_SUGGESTIONS)
      .map((v) => suggestion(k, v, negated));
  }
  if (needle.length < 2) return [];
  return [...tags().slice(0, 4), ...packs().slice(0, 2), ...collections().slice(0, 2)];
}
