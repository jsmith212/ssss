// Where the playhead is: a linear map from audio-context time to a position
// in the sample, with an optional loop region.

export interface LoopRegion {
  /** Seconds in the sample. */
  start: number;
  end: number;
}

export interface Timeline {
  /** Context time at which the sample is at `input`. */
  at: number;
  /** Position in the sample at `at`, seconds. */
  input: number;
  /** Sample seconds per context second. */
  rate: number;
  loop: LoopRegion | null;
  /** Length of the sample, seconds. */
  duration: number;
}

/** Folds a position past the loop end back into the loop. */
export function wrap(position: number, loop: LoopRegion | null): number {
  if (!loop || position < loop.end) return position;
  const length = loop.end - loop.start;
  if (!(length > 0)) return position;
  return loop.start + ((position - loop.start) % length);
}

/** Position in the sample at context time `now`; holds at `input` before `at`. */
export function positionAt(t: Timeline, now: number): number {
  const raw = t.input + Math.max(0, now - t.at) * t.rate;
  return Math.min(wrap(raw, t.loop), t.duration);
}

/**
 * Context time at which playback reaches the end of the sample, or
 * Infinity while it loops.
 */
export function endTime(t: Timeline): number {
  if (t.loop && t.input < t.loop.end) return Number.POSITIVE_INFINITY;
  return t.at + Math.max(0, t.duration - t.input) / t.rate;
}
