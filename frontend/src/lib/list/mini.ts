// Row mini-waveform: 96 uint8 peak buckets (base64) drawn as mirrored bars.

export const MINI_WIDTH = 96;
const BAR_WIDTH = 2;
const BAR_GAP = 1;
export const MINI_BARS = MINI_WIDTH / (BAR_WIDTH + BAR_GAP);

/** Decodes the wire form; malformed input yields an empty array. */
export function decodeMini(base64: string): Uint8Array {
  if (base64 === "") return new Uint8Array(0);
  try {
    const binary = atob(base64);
    const out = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
    return out;
  } catch {
    return new Uint8Array(0);
  }
}

/** Reduces the buckets to `bars` values in 0..1, taking the peak of each group. */
export function miniBars(peaks: Uint8Array, bars = MINI_BARS): number[] {
  if (peaks.length === 0) return [];
  const out: number[] = [];
  for (let b = 0; b < bars; b++) {
    const from = Math.floor((b * peaks.length) / bars);
    const to = Math.max(from + 1, Math.floor(((b + 1) * peaks.length) / bars));
    let max = 0;
    for (let i = from; i < to && i < peaks.length; i++) max = Math.max(max, peaks[i]);
    out.push(max / 255);
  }
  return out;
}

/** Draws the waveform, sizing the backing store for the device pixel ratio. */
export function drawMini(canvas: HTMLCanvasElement, base64: string, color: string, height: number): void {
  const dpr = window.devicePixelRatio || 1;
  const w = Math.round(MINI_WIDTH * dpr);
  const h = Math.round(height * dpr);
  if (canvas.width !== w) canvas.width = w;
  if (canvas.height !== h) canvas.height = h;
  const ctx = canvas.getContext("2d");
  if (!ctx) return;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, MINI_WIDTH, height);
  const bars = miniBars(decodeMini(base64));
  if (bars.length === 0) return;
  ctx.fillStyle = color;
  ctx.beginPath();
  const mid = height / 2;
  for (let i = 0; i < bars.length; i++) {
    const half = Math.max(1, bars[i] * mid);
    const x = i * (BAR_WIDTH + BAR_GAP);
    if (ctx.roundRect) ctx.roundRect(x, mid - half, BAR_WIDTH, half * 2, BAR_WIDTH / 2);
    else ctx.rect(x, mid - half, BAR_WIDTH, half * 2);
  }
  ctx.fill();
}
