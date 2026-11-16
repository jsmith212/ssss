// In-browser checks of the audio engine against a synthetic backend that
// speaks the /audio/{id}.f32 wire format. Everything the engine plays is
// recorded from its output node and analysed, so the checks hear what a
// user would. Run through run.mjs (headless Chromium).

import { Engine, type EngineState, type PlayParams } from "../engine";
import type { FetchAudio } from "../loader";

interface Fixture {
  rate: number;
  channels: Float32Array[];
  loop?: [number, number];
  /** Artificial network delay, ms. */
  delay?: number;
}

const fixtures = new Map<string, Fixture>();
let fetchCount = 0;

const fetchAudio: FetchAudio = async (id, signal) => {
  fetchCount++;
  const f = fixtures.get(id);
  if (f?.delay) await sleep(f.delay);
  signal.throwIfAborted();
  if (!f) return new Response(JSON.stringify({ error: "no such sample" }), { status: 404 });
  const frames = f.channels[0].length;
  const body = new Float32Array(frames * f.channels.length);
  f.channels.forEach((ch, c) => body.set(ch, c * frames));
  const headers: Record<string, string> = {
    "X-Rate": String(f.rate),
    "X-Channels": String(f.channels.length),
    "X-Frames": String(frames),
    "Cache-Control": "no-store",
  };
  if (f.loop) {
    headers["X-Loop-Start"] = String(f.loop[0]);
    headers["X-Loop-End"] = String(f.loop[1]);
  }
  return new Response(body.buffer, { headers });
};

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function sine(freq: number, seconds: number, rate = 44100, amplitude = 0.5): Float32Array {
  const out = new Float32Array(Math.round(seconds * rate));
  for (let i = 0; i < out.length; i++) out[i] = amplitude * Math.sin((2 * Math.PI * freq * i) / rate);
  return out;
}

/** 40 ms tone bursts every `interval` seconds: a metronome. */
function bursts(interval: number, seconds: number, rate = 44100): Float32Array {
  const out = new Float32Array(Math.round(seconds * rate));
  const length = Math.round(0.04 * rate);
  for (let start = 0; start < out.length; start += Math.round(interval * rate)) {
    for (let i = 0; i < length && start + i < out.length; i++) {
      out[start + i] = 0.6 * Math.sin((2 * Math.PI * 1000 * i) / rate) * Math.sin((Math.PI * i) / length);
    }
  }
  return out;
}

// --- analysis -------------------------------------------------------------------

/** Amplitude of the `freq` component over [from, to) seconds. */
function tone(data: Float32Array, rate: number, freq: number, from = 0, to = data.length / rate): number {
  const a = Math.max(0, Math.round(from * rate));
  const b = Math.min(data.length, Math.round(to * rate));
  let re = 0;
  let im = 0;
  for (let i = a; i < b; i++) {
    // A Hann window keeps neighbouring test tones from leaking into each other.
    const w = 1 - Math.cos((2 * Math.PI * (i - a)) / (b - a));
    re += data[i] * w * Math.cos((2 * Math.PI * freq * i) / rate);
    im += data[i] * w * Math.sin((2 * Math.PI * freq * i) / rate);
  }
  return (2 * Math.hypot(re, im)) / Math.max(1, b - a);
}

function rms(data: Float32Array, rate: number, from = 0, to = data.length / rate): number {
  const a = Math.max(0, Math.round(from * rate));
  const b = Math.min(data.length, Math.round(to * rate));
  let sum = 0;
  for (let i = a; i < b; i++) sum += data[i] * data[i];
  return Math.sqrt(sum / Math.max(1, b - a));
}

/** Lowest RMS of any `window`-second stretch in [from, to). */
function quietest(data: Float32Array, rate: number, from: number, to: number, window = 0.01): number {
  let min = Number.POSITIVE_INFINITY;
  for (let t = from; t + window <= to; t += window / 2) min = Math.min(min, rms(data, rate, t, t + window));
  return min;
}

/** Times (seconds) at which the signal rises above `threshold` after a quiet spell. */
function onsets(data: Float32Array, rate: number, threshold: number): number[] {
  const hop = Math.round(0.002 * rate);
  const found: number[] = [];
  let quiet = true;
  for (let i = 0; i + hop <= data.length; i += hop) {
    let peak = 0;
    for (let j = i; j < i + hop; j++) peak = Math.max(peak, Math.abs(data[j]));
    if (quiet && peak > threshold) {
      found.push(i / rate);
      quiet = false;
    } else if (!quiet && peak < threshold / 4) {
      quiet = true;
    }
  }
  return found;
}

// --- rig -------------------------------------------------------------------------

class Rig {
  readonly engine: Engine;
  readonly states: EngineState[] = [];
  stretchFailure: unknown = null;
  private chunks: Float32Array[] | null = null;
  private recordedFrom = 0;
  private readonly tap: ScriptProcessorNode;

  constructor() {
    this.engine = new Engine({
      fetchAudio,
      onChange: (s) => this.states.push(s),
      onStretchUnavailable: (reason) => (this.stretchFailure = reason),
    });
    const ctx = this.engine.context;
    // Deprecated, but the simplest way to capture a real-time context's
    // output sample for sample; fine for a test rig.
    this.tap = ctx.createScriptProcessor(1024, 2, 1);
    this.tap.onaudioprocess = (e) => {
      if (!this.chunks) return;
      const left = e.inputBuffer.getChannelData(0);
      const right = e.inputBuffer.getChannelData(1);
      const mono = new Float32Array(left.length);
      for (let i = 0; i < mono.length; i++) mono[i] = (left[i] + right[i]) / 2;
      this.chunks.push(mono);
    };
    this.engine.output.connect(this.tap);
    this.tap.connect(ctx.destination);
  }

  get rate(): number {
    return this.engine.context.sampleRate;
  }

  get now(): number {
    return this.engine.context.currentTime;
  }

  async ready(): Promise<void> {
    this.engine.unlock();
    const ctx = this.engine.context;
    for (let i = 0; i < 100 && ctx.state !== "running"; i++) await sleep(20);
    if (ctx.state !== "running") throw new Error(`AudioContext is ${ctx.state}`);
  }

  record(): void {
    this.chunks = [];
    this.recordedFrom = this.now;
  }

  /** Stops recording; returns the audio and the context time it began at (to within a block). */
  take(): { data: Float32Array; from: number } {
    const chunks = this.chunks ?? [];
    this.chunks = null;
    const data = new Float32Array(chunks.reduce((n, c) => n + c.length, 0));
    let at = 0;
    for (const c of chunks) {
      data.set(c, at);
      at += c.length;
    }
    return { data, from: this.recordedFrom };
  }

  async capture(seconds: number): Promise<Float32Array> {
    this.record();
    await sleep(seconds * 1000);
    return this.take().data;
  }

  /** Stops and waits for fades and the stretcher's tail to die away. */
  async quiet(): Promise<void> {
    this.engine.stop();
    await sleep(250);
  }
}

const PLAIN: PlayParams = { loop: false, rate: 1, semitones: 0 };
const LOOP: PlayParams = { loop: true, rate: 1, semitones: 0 };

interface Check {
  name: string;
  ok: boolean;
  detail: Record<string, unknown>;
}

const round = (n: number, digits = 3) => Number(n.toFixed(digits));

type CheckFn = (rig: Rig) => Promise<{ ok: boolean; detail: Record<string, unknown> }>;
const checks: [string, CheckFn][] = [];
const check = (name: string, fn: CheckFn) => checks.push([name, fn]);

check("wire format becomes an AudioBuffer", async (rig) => {
  const left = sine(100, 0.25, 22050);
  const right = sine(300, 0.25, 22050, 0.25);
  fixtures.set("wire", { rate: 22050, channels: [left, right], loop: [1000, 4000] });
  const loaded = await rig.engine.loader.load("wire");
  const b = loaded.buffer;
  const same = (a: Float32Array, c: Float32Array) => a.length === c.length && a.every((v, i) => v === c[i]);
  const detail = {
    sampleRate: b.sampleRate,
    channels: b.numberOfChannels,
    length: b.length,
    leftExact: same(b.getChannelData(0), left),
    rightExact: same(b.getChannelData(1), right),
    fileLoop: loaded.fileLoop,
  };
  const ok =
    b.sampleRate === 22050 &&
    b.numberOfChannels === 2 &&
    b.length === left.length &&
    detail.leftExact &&
    detail.rightExact &&
    Math.abs((loaded.fileLoop?.start ?? 0) - 1000 / 22050) < 1e-9 &&
    Math.abs((loaded.fileLoop?.end ?? 0) - 4000 / 22050) < 1e-9;
  return { ok, detail };
});

check("plain playback sounds and the playhead advances", async (rig) => {
  fixtures.set("a440", { rate: 44100, channels: [sine(440, 3)] });
  await rig.engine.play("a440", PLAIN);
  const t0 = rig.now;
  const p0 = rig.engine.position();
  const data = await rig.capture(0.6);
  const elapsed = rig.now - t0;
  const advanced = rig.engine.position() - p0;
  const detail = {
    state: rig.engine.snapshot,
    elapsed: round(elapsed),
    advanced: round(advanced),
    amp440: round(tone(data, rig.rate, 440, 0.1, 0.5)),
    amp880: round(tone(data, rig.rate, 880, 0.1, 0.5)),
    voices: rig.engine.activeVoices(),
  };
  const ok =
    rig.engine.snapshot.playing &&
    Math.abs(rig.engine.snapshot.duration - 3) < 1e-3 &&
    Math.abs(advanced - elapsed) < 0.005 &&
    elapsed > 0.5 &&
    Math.abs(detail.amp440 - 0.5) < 0.05 &&
    detail.amp880 < 0.01 &&
    detail.voices === 1;
  await rig.quiet();
  return { ok, detail };
});

check("a one-shot ends by itself", async (rig) => {
  fixtures.set("short", { rate: 44100, channels: [sine(500, 0.3)] });
  await rig.engine.play("short", PLAIN);
  const playingAtStart = rig.engine.snapshot.playing;
  await sleep(500);
  const detail = { playingAtStart, state: rig.engine.snapshot, voices: rig.engine.activeVoices(), position: rig.engine.position() };
  const ok = playingAtStart && !rig.engine.snapshot.playing && rig.engine.snapshot.id === "short" && detail.voices === 0 && detail.position === 0;
  return { ok, detail };
});

check("loop wraps", async (rig) => {
  fixtures.set("loop", { rate: 44100, channels: [sine(330, 0.5)] });
  await rig.engine.play("loop", LOOP);
  const positions: number[] = [];
  rig.record();
  for (let i = 0; i < 45; i++) {
    positions.push(rig.engine.position());
    await sleep(30);
  }
  const { data } = rig.take();
  const wraps = positions.filter((p, i) => i > 0 && p < positions[i - 1]).length;
  const detail = {
    wraps,
    maxPosition: round(Math.max(...positions)),
    playing: rig.engine.snapshot.playing,
    voices: rig.engine.activeVoices(),
    quietest: round(quietest(data, rig.rate, 0.05, 1.2, 0.02)),
  };
  // 1.35 s of a 0.5 s loop: two wraps, never past the end, sound throughout.
  const ok = wraps === 2 && detail.maxPosition <= 0.5 && detail.playing && detail.voices === 1 && detail.quietest > 0.3;
  await rig.quiet();
  return { ok, detail };
});

check("file loop points are honoured", async (rig) => {
  // Lead-in at 300 Hz, loop region [0.4 s, 0.6 s) at 600 Hz, tail at 900 Hz.
  const rate = 44100;
  const data = new Float32Array(rate);
  for (let i = 0; i < data.length; i++) {
    const t = i / rate;
    const f = t < 0.4 ? 300 : t < 0.6 ? 600 : 900;
    data[i] = 0.5 * Math.sin(2 * Math.PI * f * t);
  }
  fixtures.set("smpl", { rate, channels: [data], loop: [0.4 * rate, 0.6 * rate] });
  await rig.engine.play("smpl", LOOP);
  await sleep(700);
  const positions: number[] = [];
  rig.record();
  for (let i = 0; i < 15; i++) {
    positions.push(rig.engine.position());
    await sleep(30);
  }
  const rec = rig.take().data;
  const detail = {
    min: round(Math.min(...positions)),
    max: round(Math.max(...positions)),
    amp600: round(tone(rec, rig.rate, 600, 0.05, 0.4)),
    amp900: round(tone(rec, rig.rate, 900, 0.05, 0.4)),
  };
  const ok = detail.min >= 0.4 && detail.max <= 0.6 && detail.amp600 > 0.4 && detail.amp900 < 0.02;
  // Loop off: the playhead leaves the region and the sample runs out.
  rig.engine.update(PLAIN);
  await sleep(800);
  const ended = !rig.engine.snapshot.playing;
  return { ok: ok && ended, detail: { ...detail, endedAfterLoopOff: ended } };
});

check("seek moves the playhead", async (rig) => {
  await rig.engine.play("a440", PLAIN);
  await sleep(100);
  rig.engine.seek(2);
  const justAfter = rig.engine.position();
  await sleep(200);
  const later = rig.engine.position();
  const detail = { justAfter: round(justAfter), later: round(later), voices: rig.engine.activeVoices() };
  const ok = Math.abs(justAfter - 2) < 0.02 && later > 2.15 && later < 2.3 && detail.voices === 1;
  await rig.quiet();
  return { ok, detail };
});

check("starting from an offset while stopped", async (rig) => {
  await rig.engine.play("a440", PLAIN, 1.5);
  const p = rig.engine.position();
  await rig.quiet();
  return { ok: Math.abs(p - 1.5) < 0.02, detail: { position: round(p) } };
});

check("stretch shifts pitch by the requested semitones", async (rig) => {
  // 44.1 kHz material in a context that may run at another rate: the node
  // has to compensate, or the pitch lands somewhere else.
  fixtures.set("a220", { rate: 44100, channels: [sine(220, 4)] });
  await rig.engine.play("a220", { loop: false, rate: 1, semitones: 12 });
  const data = await rig.capture(0.9);
  const up = { amp440: round(tone(data, rig.rate, 440, 0.4, 0.85)), amp220: round(tone(data, rig.rate, 220, 0.4, 0.85)) };
  rig.engine.update({ loop: false, rate: 1, semitones: -5 });
  const data2 = await rig.capture(0.9);
  const target = 220 * Math.pow(2, -5 / 12);
  const down = { ampTarget: round(tone(data2, rig.rate, target, 0.4, 0.85)), amp440: round(tone(data2, rig.rate, 440, 0.4, 0.85)) };
  const detail = { contextRate: rig.rate, up, down, voices: rig.engine.activeVoices(), stretchFailure: String(rig.stretchFailure) };
  const ok = up.amp440 > 0.35 && up.amp220 < 0.03 && down.ampTarget > 0.35 && down.amp440 < 0.03 && detail.voices === 1 && !rig.stretchFailure;
  await rig.quiet();
  return { ok, detail };
});

check("stretch changes tempo without changing pitch", async (rig) => {
  // A 120 BPM click (0.5 s apart) played at 1.25x must click every 0.4 s,
  // still at 1 kHz.
  fixtures.set("click", { rate: 44100, channels: [bursts(0.5, 6)] });
  await rig.engine.play("click", { loop: false, rate: 1.25, semitones: 0 });
  const t0 = rig.now;
  const p0 = rig.engine.position();
  const data = await rig.capture(2.4);
  const playheadRate = (rig.engine.position() - p0) / (rig.now - t0);
  const times = onsets(data, rig.rate, 0.15);
  const gaps = times.slice(1).map((t, i) => round(t - times[i]));
  const first = times[1] ?? 0;
  const detail = {
    onsets: times.map((t) => round(t)),
    gaps,
    playheadRate: round(playheadRate),
    amp1000: round(tone(data, rig.rate, 1000, first, first + 0.03), 4),
    amp1250: round(tone(data, rig.rate, 1250, first, first + 0.03), 4),
  };
  const ok =
    gaps.length >= 4 &&
    gaps.every((g) => Math.abs(g - 0.4) < 0.015) &&
    // The playhead waits for the stretcher's lead-in, so it averages a touch slow.
    Math.abs(playheadRate - 1.25) < 0.06 &&
    detail.amp1000 > 4 * detail.amp1250;
  await rig.quiet();
  return { ok, detail };
});

check("stretch loops and its playhead wraps", async (rig) => {
  await rig.engine.play("loop", { loop: true, rate: 0.8, semitones: 3 });
  const positions: number[] = [];
  rig.record();
  for (let i = 0; i < 50; i++) {
    positions.push(rig.engine.position());
    await sleep(30);
  }
  const { data } = rig.take();
  const wraps = positions.filter((p, i) => i > 0 && p < positions[i - 1]).length;
  const target = 330 * Math.pow(2, 3 / 12);
  const detail = {
    wraps,
    maxPosition: round(Math.max(...positions)),
    ampTarget: round(tone(data, rig.rate, target, 0.3, 1.4)),
    playing: rig.engine.snapshot.playing,
  };
  // 1.5 s at 0.8x covers 1.2 s of a 0.5 s loop: two wraps.
  const ok = wraps === 2 && detail.maxPosition <= 0.5 && detail.ampTarget > 0.25 && detail.playing;
  await rig.quiet();
  return { ok, detail };
});

check("a stretched one-shot ends by itself", async (rig) => {
  await rig.engine.play("short", { loop: false, rate: 1, semitones: 2 });
  await sleep(700);
  const detail = { state: rig.engine.snapshot, voices: rig.engine.activeVoices() };
  return { ok: !rig.engine.snapshot.playing && detail.voices === 0, detail };
});

check("switching between plain and matched is seamless", async (rig) => {
  fixtures.set("a220loop", { rate: 44100, channels: [sine(220, 2)] });
  await rig.engine.play("a220loop", LOOP);
  rig.record();
  await sleep(400);
  const before = rig.engine.position();
  rig.engine.update({ loop: true, rate: 1, semitones: 12 });
  const toStretch = rig.now;
  await sleep(600);
  rig.engine.update(LOOP);
  const toPlain = rig.now;
  await sleep(400);
  const after = rig.engine.position();
  const { data, from } = rig.take();
  const a = toStretch - from;
  const b = toPlain - from;
  const detail = {
    quietestAroundToStretch: round(quietest(data, rig.rate, a - 0.05, a + 0.25)),
    quietestAroundToPlain: round(quietest(data, rig.rate, b - 0.05, b + 0.25)),
    amp220Before: round(tone(data, rig.rate, 220, 0.1, a - 0.02)),
    amp440During: round(tone(data, rig.rate, 440, a + 0.2, b - 0.02)),
    amp220During: round(tone(data, rig.rate, 220, a + 0.2, b - 0.02)),
    amp220After: round(tone(data, rig.rate, 220, b + 0.1, b + 0.35)),
    amp440After: round(tone(data, rig.rate, 440, b + 0.1, b + 0.35)),
    // One second later in a 2 s loop.
    playheadTravel: round((((after - before) % 2) + 2) % 2),
    voices: rig.engine.activeVoices(),
  };
  // A steady 0.5 sine has an RMS of 0.354; no 10 ms window may drop far below it.
  const ok =
    detail.quietestAroundToStretch > 0.2 &&
    detail.quietestAroundToPlain > 0.2 &&
    detail.amp220Before > 0.45 &&
    detail.amp440During > 0.35 &&
    detail.amp220During < 0.03 &&
    detail.amp220After > 0.45 &&
    detail.amp440After < 0.03 &&
    Math.abs(detail.playheadTravel - 1) < 0.06 &&
    detail.voices === 1;
  await rig.quiet();
  return { ok, detail };
});

/** 50 distinct tones, 60 Hz apart; every other one is cached, the last one is not. */
async function retriggerSet(rig: Rig, prefix: string): Promise<{ ids: string[]; freqs: number[] }> {
  const ids: string[] = [];
  const freqs: number[] = [];
  for (let i = 0; i < 50; i++) {
    const id = `${prefix}${i}`;
    const freq = 400 + i * 60;
    fixtures.set(id, { rate: 44100, channels: [sine(freq, 2)], delay: i % 2 ? 5 + (i % 7) * 4 : 0 });
    ids.push(id);
    freqs.push(freq);
  }
  const cached = ids.filter((_, i) => i % 2 === 0);
  await Promise.all(cached.map((id) => rig.engine.loader.load(id).catch(() => {})));
  // The loader keeps only the newest load in flight; make sure every other one landed.
  for (const id of cached) {
    while (!rig.engine.loader.peek(id)) await rig.engine.loader.load(id);
  }
  return { ids, freqs };
}

async function retrigger(rig: Rig, prefix: string, params: (i: number) => PlayParams, shift: number) {
  const { ids, freqs } = await retriggerSet(rig, prefix);
  const before = fetchCount;
  const startStates = rig.states.length;
  rig.record();
  for (let i = 0; i < ids.length; i++) {
    void rig.engine.play(ids[i], params(i));
    // Faster than key repeat; some requests land in the same task as the previous one's fetch.
    await sleep(i % 3 === 0 ? 0 : 4);
  }
  await sleep(500);
  const voices = rig.engine.activeVoices();
  const inFlight = rig.engine.loader.inFlight;
  await sleep(300);
  const { data } = rig.take();
  const end = data.length / rig.rate;
  const factor = Math.pow(2, shift / 12);
  const amps = freqs.map((f) => tone(data, rig.rate, f * factor, end - 0.25, end - 0.02));
  const last = amps[amps.length - 1];
  const loudestOther = Math.max(...amps.slice(0, -1));
  const playingStates = rig.states.slice(startStates).filter((s) => s.playing).map((s) => s.id);
  const detail = {
    voices,
    inFlight,
    state: rig.engine.snapshot,
    lastAmp: round(last),
    loudestOtherAmp: round(loudestOther, 4),
    fetchesStarted: fetchCount - before,
    startedPlaying: playingStates.length,
  };
  const ok =
    voices === 1 &&
    inFlight.length === 0 &&
    rig.engine.snapshot.id === ids[ids.length - 1] &&
    rig.engine.snapshot.playing &&
    last > 0.3 &&
    loudestOther < 0.02;
  await rig.quiet();
  return { ok, detail };
}

check("50 rapid retriggers leave one plain voice", (rig) => retrigger(rig, "p", () => LOOP, 0));

check("50 rapid retriggers leave one matched voice", (rig) =>
  retrigger(rig, "m", () => ({ loop: true, rate: 1.1, semitones: 2 }), 2));

check("50 rapid retriggers alternating plain and matched", (rig) =>
  retrigger(rig, "x", (i) => (i % 2 ? LOOP : { loop: true, rate: 1, semitones: 2 }), 0));

check("stop silences everything", async (rig) => {
  await rig.engine.play("a220loop", { loop: true, rate: 1, semitones: 7 });
  await sleep(300);
  await rig.engine.play("a440", PLAIN);
  await sleep(100);
  rig.engine.stop();
  await sleep(60);
  const data = await rig.capture(0.3);
  const detail = { rms: round(rms(data, rig.rate), 5), voices: rig.engine.activeVoices(), state: rig.engine.snapshot };
  return { ok: detail.rms < 1e-4 && detail.voices === 0 && !detail.state.playing, detail };
});

check("volume scales the output", async (rig) => {
  await rig.engine.play("a440", PLAIN);
  rig.engine.setVolume(0.25);
  await sleep(150);
  const data = await rig.capture(0.3);
  rig.engine.setVolume(1);
  const amp = round(tone(data, rig.rate, 440, 0.05, 0.25));
  await rig.quiet();
  return { ok: Math.abs(amp - 0.125) < 0.01, detail: { amp } };
});

check("a missing sample reports an error and stays silent", async (rig) => {
  await rig.engine.play("a440", PLAIN);
  await rig.engine.play("does-not-exist", PLAIN);
  await sleep(50);
  const s = rig.engine.snapshot;
  const detail = { state: s, voices: rig.engine.activeVoices() };
  const ok = s.id === "does-not-exist" && !s.playing && !s.loading && s.error === "no such sample" && detail.voices === 0;
  return { ok, detail };
});

check("start latency", async (rig) => {
  // Time from play() to the first audible sample, for a cached sample.
  // Includes up to one recorder block (21 ms) of measurement slack.
  const measure = async (params: PlayParams) => {
    await rig.quiet();
    rig.record();
    await sleep(100);
    const asked = rig.now;
    void rig.engine.play("a440", params);
    await sleep(400);
    const { data, from } = rig.take();
    const first = onsets(data, rig.rate, 0.05)[0] ?? Number.NaN;
    return round(first - (asked - from));
  };
  const plain = await measure(PLAIN);
  const matched = await measure({ loop: false, rate: 1, semitones: 3 });
  await rig.quiet();
  return { ok: plain < 0.05 && matched < 0.15, detail: { plainSeconds: plain, matchedSeconds: matched } };
});

async function runAll(): Promise<{ ok: boolean; checks: Check[] }> {
  const rig = new Rig();
  const results: Check[] = [];
  try {
    await rig.ready();
    for (const [name, fn] of checks) {
      try {
        const { ok, detail } = await fn(rig);
        results.push({ name, ok, detail });
      } catch (err) {
        results.push({ name, ok: false, detail: { threw: err instanceof Error ? (err.stack ?? err.message) : String(err) } });
        await rig.quiet().catch(() => {});
      }
    }
  } catch (err) {
    results.push({ name: "setup", ok: false, detail: { threw: String(err) } });
  }
  await rig.engine.dispose().catch(() => {});
  return { ok: results.every((r) => r.ok), checks: results };
}

declare global {
  interface Window {
    __audioChecks?: Promise<{ ok: boolean; checks: Check[] }>;
  }
}

window.__audioChecks = runAll();
