// The omnibar query language, reimplemented for the mock backend. The real
// implementation lives in Go (internal/index); this follows the same rules
// closely enough for UI work.

import type { Sample } from "../types";
import { compatibleKeys, normaliseKey } from "./keys";

/** What a predicate needs to know beyond the sample itself. */
export interface QueryContext {
  /** Pack ids matching a name or id (exact match preferred, else substring). */
  packIds(nameOrId: string): Set<string>;
  /** Sample ids in collections matching a name or id. */
  collectionSampleIds(nameOrId: string): Set<string>;
  /** Sample ids of the last import batch. */
  recentIds(): Set<string>;
}

export type Predicate = (s: Sample) => boolean;

export interface ParsedQuery {
  predicates: Predicate[];
  /** Lower-cased free-text terms that are not negated, for relevance ranking. */
  text: string[];
  /** First problem found, human readable; "" when the query is fine. */
  error: string;
}

/**
 * Splits on whitespace; double quotes group and are removed, and inside them
 * a backslash escapes a quote or a backslash.
 */
export function tokenize(query: string): string[] {
  const tokens: string[] = [];
  let current = "";
  let quoted = false;
  let started = false;
  for (let i = 0; i < query.length; i++) {
    const ch = query[i];
    if (quoted && ch === "\\" && (query[i + 1] === '"' || query[i + 1] === "\\")) {
      current += query[++i];
    } else if (ch === '"') {
      quoted = !quoted;
      started = true;
    } else if (!quoted && /\s/.test(ch)) {
      if (started) tokens.push(current);
      current = "";
      started = false;
    } else {
      current += ch;
      started = true;
    }
  }
  if (started) tokens.push(current);
  return tokens;
}

/** Lower-cased text a free-text term is matched against. */
export function haystack(s: Sample): string {
  return `${s.name}\n${s.relPath}\n${s.packName}\n${s.tags.join("\n")}`.toLowerCase();
}

function tagPredicate(value: string): Predicate {
  const v = value.toLowerCase().replace(/^\/+|\/+$/g, "");
  const leafOnly = !v.includes("/");
  return (s) =>
    s.tags.some((raw) => {
      const t = raw.toLowerCase();
      return t === v || t.startsWith(v + "/") || (leafOnly && t.endsWith("/" + v));
    });
}

function bpmPredicate(value: string): Predicate | null {
  const num = "(\\d+(?:\\.\\d+)?)";
  let m: RegExpExecArray | null;
  if ((m = new RegExp(`^${num}$`).exec(value))) {
    const target = Number(m[1]);
    return (s) => s.bpm > 0 && Math.abs(s.bpm - target) <= 0.5;
  }
  if ((m = new RegExp(`^${num}-${num}$`).exec(value))) {
    const lo = Math.min(Number(m[1]), Number(m[2]));
    const hi = Math.max(Number(m[1]), Number(m[2]));
    return (s) => s.bpm >= lo && s.bpm <= hi;
  }
  if ((m = new RegExp(`^~${num}$`).exec(value))) {
    const target = Number(m[1]);
    const near = (bpm: number) => Math.abs(bpm - target) <= target * 0.03;
    return (s) => s.bpm > 0 && (near(s.bpm) || near(s.bpm * 2) || near(s.bpm / 2));
  }
  if ((m = new RegExp(`^(>=|<=|>|<)${num}$`).exec(value))) {
    const target = Number(m[2]);
    switch (m[1]) {
      case ">":
        return (s) => s.bpm > target;
      case ">=":
        return (s) => s.bpm >= target;
      case "<":
        return (s) => s.bpm > 0 && s.bpm < target;
      default:
        return (s) => s.bpm > 0 && s.bpm <= target;
    }
  }
  return null;
}

/** Parses "250ms", "2s", "1.5m" or a bare number of seconds. */
export function parseDuration(value: string): number | null {
  const m = /^(\d+(?:\.\d+)?)(ms|s|m)?$/.exec(value);
  if (!m) return null;
  const n = Number(m[1]);
  if (m[2] === "ms") return n / 1000;
  if (m[2] === "m") return n * 60;
  return n;
}

function lenPredicate(value: string): Predicate | null {
  const cmp = /^(>=|<=|>|<)(.+)$/.exec(value);
  if (cmp) {
    const d = parseDuration(cmp[2]);
    if (d === null) return null;
    switch (cmp[1]) {
      case ">":
        return (s) => s.dur > d;
      case ">=":
        return (s) => s.dur >= d;
      case "<":
        return (s) => s.dur < d;
      default:
        return (s) => s.dur <= d;
    }
  }
  const range = /^([^-]+)-([^-]+)$/.exec(value);
  if (range) {
    const a = parseDuration(range[1]);
    const b = parseDuration(range[2]);
    if (a === null || b === null) return null;
    const lo = Math.min(a, b);
    const hi = Math.max(a, b);
    return (s) => s.dur >= lo && s.dur <= hi;
  }
  return null;
}

function keyPredicate(value: string): Predicate | null {
  const compatible = /^compatible:(.+)$/.exec(value);
  if (compatible) {
    const keys = compatibleKeys(compatible[1]);
    return keys.length ? (s) => keys.includes(s.key) || rootFits(s, keys) : null;
  }
  const key = normaliseKey(value);
  return key ? (s) => s.key === key || rootFits(s, [key]) : null;
}

/** A bare root note fits every key on that tonic (as in internal/index). */
function rootFits(s: Sample, keys: string[]): boolean {
  return s.keyRoot && keys.some((k) => k.endsWith("m") && k.slice(0, -1) === s.key);
}

function typePredicate(value: string): Predicate | null {
  if (value === "loop" || value === "loops") return (s) => s.type === "loop";
  if (/^(oneshot|one-shot|oneshots|shot)$/.test(value)) return (s) => s.type === "oneshot";
  return null;
}

function fmtPredicate(value: string): Predicate {
  const canon = (ext: string) => (ext === "aiff" || ext === "aifc" ? "aif" : ext);
  const want = canon(value.replace(/^\./, ""));
  return (s) => canon(s.ext.toLowerCase()) === want;
}

function isPredicate(value: string, ctx: QueryContext): Predicate | null {
  switch (value) {
    case "fav":
    case "favorite":
    case "favourite":
      return (s) => s.fav;
    case "untagged":
      return (s) => s.tags.length === 0;
    case "missing":
      return (s) => s.missing;
    case "unanalyzed":
    case "unanalysed":
      return (s) => !s.analyzed;
    case "recent": {
      const ids = ctx.recentIds();
      return (s) => ids.has(s.id);
    }
    default:
      return null;
  }
}

const FIELD = /^(tag|pack|in|bpm|key|type|len|fmt|is):(.*)$/i;

/**
 * Parses a query. Never throws: a malformed value is reported in `error` and
 * that term is skipped; unknown `foo:bar` tokens are free text.
 */
export function parseQuery(query: string, ctx: QueryContext): ParsedQuery {
  const out: ParsedQuery = { predicates: [], text: [], error: "" };

  for (const token of tokenize(query)) {
    const neg = token.length > 1 && token.startsWith("-");
    const body = neg ? token.slice(1) : token;
    let predicate: Predicate | null = null;
    let recognised = true;

    const field = FIELD.exec(body);
    if (body.length > 1 && body.startsWith("#")) {
      predicate = tagPredicate(body.slice(1));
    } else if (body.length > 1 && body.startsWith("@")) {
      const ids = ctx.packIds(body.slice(1));
      predicate = (s) => ids.has(s.packId);
    } else if (field && field[2] !== "") {
      const name = field[1].toLowerCase();
      const value = field[2];
      const lower = value.toLowerCase();
      switch (name) {
        case "tag":
          predicate = tagPredicate(value);
          break;
        case "pack": {
          const ids = ctx.packIds(value);
          predicate = (s) => ids.has(s.packId);
          break;
        }
        case "in": {
          const ids = ctx.collectionSampleIds(value);
          predicate = (s) => ids.has(s.id);
          break;
        }
        case "bpm":
          predicate = bpmPredicate(lower);
          break;
        case "key":
          predicate = keyPredicate(value);
          break;
        case "type":
          predicate = typePredicate(lower);
          break;
        case "len":
          predicate = lenPredicate(lower);
          break;
        case "fmt":
          predicate = fmtPredicate(lower);
          break;
        case "is":
          predicate = isPredicate(lower, ctx);
          break;
      }
      if (!predicate) {
        out.error ||= `Cannot understand "${body}"`;
        continue;
      }
    } else {
      recognised = false;
    }

    if (!recognised) {
      const needle = body.toLowerCase();
      if (needle === "") continue;
      predicate = (s) => haystackOf(s).includes(needle);
      if (!neg) out.text.push(needle);
    }

    const p = predicate!;
    out.predicates.push(neg ? (s) => !p(s) : p);
  }
  return out;
}

// Samples in the mock carry a precomputed `hay`; fall back for plain samples.
function haystackOf(s: Sample): string {
  return (s as Sample & { hay?: string }).hay ?? haystack(s);
}

export function matches(parsed: ParsedQuery, s: Sample): boolean {
  for (const p of parsed.predicates) {
    if (!p(s)) return false;
  }
  return true;
}
