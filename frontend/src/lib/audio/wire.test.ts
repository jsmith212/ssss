import { describe, expect, it } from "vitest";
import { decodeWire, pcmBytes } from "./wire";

function headers(map: Record<string, string>) {
  const lower = Object.fromEntries(Object.entries(map).map(([k, v]) => [k.toLowerCase(), v]));
  return { get: (name: string) => lower[name.toLowerCase()] ?? null };
}

function planar(...channels: number[][]): ArrayBuffer {
  const frames = channels[0].length;
  const view = new DataView(new ArrayBuffer(frames * channels.length * 4));
  channels.forEach((ch, c) => ch.forEach((v, i) => view.setFloat32((c * frames + i) * 4, v, true)));
  return view.buffer;
}

describe("decodeWire", () => {
  it("splits planar stereo", () => {
    const pcm = decodeWire(
      headers({ "X-Rate": "44100", "X-Channels": "2", "X-Frames": "3" }),
      planar([0.5, -0.5, 0.25], [1, 0, -1]),
    );
    expect(pcm.rate).toBe(44100);
    expect(pcm.frames).toBe(3);
    expect([...pcm.channels[0]]).toEqual([0.5, -0.5, 0.25]);
    expect([...pcm.channels[1]]).toEqual([1, 0, -1]);
    expect(pcm.loopStart).toBeUndefined();
    expect(pcmBytes(pcm)).toBe(24);
  });

  it("reads mono and loop points", () => {
    const pcm = decodeWire(
      headers({ "x-rate": "48000", "x-channels": "1", "x-frames": "4", "x-loop-start": "1", "x-loop-end": "4" }),
      planar([0, 1, 0, -1]),
    );
    expect(pcm.channels).toHaveLength(1);
    expect(pcm.loopStart).toBe(1);
    expect(pcm.loopEnd).toBe(4);
  });

  it("ignores loop points that do not fit the audio", () => {
    const h = { "X-Rate": "44100", "X-Channels": "1", "X-Frames": "4" };
    const body = planar([0, 1, 0, -1]);
    for (const [start, end] of [["2", "2"], ["3", "1"], ["0", "9"], ["x", "3"]]) {
      const pcm = decodeWire(headers({ ...h, "X-Loop-Start": start, "X-Loop-End": end }), body);
      expect(pcm.loopStart).toBeUndefined();
      expect(pcm.loopEnd).toBeUndefined();
    }
  });

  it("plays what arrived when the body is short", () => {
    const pcm = decodeWire(
      headers({ "X-Rate": "44100", "X-Channels": "1", "X-Frames": "10" }),
      planar([0.1, 0.2]),
    );
    expect(pcm.frames).toBe(2);
    expect(pcm.channels[0]).toHaveLength(2);
  });

  it.each([
    [{ "X-Channels": "2", "X-Frames": "1" }, "X-Rate"],
    [{ "X-Rate": "0", "X-Channels": "2", "X-Frames": "1" }, "X-Rate"],
    [{ "X-Rate": "44100", "X-Channels": "3", "X-Frames": "1" }, "X-Channels"],
    [{ "X-Rate": "44100", "X-Channels": "1", "X-Frames": "-4" }, "X-Frames"],
    [{ "X-Rate": "44100", "X-Channels": "1" }, "X-Frames"],
  ])("rejects bad headers %j", (h, what) => {
    expect(() => decodeWire(headers(h as Record<string, string>), new ArrayBuffer(8))).toThrow(what);
  });

  it("rejects an empty body", () => {
    expect(() =>
      decodeWire(headers({ "X-Rate": "44100", "X-Channels": "1", "X-Frames": "0" }), new ArrayBuffer(0)),
    ).toThrow("empty");
  });
});
