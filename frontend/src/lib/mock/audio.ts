// Synthesised audio and waveforms for the mock backend, in the same wire
// formats the Go side serves: planar float32 PCM for /audio/{id}.f32 and
// 2000 int8 min/max pairs for /peaks/{id}.

import { FULL_PEAK_BUCKETS, MINI_PEAKS } from "../types";
import type { MockSample } from "./data";
import { parseKey, rootFrequency } from "./keys";
import { Rng } from "./rng";

const RATE = 44100;
/** Longer mock samples are truncated; nobody needs minutes of fake audio. */
const MAX_SECONDS = 20;
const TAU = Math.PI * 2;

export interface Rendered {
  frames: number;
  rate: number;
  channels: Float32Array[];
}

type VoiceFn = (out: Float32Array, start: number, length: number, gain: number) => void;

function kick(rng: Rng): VoiceFn {
  const sweep = 20 + rng.next() * 30;
  const decay = 6 + rng.next() * 10;
  const base = 40 + rng.next() * 20;
  return (out, start, length, gain) => {
    const n = Math.min(length, out.length - start);
    for (let i = 0; i < n; i++) {
      const t = i / RATE;
      const phase = base * t + (110 * (1 - Math.exp(-sweep * t))) / sweep;
      out[start + i] += Math.sin(TAU * phase) * Math.exp(-decay * t) * gain;
    }
  };
}

/** Noise burst; `brightness` 0 = full band, towards 1 = high-passed (hats). */
function noise(rng: Rng, decay: number, brightness: number): VoiceFn {
  let state = rng.int(1, 0x7fffffff);
  return (out, start, length, gain) => {
    const n = Math.min(length, out.length - start);
    let prev = 0;
    for (let i = 0; i < n; i++) {
      state = (Math.imul(state, 1664525) + 1013904223) | 0;
      const white = state / 0x80000000;
      const shaped = white - prev * brightness;
      prev = white;
      out[start + i] += shaped * Math.exp((-decay * i) / RATE) * gain * 0.7;
    }
  };
}

/** A few harmonics at `freq`; more harmonics sound brighter. */
function tone(freq: number, decay: number, harmonics: number): VoiceFn {
  return (out, start, length, gain) => {
    const n = Math.min(length, out.length - start);
    for (let i = 0; i < n; i++) {
      const t = i / RATE;
      let v = 0;
      for (let h = 1; h <= harmonics; h++) v += Math.sin(TAU * freq * h * t) / h;
      // 3 ms attack avoids a click at the note start.
      const attack = Math.min(1, t / 0.003);
      out[start + i] += v * attack * Math.exp(-decay * t) * gain * 0.6;
    }
  };
}

function scale(key: string): number[] {
  const minor = parseKey(key)?.minor ?? true;
  return minor ? [0, 3, 5, 7, 10, 12] : [0, 2, 4, 7, 9, 12];
}

function voiceFor(s: MockSample, rng: Rng, degree = 0): VoiceFn {
  const semis = scale(s.key)[degree % 6];
  const pitch = (octave: number) => rootFrequency(s.key || "C", octave) * Math.pow(2, semis / 12);
  switch (s.voice) {
    case "kick":
      return kick(rng);
    case "snare":
      return noise(rng, 18 + rng.next() * 14, 0.2);
    case "hat":
      return noise(rng, s.tags.includes("drums/hat/closed") ? 60 : 9, 0.95);
    case "perc":
      return tone(180 + rng.next() * 400, 22, 2);
    case "bass":
      return tone(pitch(1), 3, 5);
    case "pad":
      return tone(pitch(3), 0.4, 3);
    case "noise":
      return noise(rng, 0.6, 0.5);
    default:
      return tone(pitch(3), 5, 4);
  }
}

function renderOneShot(s: MockSample, rng: Rng, out: Float32Array): void {
  voiceFor(s, rng)(out, 0, out.length, 1);
  if (s.voice === "pad" || s.voice === "noise") {
    // Swell in and out instead of a hard start.
    for (let i = 0; i < out.length; i++) out[i] *= Math.sin((Math.PI * i) / out.length);
  }
}

function renderLoop(s: MockSample, rng: Rng, out: Float32Array): void {
  const stepLen = Math.round((60 / s.bpm / 4) * RATE);
  const steps = Math.max(1, Math.round(out.length / stepLen));
  const drums = s.tags.includes("drums/loop") || !s.key;

  if (drums) {
    const kickVoice = kick(rng);
    const snareVoice = noise(rng, 22, 0.2);
    const hatVoice = noise(rng, 70, 0.95);
    const tops = /Top/.test(s.name);
    for (let step = 0; step < steps; step++) {
      const at = step * stepLen;
      const inBar = step % 16;
      if (!tops && (inBar % 4 === 0 || (inBar === 10 && rng.chance(0.4)))) kickVoice(out, at, stepLen * 3, 1);
      if (inBar === 4 || inBar === 12) snareVoice(out, at, stepLen * 3, 0.7);
      if (inBar % 2 === 0 || rng.chance(0.3)) hatVoice(out, at, stepLen, inBar % 4 === 2 ? 0.5 : 0.3);
    }
    return;
  }

  // One bar of rhythm, repeated, so the loop sounds intentional.
  const pattern = Array.from({ length: 16 }, (_, i) => (i === 0 || rng.chance(0.35) ? rng.int(0, 5) : -1));
  for (let step = 0; step < steps; step++) {
    const degree = pattern[step % 16];
    if (degree < 0) continue;
    voiceFor(s, rng, degree)(out, step * stepLen, stepLen * 4, 0.8);
  }
}

function normalise(out: Float32Array, peak: number): void {
  let max = 0;
  for (let i = 0; i < out.length; i++) max = Math.max(max, Math.abs(out[i]));
  if (max === 0) return;
  const k = peak / max;
  for (let i = 0; i < out.length; i++) out[i] *= k;
}

/** Synthesises the sample's audio. Deterministic per sample. */
export function render(s: MockSample): Rendered {
  const rng = new Rng(s.seed);
  const frames = Math.max(1, Math.round(Math.min(s.dur, MAX_SECONDS) * RATE));
  const left = new Float32Array(frames);
  if (s.type === "loop" && s.bpm > 0) renderLoop(s, rng, left);
  else renderOneShot(s, rng, left);
  normalise(left, 0.85);

  if (s.ch < 2) return { frames, rate: RATE, channels: [left] };
  // A 7 ms delayed copy on the right gives a cheap sense of width.
  const right = new Float32Array(frames);
  const delay = Math.round(0.007 * RATE);
  for (let i = 0; i < frames; i++) right[i] = i >= delay ? left[i - delay] * 0.9 : 0;
  return { frames, rate: RATE, channels: [left, right] };
}

/** Planar little-endian float32: all of channel 0, then channel 1. */
export function encodeF32(r: Rendered): ArrayBuffer {
  const out = new Float32Array(r.frames * r.channels.length);
  r.channels.forEach((ch, i) => out.set(ch, i * r.frames));
  return out.buffer;
}

/** 2000 x (int8 min, int8 max) over the mono mix. */
export function fullPeaks(r: Rendered): ArrayBuffer {
  const out = new Int8Array(FULL_PEAK_BUCKETS * 2);
  const mono = r.channels[0];
  for (let b = 0; b < FULL_PEAK_BUCKETS; b++) {
    const from = Math.floor((b * r.frames) / FULL_PEAK_BUCKETS);
    const to = Math.max(from + 1, Math.floor(((b + 1) * r.frames) / FULL_PEAK_BUCKETS));
    let min = 0;
    let max = 0;
    for (let i = from; i < to && i < r.frames; i++) {
      const v = mono[i];
      if (v < min) min = v;
      if (v > max) max = v;
    }
    out[b * 2] = Math.max(-127, Math.round(min * 127));
    out[b * 2 + 1] = Math.min(127, Math.round(max * 127));
  }
  return out.buffer;
}

/**
 * The row waveform: base64 of 96 uint8 buckets. Rendering every visible row's
 * audio would be far too slow, so this approximates the envelope the synth
 * produces from the same parameters.
 */
export function miniPeaks(s: MockSample): string {
  const rng = new Rng(s.seed ^ 0x5bd1e995);
  const bytes = new Uint8Array(MINI_PEAKS);
  const loop = s.type === "loop" && s.bpm > 0;
  const beats = loop ? Math.max(1, Math.round((s.dur * s.bpm) / 60)) : 0;
  const sustained = s.voice === "pad" || s.voice === "noise";
  const decay = s.voice === "hat" ? 14 : s.voice === "kick" ? 5 : 3.5;
  const accents = Array.from({ length: 16 }, () => 0.45 + rng.next() * 0.55);

  for (let i = 0; i < MINI_PEAKS; i++) {
    const x = i / MINI_PEAKS;
    let level: number;
    if (loop) {
      const pos = x * beats * 2;
      const phase = pos - Math.floor(pos);
      level = accents[Math.floor(pos) % 16] * (0.35 + 0.65 * Math.exp(-phase * 3));
    } else if (sustained) {
      level = Math.pow(Math.sin(Math.PI * Math.min(1, x + 0.02)), 0.6);
    } else {
      level = Math.exp(-x * decay);
    }
    level *= 0.85 + rng.next() * 0.15;
    bytes[i] = Math.max(2, Math.min(255, Math.round(level * 255)));
  }
  return btoa(String.fromCharCode(...bytes));
}
