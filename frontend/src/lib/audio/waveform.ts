// Waveform drawing shared by the player bar and anything else that shows a
// waveform: mirrored 2 px bars with a 1 px gap and rounded caps, the played
// part in the accent colour, a 1 px playhead (SPEC section 6, visual tokens).

export const BAR_WIDTH = 2;
export const BAR_GAP = 1;
const BAR_PITCH = BAR_WIDTH + BAR_GAP;
/** A silent bar still shows as a dot, so the waveform reads as a line. */
const MIN_BAR_HEIGHT = 2;

export interface WaveColors {
  unplayed: string;
  played: string;
  playhead: string;
  tick: string;
}

/** Resolves the waveform colours from the design tokens in effect on `el`. */
export function readColors(el: Element = document.documentElement): WaveColors {
  const style = getComputedStyle(el);
  const token = (name: string) => style.getPropertyValue(name).trim();
  return {
    unplayed: token("--text-3"),
    played: token("--accent"),
    playhead: token("--text-1"),
    tick: token("--text-3"),
  };
}

/** How many bars fit in `width` CSS pixels. */
export function barCount(width: number): number {
  return Math.max(0, Math.floor((width + BAR_GAP) / BAR_PITCH));
}

/** Largest value of each of `count` equal slices of `length` source buckets. */
function reduce(length: number, count: number, at: (i: number) => number): Float32Array {
  const out = new Float32Array(count);
  if (length === 0) return out;
  for (let b = 0; b < count; b++) {
    const from = Math.min(length - 1, Math.floor((b * length) / count));
    const to = Math.max(from + 1, Math.floor(((b + 1) * length) / count));
    let max = 0;
    for (let i = from; i < to && i < length; i++) max = Math.max(max, at(i));
    out[b] = Math.min(1, max);
  }
  return out;
}

/** Bar heights (0..1) from /peaks/{id}: pairs of int8 (min, max). */
export function barsFromPeaks(peaks: Int8Array, count: number): Float32Array {
  return reduce(peaks.length >> 1, count, (i) => Math.max(Math.abs(peaks[i * 2]), Math.abs(peaks[i * 2 + 1])) / 127);
}

/** Bar heights (0..1) from a row's mini peaks: base64 of uint8 buckets. */
export function barsFromMini(base64: string, count: number): Float32Array {
  let binary = "";
  try {
    binary = atob(base64);
  } catch {
    // Malformed peaks draw as a flat line.
  }
  return reduce(binary.length, count, (i) => binary.charCodeAt(i) / 255);
}

export interface Tick {
  /** Position along the waveform, 0..1. */
  x: number;
  /** First beat of a bar. */
  strong: boolean;
}

/**
 * Beat marks for a loop of `duration` seconds at `bpm`. Thins out to bars
 * only, then to nothing, when marks would sit closer than `minSpacing`
 * pixels at `width`.
 */
export function beatTicks(bpm: number, duration: number, width: number, minSpacing = 6): Tick[] {
  if (!(bpm > 0) || !(duration > 0) || !(width > 0)) return [];
  const beat = 60 / bpm;
  const perBeat = (beat / duration) * width;
  let step = 1;
  if (perBeat < minSpacing) step = 4;
  if (perBeat * step < minSpacing) return [];
  const ticks: Tick[] = [];
  // The first beat is the left edge; a mark there says nothing.
  for (let n = step; n * beat < duration - 1e-6; n += step) {
    ticks.push({ x: (n * beat) / duration, strong: n % 4 === 0 });
  }
  return ticks;
}

/**
 * Sizes the canvas backing store for the device pixel ratio and returns a
 * context whose units are CSS pixels. Null when the canvas has no area.
 */
export function prepareCanvas(
  canvas: HTMLCanvasElement,
  width: number,
  height: number,
  dpr: number = window.devicePixelRatio || 1,
): CanvasRenderingContext2D | null {
  if (!(width > 0) || !(height > 0)) return null;
  const w = Math.round(width * dpr);
  const h = Math.round(height * dpr);
  if (canvas.width !== w) canvas.width = w;
  if (canvas.height !== h) canvas.height = h;
  const ctx = canvas.getContext("2d");
  if (!ctx) return null;
  ctx.setTransform(w / width, 0, 0, h / height, 0, 0);
  ctx.clearRect(0, 0, width, height);
  return ctx;
}

export interface WaveformPicture {
  width: number;
  height: number;
  /** One height per bar, 0..1; see barCount. */
  bars: Float32Array;
  colors: WaveColors;
  /** Played fraction 0..1; omit for a waveform without playback state. */
  progress?: number;
  /** Draw the playhead line at `progress`. */
  playhead?: boolean;
  ticks?: Tick[];
}

/** Height reserved under the bars for beat marks. */
export const TICK_AREA = 6;

export function drawWaveform(ctx: CanvasRenderingContext2D, p: WaveformPicture): void {
  const { width, height, bars, colors } = p;
  const ticks = p.ticks ?? [];
  const waveHeight = ticks.length > 0 ? height - TICK_AREA : height;
  const mid = waveHeight / 2;

  const path = new Path2D();
  for (let i = 0; i < bars.length; i++) {
    const h = Math.max(MIN_BAR_HEIGHT, bars[i] * waveHeight);
    const x = i * BAR_PITCH;
    if (path.roundRect) path.roundRect(x, mid - h / 2, BAR_WIDTH, h, BAR_WIDTH / 2);
    else path.rect(x, mid - h / 2, BAR_WIDTH, h);
  }

  const progress = Math.min(1, Math.max(0, p.progress ?? 0));
  const split = progress * width;
  ctx.fillStyle = colors.unplayed;
  ctx.fill(path);
  if (split > 0) {
    ctx.save();
    ctx.beginPath();
    ctx.rect(0, 0, split, waveHeight);
    ctx.clip();
    ctx.fillStyle = colors.played;
    ctx.fill(path);
    ctx.restore();
  }

  if (ticks.length > 0) {
    ctx.fillStyle = colors.tick;
    for (const tick of ticks) {
      const h = tick.strong ? 4 : 2;
      ctx.globalAlpha = tick.strong ? 1 : 0.6;
      ctx.fillRect(Math.round(tick.x * width), height - h, 1, h);
    }
    ctx.globalAlpha = 1;
  }

  if (p.playhead) {
    ctx.fillStyle = colors.playhead;
    // Keep the line on the canvas at both ends.
    ctx.fillRect(Math.min(width - 1, Math.max(0, Math.round(split))), 0, 1, waveHeight);
  }
}

/**
 * Calls `redraw` whenever the picture may be stale for reasons outside the
 * data: the element resized, the theme changed (system scheme or the manual
 * override on <html>), or the window moved to a screen with another pixel
 * ratio. Returns the function that stops watching.
 */
export function watchAppearance(el: Element, redraw: () => void): () => void {
  const resize = new ResizeObserver(redraw);
  resize.observe(el);

  const scheme = window.matchMedia("(prefers-color-scheme: light)");
  scheme.addEventListener("change", redraw);

  const theme = new MutationObserver(redraw);
  theme.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });

  // A resolution query only fires once, when it stops matching.
  let density: MediaQueryList | null = null;
  const onDensity = () => {
    density?.removeEventListener("change", onDensity);
    density = window.matchMedia(`(resolution: ${window.devicePixelRatio || 1}dppx)`);
    density.addEventListener("change", onDensity);
    redraw();
  };
  density = window.matchMedia(`(resolution: ${window.devicePixelRatio || 1}dppx)`);
  density.addEventListener("change", onDensity);

  return () => {
    resize.disconnect();
    scheme.removeEventListener("change", redraw);
    theme.disconnect();
    density?.removeEventListener("change", onDensity);
  };
}
