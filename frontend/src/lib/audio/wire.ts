// Decoding of the /audio/{id}.f32 wire format: planar little-endian float32
// (all of channel 0, then channel 1) described by response headers.

export interface Pcm {
  rate: number;
  frames: number;
  /** One Float32Array per channel (1 or 2). */
  channels: Float32Array[];
  /** Loop region in frames from the file's smpl chunk, when it has one. */
  loopStart?: number;
  loopEnd?: number;
}

/** The part of `Headers` this module needs; lets tests pass a plain map. */
export interface HeaderSource {
  get(name: string): string | null;
}

const LITTLE_ENDIAN = new Uint8Array(new Uint16Array([1]).buffer)[0] === 1;

function intHeader(headers: HeaderSource, name: string): number | undefined {
  const raw = headers.get(name);
  if (raw === null || raw.trim() === "") return undefined;
  const n = Number(raw);
  return Number.isInteger(n) && n >= 0 ? n : Number.NaN;
}

/**
 * Turns a response into channel arrays. On little-endian hosts the arrays
 * are views over `body`, not copies. Throws on a malformed response.
 */
export function decodeWire(headers: HeaderSource, body: ArrayBuffer): Pcm {
  const rate = intHeader(headers, "X-Rate");
  const channelCount = intHeader(headers, "X-Channels");
  const declared = intHeader(headers, "X-Frames");
  if (!rate || Number.isNaN(rate)) throw new Error("audio response has no valid X-Rate");
  if (channelCount !== 1 && channelCount !== 2) {
    throw new Error("audio response has no valid X-Channels");
  }
  if (declared === undefined || Number.isNaN(declared)) {
    throw new Error("audio response has no valid X-Frames");
  }
  // A body cut short still plays what arrived.
  const frames = Math.min(declared, Math.floor(body.byteLength / (4 * channelCount)));
  if (frames === 0) throw new Error("audio response is empty");

  const channels: Float32Array[] = [];
  for (let c = 0; c < channelCount; c++) {
    // The declared frame count, not the received one, is the channel stride.
    const offset = c * declared * 4;
    const available = Math.max(0, Math.min(frames, Math.floor((body.byteLength - offset) / 4)));
    if (LITTLE_ENDIAN && available === frames) {
      channels.push(new Float32Array(body, offset, frames));
      continue;
    }
    const out = new Float32Array(frames);
    const view = new DataView(body);
    for (let i = 0; i < available; i++) out[i] = view.getFloat32(offset + i * 4, true);
    channels.push(out);
  }

  const pcm: Pcm = { rate, frames, channels };
  const loopStart = intHeader(headers, "X-Loop-Start");
  const loopEnd = intHeader(headers, "X-Loop-End");
  if (
    loopStart !== undefined &&
    loopEnd !== undefined &&
    loopStart < loopEnd &&
    loopEnd <= frames
  ) {
    pcm.loopStart = loopStart;
    pcm.loopEnd = loopEnd;
  }
  return pcm;
}

export function pcmBytes(pcm: Pcm): number {
  return pcm.frames * pcm.channels.length * 4;
}
