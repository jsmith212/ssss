// URLs of the binary endpoints on the Wails asset server (same origin).

/** Decoded audio as planar float32; see Api.audioUrl. */
export function audioUrl(id: string): string {
  return `/audio/${encodeURIComponent(id)}.f32`;
}

/** Full-resolution peaks; see Api.peaksUrl. */
export function peaksUrl(id: string): string {
  return `/peaks/${encodeURIComponent(id)}`;
}
