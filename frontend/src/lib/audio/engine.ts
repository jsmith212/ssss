// Playback engine: one AudioContext, a plain voice (AudioBufferSourceNode)
// for untouched preview and the stretch voice for tempo/key matched preview.
// Every request carries a token; whatever was asked for last wins, and work
// for earlier requests is dropped when it completes.

import { BufferLoader, isAbort, type FetchAudio } from "./loader";
import { FADE, hold, StretchVoice } from "./stretch";
import { endTime, positionAt, type LoopRegion, type Timeline } from "./timeline";
import type { Pcm } from "./wire";

export interface Loaded {
  id: string;
  buffer: AudioBuffer;
  /** Loop points from the file itself (smpl chunk), seconds. */
  fileLoop: LoopRegion | null;
}

export interface PlayParams {
  loop: boolean;
  /** 1 = original tempo. */
  rate: number;
  /** 0 = original pitch. */
  semitones: number;
}

export interface EngineState {
  /** The sample that is loaded for playback, playing or not. */
  id: string | null;
  playing: boolean;
  /** Waiting for the audio to arrive. */
  loading: boolean;
  /** Seconds; 0 until the audio is loaded. */
  duration: number;
  /** Why the sample cannot be played; "" when fine. */
  error: string;
}

export interface EngineOptions {
  fetchAudio: FetchAudio;
  onChange?: (state: EngineState) => void;
  /** Called once if the stretcher cannot be set up; matched preview then plays unmatched. */
  onStretchUnavailable?: (reason: unknown) => void;
  maxEntries?: number;
  maxBytes?: number;
}

interface PlainVoice {
  source: AudioBufferSourceNode;
  gain: GainNode;
  /** A stop has been scheduled; the voice is on its way out. */
  stopping: boolean;
}

interface Current {
  loaded: Loaded;
  params: PlayParams;
  timeline: Timeline;
  /** The plain voice carrying the sample; null while the stretch voice does. */
  plain: PlainVoice | null;
}

function buildBuffer(id: string, pcm: Pcm): Loaded {
  const buffer = new AudioBuffer({
    length: pcm.frames,
    numberOfChannels: pcm.channels.length,
    sampleRate: pcm.rate,
  });
  // The cast narrows the array's buffer type for copyToChannel's signature.
  pcm.channels.forEach((data, c) => buffer.copyToChannel(data as Float32Array<ArrayBuffer>, c));
  const fileLoop =
    pcm.loopStart !== undefined && pcm.loopEnd !== undefined
      ? { start: pcm.loopStart / pcm.rate, end: pcm.loopEnd / pcm.rate }
      : null;
  return { id, buffer, fileLoop };
}

function needsStretch(p: PlayParams): boolean {
  return Math.abs(p.rate - 1) > 1e-4 || p.semitones !== 0;
}

function loopRegion(loaded: Loaded, loop: boolean): LoopRegion | null {
  if (!loop) return null;
  return loaded.fileLoop ?? { start: 0, end: loaded.buffer.duration };
}

export class Engine {
  readonly loader: BufferLoader<Loaded>;
  private ctx: AudioContext | null = null;
  private master: GainNode | null = null;
  private volume = 1;
  private token = 0;
  private current: Current | null = null;
  /**
   * Settings for the play() that is still fetching its audio. update() keeps
   * them current, so a toggle pressed meanwhile is not lost.
   */
  private pending: PlayParams | null = null;
  private readonly voices = new Set<PlainVoice>();
  private stretch: StretchVoice | null = null;
  private stretchSetup: Promise<StretchVoice | null> | null = null;
  private endTimer: ReturnType<typeof setTimeout> | undefined;
  private state: EngineState = { id: null, playing: false, loading: false, duration: 0, error: "" };

  constructor(private readonly options: EngineOptions) {
    this.loader = new BufferLoader<Loaded>({
      fetchAudio: options.fetchAudio,
      build: buildBuffer,
      maxEntries: options.maxEntries,
      maxBytes: options.maxBytes,
    });
  }

  /** The context, created on first use so nothing happens before a gesture. */
  get context(): AudioContext {
    if (!this.ctx) {
      this.ctx = new AudioContext({ latencyHint: "interactive" });
      this.master = this.ctx.createGain();
      this.master.gain.value = this.volume;
      this.master.connect(this.ctx.destination);
    }
    return this.ctx;
  }

  /** Everything audible passes through this node. */
  get output(): GainNode {
    void this.context;
    return this.master!;
  }

  get snapshot(): EngineState {
    return this.state;
  }

  /**
   * Call from a user gesture: webviews keep the context suspended until
   * one. Also warms up the stretcher so the first matched preview is quick.
   */
  unlock(): void {
    const ctx = this.context;
    if (ctx.state === "suspended") void ctx.resume().catch(() => {});
    void this.stretchVoice();
  }

  /** Linear gain 0..1 applied to everything. */
  setVolume(gain: number): void {
    this.volume = Math.min(1, Math.max(0, gain));
    if (this.ctx && this.master) {
      this.master.gain.setTargetAtTime(this.volume, this.ctx.currentTime, 0.01);
    }
  }

  prefetch(ids: string[]): void {
    this.loader.prefetch(ids);
  }

  /**
   * Plays a sample from `offset` seconds, replacing whatever is sounding.
   * A cached sample starts within this call. Resolves once playback has
   * started, failed, or been superseded by a later request.
   */
  async play(id: string, params: PlayParams, offset = 0): Promise<void> {
    const token = ++this.token;
    this.unlock();
    this.pending = params;

    let loaded = this.loader.peek(id);
    if (loaded) {
      this.loader.focus(id);
    } else {
      // Cut the old sound now instead of when the new audio arrives.
      this.silence();
      this.setState({ id, playing: false, loading: true, duration: 0, error: "" });
      try {
        loaded = await this.loader.load(id);
      } catch (err) {
        if (token === this.token) {
          this.pending = null;
          if (!isAbort(err)) {
            this.setState({ id, playing: false, loading: false, duration: 0, error: message(err) });
          }
        }
        return;
      }
      if (token !== this.token) return;
    }

    // The token is still ours, so `pending` is this call's params or what
    // update() replaced them with while the audio was loading.
    let stretch: StretchVoice | null = null;
    if (needsStretch(this.pending ?? params)) {
      stretch = this.stretch;
      if (!stretch) {
        this.silence();
        stretch = await this.stretchVoice();
        if (token !== this.token) return;
      }
    }
    const latest = this.pending ?? params;
    this.pending = null;
    this.begin(loaded, latest, offset, needsStretch(latest) ? stretch : null);
  }

  /**
   * Applies new loop, tempo or pitch settings to the sample that is playing,
   * without a gap: the playhead carries on from where it is.
   */
  update(params: PlayParams): void {
    const cur = this.current;
    if (!cur) {
      // Nothing sounds yet; a play() that is loading starts with these.
      if (this.pending) this.pending = params;
      return;
    }
    const before = cur.params;
    if (before.loop === params.loop && before.rate === params.rate && before.semitones === params.semitones) return;

    const ctx = this.context;
    const loop = loopRegion(cur.loaded, params.loop);
    const duration = cur.loaded.buffer.duration;
    const wantStretch = needsStretch(params);

    if (wantStretch && !this.stretch) {
      // Keep playing as is and switch over once the stretcher exists.
      const token = this.token;
      void this.stretchVoice().then((voice) => {
        if (token !== this.token || this.current !== cur) return;
        // No stretcher: the loop setting is all that can be honoured.
        this.update(voice ? params : { loop: params.loop, rate: 1, semitones: 0 });
      });
      return;
    }

    if (wantStretch && this.stretch) {
      const stretch = this.stretch;
      const at = ctx.currentTime + stretch.lead;
      // Where the sample will be when the stretched sound comes in.
      const input = positionAt(cur.timeline, at);
      const request = { input, rate: params.rate, semitones: params.semitones, loop };
      if (cur.plain) {
        const audibleAt = stretch.start(cur.loaded, request);
        this.stopPlain(cur.plain, audibleAt - FADE);
        cur.plain = null;
        cur.timeline = { at: audibleAt, input, rate: params.rate, loop, duration };
      } else {
        const audibleAt = stretch.retime(cur.loaded, request);
        cur.timeline = { at: audibleAt, input, rate: params.rate, loop, duration };
      }
    } else {
      const now = ctx.currentTime;
      const input = positionAt(cur.timeline, now);
      if (cur.plain) {
        applyLoop(cur.plain.source, loop);
      } else {
        this.stretch?.silence();
        cur.plain = this.startPlain(cur.loaded, input, loop, true);
      }
      cur.timeline = { at: now, input, rate: 1, loop, duration };
    }
    cur.params = { ...params };
    this.armEnd();
  }

  /** Moves the playhead of the playing sample. */
  seek(seconds: number): void {
    const cur = this.current;
    if (!cur) return;
    const duration = cur.loaded.buffer.duration;
    const input = clampOffset(seconds, duration);
    const loop = cur.timeline.loop;
    if (cur.plain) {
      this.stopPlain(cur.plain);
      cur.plain = this.startPlain(cur.loaded, input, loop, true);
      cur.timeline = { at: this.context.currentTime, input, rate: 1, loop, duration };
    } else if (this.stretch) {
      const { rate, semitones } = cur.params;
      const at = this.stretch.retime(cur.loaded, { input, rate, semitones, loop });
      cur.timeline = { at, input, rate, loop, duration };
    }
    this.armEnd();
  }

  /** Stops playback; the sample stays loaded as the current one. */
  stop(): void {
    this.token++;
    this.pending = null;
    this.loader.focus(null);
    this.silence();
    if (this.state.playing || this.state.loading) {
      this.setState({ ...this.state, playing: false, loading: false });
    }
  }

  /** Playhead position in the current sample, seconds; 0 when stopped. */
  position(): number {
    if (!this.current || !this.ctx) return 0;
    return positionAt(this.current.timeline, this.ctx.currentTime);
  }

  /** Number of voices that are sounding or still fading out. */
  activeVoices(): number {
    return this.voices.size + (this.stretch?.active ? 1 : 0);
  }

  /** Stops everything and releases the audio device. */
  async dispose(): Promise<void> {
    this.stop();
    this.loader.clear();
    const ctx = this.ctx;
    this.ctx = null;
    this.master = null;
    this.stretch = null;
    this.stretchSetup = null;
    if (ctx && ctx.state !== "closed") await ctx.close();
  }

  // --- internals ---------------------------------------------------------------

  private begin(loaded: Loaded, params: PlayParams, offset: number, stretch: StretchVoice | null): void {
    const ctx = this.context;
    const duration = loaded.buffer.duration;
    const loop = loopRegion(loaded, params.loop);
    const input = clampOffset(offset, duration);

    this.fadeOutPlain();
    let timeline: Timeline;
    let plain: PlainVoice | null = null;
    if (stretch) {
      const { rate, semitones } = params;
      const at = stretch.start(loaded, { input, rate, semitones, loop });
      timeline = { at, input, rate, loop, duration };
    } else {
      this.stretch?.silence();
      // A fade-in would dull the attack of a sample played from its start.
      plain = this.startPlain(loaded, input, loop, input > 0);
      timeline = { at: ctx.currentTime, input, rate: 1, loop, duration };
    }
    // Without a stretcher the sample plays unmatched; remember what was
    // asked for so a later update() can still switch over.
    this.current = { loaded, params: { ...params }, timeline, plain };
    this.armEnd();
    this.setState({ id: loaded.id, playing: true, loading: false, duration, error: "" });
  }

  private startPlain(loaded: Loaded, offset: number, loop: LoopRegion | null, fadeIn: boolean): PlainVoice {
    const ctx = this.context;
    const now = ctx.currentTime;
    const source = ctx.createBufferSource();
    source.buffer = loaded.buffer;
    applyLoop(source, loop);
    const gain = ctx.createGain();
    if (fadeIn) {
      gain.gain.setValueAtTime(0, now);
      gain.gain.linearRampToValueAtTime(1, now + FADE);
    }
    source.connect(gain).connect(this.output);

    const voice: PlainVoice = { source, gain, stopping: false };
    this.voices.add(voice);
    source.onended = () => {
      this.voices.delete(voice);
      gain.disconnect();
      if (this.current?.plain === voice) this.finish();
    };
    source.start(now, offset);
    return voice;
  }

  /** Fades a plain voice out over FADE, starting at `when` (default now). */
  private stopPlain(voice: PlainVoice, when?: number): void {
    if (voice.stopping) return;
    voice.stopping = true;
    const now = this.context.currentTime;
    const start = Math.max(now, when ?? now);
    const g = voice.gain.gain;
    if (start > now) {
      g.setValueAtTime(1, start);
    } else {
      hold(g, now);
    }
    g.linearRampToValueAtTime(0, start + FADE);
    voice.source.stop(start + FADE);
  }

  private fadeOutPlain(): void {
    for (const voice of this.voices) this.stopPlain(voice);
  }

  private silence(): void {
    clearTimeout(this.endTimer);
    this.current = null;
    this.fadeOutPlain();
    this.stretch?.silence();
  }

  /** The sample ran to its end. */
  private finish(): void {
    clearTimeout(this.endTimer);
    this.current = null;
    this.stretch?.silence();
    this.setState({ ...this.state, playing: false });
  }

  /**
   * The stretch voice has no "ended" event, so its end is timed from the
   * timeline. Plain voices report their own end.
   */
  private armEnd(): void {
    clearTimeout(this.endTimer);
    const cur = this.current;
    if (!cur || cur.plain) return;
    const end = endTime(cur.timeline);
    if (!Number.isFinite(end)) return;
    const wait = Math.max(0, end - this.context.currentTime);
    this.endTimer = setTimeout(() => {
      if (this.current !== cur) return;
      // A suspended context or a throttled timer can leave some of the sample unplayed.
      if (endTime(cur.timeline) - this.context.currentTime > 0.02) this.armEnd();
      else this.finish();
    }, wait * 1000);
  }

  private stretchVoice(): Promise<StretchVoice | null> {
    this.stretchSetup ??= StretchVoice.create(this.context, this.output).then(
      (voice) => (this.stretch = voice),
      (reason) => {
        this.options.onStretchUnavailable?.(reason);
        return null;
      },
    );
    return this.stretchSetup;
  }

  private setState(next: EngineState): void {
    this.state = next;
    this.options.onChange?.(next);
  }
}

function applyLoop(source: AudioBufferSourceNode, loop: LoopRegion | null): void {
  source.loop = loop !== null;
  source.loopStart = loop?.start ?? 0;
  source.loopEnd = loop?.end ?? 0;
}

/** Keeps a start offset inside the sample; starting exactly at the end plays nothing. */
function clampOffset(seconds: number, duration: number): number {
  if (!(seconds > 0)) return 0;
  return Math.min(seconds, Math.max(0, duration - 0.001));
}

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
