// Full-resolution peaks for the player waveform: /peaks/{id}, 2000 pairs of
// int8 (min, max).

import { Lru } from "./lru";

const cache = new Lru<Int8Array>(256);

/** Peaks already fetched this session. */
export function cachedPeaks(id: string): Int8Array | undefined {
  return cache.get(id);
}

/** Fetches the peaks; rejects on failure or abort. */
export async function loadPeaks(id: string, url: string, signal?: AbortSignal): Promise<Int8Array> {
  const hit = cache.get(id);
  if (hit) return hit;
  const response = await fetch(url, { signal });
  if (!response.ok) throw new Error(`peaks request failed (${response.status})`);
  const body = await response.arrayBuffer();
  signal?.throwIfAborted();
  // An odd trailing byte cannot be half of a pair.
  const peaks = new Int8Array(body, 0, body.byteLength & ~1);
  cache.set(id, peaks);
  return peaks;
}

/** Forgets a sample's peaks, e.g. after it was reanalysed. */
export function invalidatePeaks(id: string): void {
  cache.delete(id);
}
