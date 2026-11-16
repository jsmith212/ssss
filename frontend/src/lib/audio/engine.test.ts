// The engine with Web Audio stubbed out: which voice starts, and with what
// settings. The sound itself is covered by the browser e2e (npm run e2e:audio).

import { beforeEach, describe, expect, it, vi } from "vitest";

class Param {
  value = 1;
  setValueAtTime() {}
  linearRampToValueAtTime() {}
  cancelScheduledValues() {}
  setTargetAtTime() {}
  cancelAndHoldAtTime() {}
}

class FakeNode {
  gain = new Param();
  playbackRate = new Param();
  connect(node: unknown) {
    return node;
  }
  disconnect() {}
}

const sources: { start: ReturnType<typeof vi.fn> }[] = [];

class FakeContext {
  currentTime = 0;
  state = "running";
  sampleRate = 48000;
  destination = new FakeNode();
  createGain() {
    return new FakeNode();
  }
  createBufferSource() {
    const source = Object.assign(new FakeNode(), { start: vi.fn(), stop: vi.fn(), loop: false, onended: null });
    sources.push(source);
    return source;
  }
  resume() {
    return Promise.resolve();
  }
}

class FakeBuffer {
  readonly duration: number;
  readonly sampleRate: number;
  readonly numberOfChannels: number;
  constructor(o: { length: number; sampleRate: number; numberOfChannels: number }) {
    this.sampleRate = o.sampleRate;
    this.numberOfChannels = o.numberOfChannels;
    this.duration = o.length / o.sampleRate;
  }
  copyToChannel() {}
  getChannelData() {
    return new Float32Array(4);
  }
}

vi.stubGlobal("AudioContext", FakeContext);
vi.stubGlobal("AudioBuffer", FakeBuffer);

const stretchStart = vi.fn((_loaded: unknown, _request: { rate: number; semitones: number }) => 0.1);

vi.mock("./stretch", () => ({
  FADE: 0.005,
  hold: () => {},
  StretchVoice: {
    create: async () => ({
      lead: 0.07,
      active: false,
      start: stretchStart,
      retime: vi.fn(() => 0.1),
      silence() {},
      unload() {},
    }),
  },
}));

const { Engine } = await import("./engine");

function pcm(): Response {
  return new Response(new Float32Array(48000).buffer, {
    headers: { "X-Rate": "48000", "X-Channels": "1", "X-Frames": "48000" },
  });
}

/** An engine whose audio fetches wait for `release()`. */
function gatedEngine() {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => (release = resolve));
  const engine = new Engine({
    fetchAudio: async () => {
      await gate;
      return pcm();
    },
  });
  return { engine, release };
}

const plainStarts = () => sources.filter((s) => s.start.mock.calls.length > 0).length;

beforeEach(() => {
  sources.length = 0;
  stretchStart.mockClear();
});

describe("settings changed while a sample is loading", () => {
  it("starts matched when match was switched on during the fetch", async () => {
    const { engine, release } = gatedEngine();
    const playing = engine.play("a", { loop: true, rate: 1, semitones: 0 });
    // What the player does when M is pressed or the project tempo is nudged.
    engine.update({ loop: true, rate: 1.25, semitones: 0 });
    release();
    await playing;
    expect(engine.snapshot.playing).toBe(true);
    expect(stretchStart).toHaveBeenCalledOnce();
    expect(stretchStart.mock.calls[0][1]).toMatchObject({ rate: 1.25, semitones: 0 });
    expect(plainStarts()).toBe(0);
  });

  it("starts plain when match was switched off during the fetch", async () => {
    const { engine, release } = gatedEngine();
    const playing = engine.play("a", { loop: false, rate: 1.25, semitones: 2 });
    engine.update({ loop: false, rate: 1, semitones: 0 });
    release();
    await playing;
    expect(engine.snapshot.playing).toBe(true);
    expect(stretchStart).not.toHaveBeenCalled();
    expect(plainStarts()).toBe(1);
  });

  it("uses the latest of several changes", async () => {
    const { engine, release } = gatedEngine();
    const playing = engine.play("a", { loop: true, rate: 1, semitones: 0 });
    engine.update({ loop: true, rate: 1.1, semitones: 0 });
    engine.update({ loop: true, rate: 1.2, semitones: -3 });
    release();
    await playing;
    expect(stretchStart.mock.calls[0][1]).toMatchObject({ rate: 1.2, semitones: -3 });
  });

  it("does not carry a change over to a later, unrelated play", async () => {
    const { engine, release } = gatedEngine();
    const first = engine.play("a", { loop: false, rate: 1, semitones: 0 });
    engine.stop();
    // Nothing is loading any more; this must not be remembered.
    engine.update({ loop: false, rate: 1.25, semitones: 0 });
    release();
    await first;
    await engine.play("b", { loop: false, rate: 1, semitones: 0 });
    expect(engine.snapshot.playing).toBe(true);
    expect(stretchStart).not.toHaveBeenCalled();
    expect(plainStarts()).toBe(1);
  });
});
