// The matched-preview voice: one persistent Signalsmith Stretch worklet node
// that plays a sample at a different tempo and pitch.

import type { StretchNode } from "signalsmith-stretch";
import type { LoopRegion } from "./timeline";

/** Length of the fades that hide cuts, seconds. */
export const FADE = 0.005;

/**
 * Slack for the hop from the main thread to the audio thread. A change is
 * scheduled this far beyond the node's latency so the audio thread has not
 * already passed it, which would soften the start of the sample.
 */
const MESSAGE_SLACK = 0.015;

export interface StretchSource {
  id: string;
  buffer: AudioBuffer;
}

export interface StretchParams {
  /** Position in the sample to play from, seconds. */
  input: number;
  /** Sample seconds per second: 1.25 plays a 100 BPM loop at 125. */
  rate: number;
  semitones: number;
  loop: LoopRegion | null;
}

/** Freezes a parameter at its present value, discarding scheduled changes. */
export function hold(param: AudioParam, now: number): void {
  if (typeof param.cancelAndHoldAtTime === "function") {
    param.cancelAndHoldAtTime(now);
    return;
  }
  const value = param.value;
  param.cancelScheduledValues(now);
  param.setValueAtTime(value, now);
}

export class StretchVoice {
  /** Whether the node is producing sound (or about to). */
  active = false;
  private loadedId: string | null = null;

  private constructor(
    private readonly context: BaseAudioContext,
    private readonly node: StretchNode,
    private readonly gain: GainNode,
    /** Seconds from scheduling a change to hearing it. */
    readonly lead: number,
  ) {}

  static async create(context: BaseAudioContext, destination: AudioNode): Promise<StretchVoice> {
    // Loaded on demand: the module carries the WASM stretcher inline (~110 kB).
    const { default: SignalsmithStretch } = await import("signalsmith-stretch");
    // Default channel options (one unconnected input, stereo out): the
    // processor's idle path indexes its input list, so the input must exist.
    const node = await SignalsmithStretch(context);
    const gain = context.createGain();
    gain.gain.value = 0;
    node.connect(gain).connect(destination);
    // latency() is input plus output latency, which are equal; what is heard
    // at context time t is the input mapped to t, one output latency after
    // it was fed (measured, see e2e/harness.ts).
    const latency = await node.latency();
    return new StretchVoice(context, node, gain, latency / 2 + MESSAGE_SLACK);
  }

  /**
   * Starts a sample, cutting off whatever the node was playing. Returns the
   * context time at which `params.input` becomes audible; the output is
   * silent until then.
   */
  start(source: StretchSource, params: StretchParams): number {
    const now = this.context.currentTime;
    const at = now + this.lead;
    const g = this.gain.gain;
    hold(g, now);
    g.linearRampToValueAtTime(0, now + FADE);
    g.setValueAtTime(0, at - FADE);
    g.linearRampToValueAtTime(1, at);

    if (this.loadedId !== source.id) {
      void this.node.dropBuffers();
      // Structured clone copies the samples; the cache keeps the originals.
      const channels: Float32Array[] = [];
      for (let c = 0; c < source.buffer.numberOfChannels; c++) channels.push(source.buffer.getChannelData(c));
      void this.node.addBuffers(channels);
      this.loadedId = source.id;
    }
    // Reconfiguring resets the stretcher, which otherwise keeps sounding the
    // previous audio for one block (~120 ms) under the new sample.
    void this.node.configure({ preset: "default" });
    this.schedule(source, params, at);
    return at;
  }

  /**
   * Moves the playhead or changes rate, pitch or loop of the sample that is
   * already sounding; the stretcher blends into the new state by itself.
   * Returns the context time at which `params.input` becomes audible.
   */
  retime(source: StretchSource, params: StretchParams): number {
    const now = this.context.currentTime;
    const at = now + this.lead;
    const g = this.gain.gain;
    hold(g, now);
    g.linearRampToValueAtTime(1, now + FADE);
    this.schedule(source, params, at);
    return at;
  }

  /** Fades out and idles the node. */
  silence(): void {
    if (!this.active) return;
    const now = this.context.currentTime;
    const g = this.gain.gain;
    hold(g, now);
    g.linearRampToValueAtTime(0, now + FADE);
    void this.node.schedule({ active: false });
    this.active = false;
  }

  /** Releases the node's copy of the audio. */
  unload(): void {
    this.silence();
    void this.node.dropBuffers();
    this.loadedId = null;
  }

  private schedule(source: StretchSource, params: StretchParams, at: number): void {
    // The node reads its buffers at the context's sample rate. For a sample
    // at another rate, time runs `k` times faster inside the node and the
    // pitch is off by the same factor; both are folded into the request.
    const k = source.buffer.sampleRate / this.context.sampleRate;
    void this.node.schedule({
      active: true,
      output: at,
      input: params.input * k,
      rate: params.rate * k,
      semitones: params.semitones + 12 * Math.log2(k),
      loopStart: params.loop ? params.loop.start * k : 0,
      loopEnd: params.loop ? params.loop.end * k : 0,
    });
    this.active = true;
  }
}
