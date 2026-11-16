// Tempo and key matching maths (SPEC section 7): how fast and how far
// transposed a sample plays so it fits the project BPM and key.

import { formatKey, parseKey, relativeKey, transposeKey } from "./keys";

export const MIN_RATE = 0.67;
export const MAX_RATE = 1.5;

/**
 * Playback rate that brings `bpm` to `projectBpm`, folded by octaves into
 * [0.67, 1.5]: a 70 BPM loop in a 140 BPM project plays untouched in half
 * time instead of at double speed.
 */
export function tempoRate(bpm: number, projectBpm: number): number {
  if (!(bpm > 0) || !(projectBpm > 0)) return 1;
  let rate = projectBpm / bpm;
  while (rate > MAX_RATE) rate /= 2;
  while (rate < MIN_RATE) rate *= 2;
  return rate;
}

/**
 * Semitones (-6..+5) that move `key` onto `projectKey` by the nearest
 * interval. When one is minor and the other major, the sample is aimed at
 * the project's relative key, so Am needs no shift in a C major project.
 * Returns null when either key is unknown.
 */
export function keyShift(key: string, projectKey: string): number | null {
  const from = parseKey(key);
  let to = parseKey(projectKey);
  if (!from || !to) return null;
  if (from.minor !== to.minor) to = relativeKey(to);
  return ((((to.pc - from.pc) % 12) + 18) % 12) - 6;
}

/** Semitones (-6..+5) from one tonic to the other by the nearest interval, modes ignored. */
function tonicShift(key: string, projectKey: string): number | null {
  const from = parseKey(key);
  const to = parseKey(projectKey);
  if (!from || !to) return null;
  return ((((to.pc - from.pc) % 12) + 18) % 12) - 6;
}

export interface MatchInput {
  /** Only loops are tempo matched. */
  type: string;
  bpm: number;
  key: string;
  /**
   * The key is only a root note (Sample.keyRoot). Analysis finds no mode
   * worth guessing in a single note or stab and writes the root like a major
   * key. Such a key is matched root to tonic: an 808 on A is already in tune
   * with an Am project and must not be aimed at its relative major. A key
   * from the file name or the user says what it means and gets the full rule.
   */
  keyRoot?: boolean;
}

export interface MatchSettings {
  matchTempo: boolean;
  matchKey: boolean;
  projectBpm: number;
  projectKey: string;
}

export interface Match {
  /** Playback rate; 1 when tempo matching does not apply. */
  rate: number;
  /** Pitch shift; 0 when key matching does not apply. */
  semitones: number;
  /** Tempo matching is on and this sample can be matched. */
  tempo: boolean;
  /** Key matching is on and this sample can be matched. */
  key: boolean;
  /** Whether the sample could be matched at all, regardless of the toggles. */
  canTempo: boolean;
  canKey: boolean;
  /** Tempo the sample sounds at; 0 when its BPM is unknown. */
  effectiveBpm: number;
  /** Key the sample sounds in; "" when its key is unknown. */
  effectiveKey: string;
}

/** True when the match changes the sound, i.e. the stretcher is needed. */
export function isAltered(m: Pick<Match, "rate" | "semitones">): boolean {
  return Math.abs(m.rate - 1) > 1e-4 || m.semitones !== 0;
}

export function computeMatch(sample: MatchInput, s: MatchSettings): Match {
  const canTempo = sample.type === "loop" && sample.bpm > 0 && s.projectBpm > 0;
  const shift = sample.keyRoot ? tonicShift(sample.key, s.projectKey) : keyShift(sample.key, s.projectKey);
  const canKey = shift !== null;
  const tempo = canTempo && s.matchTempo;
  const key = canKey && s.matchKey;
  const rate = tempo ? tempoRate(sample.bpm, s.projectBpm) : 1;
  const semitones = key ? shift : 0;
  const parsed = parseKey(sample.key);
  return {
    rate,
    semitones,
    tempo,
    key,
    canTempo,
    canKey,
    effectiveBpm: sample.bpm > 0 ? sample.bpm * rate : 0,
    effectiveKey: parsed ? formatKey(transposeKey(parsed, semitones)) : "",
  };
}

/** "+2 st", "−3 st", "0 st". */
export function formatSemitones(semitones: number): string {
  const sign = semitones > 0 ? "+" : semitones < 0 ? "−" : "";
  return `${sign}${Math.abs(semitones)} st`;
}

export { formatBpm } from "../format";
