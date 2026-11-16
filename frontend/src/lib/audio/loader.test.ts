import { describe, expect, it } from "vitest";
import { BufferLoader, isAbort, type FetchAudio } from "./loader";
import type { Pcm } from "./wire";

interface Call {
  id: string;
  signal: AbortSignal;
  respond: (frames?: number) => void;
  fail: (status: number, error: string) => void;
}

/** A fetch whose responses are released by the test. */
function fakeBackend() {
  const calls: Call[] = [];
  const fetchAudio: FetchAudio = (id, signal) =>
    new Promise<Response>((resolve, reject) => {
      signal.addEventListener("abort", () => reject(signal.reason));
      calls.push({
        id,
        signal,
        respond: (frames = 4) =>
          resolve(
            new Response(new Float32Array(frames).buffer, {
              headers: { "X-Rate": "44100", "X-Channels": "1", "X-Frames": String(frames) },
            }),
          ),
        fail: (status, error) => resolve(new Response(JSON.stringify({ error }), { status })),
      });
    });
  return { calls, fetchAudio, call: (id: string) => calls.filter((c) => c.id === id).at(-1)! };
}

function loader(backend: ReturnType<typeof fakeBackend>, limits: { maxEntries?: number; maxBytes?: number } = {}) {
  return new BufferLoader<{ id: string; pcm: Pcm }>({
    fetchAudio: backend.fetchAudio,
    build: (id, pcm) => ({ id, pcm }),
    ...limits,
  });
}

describe("BufferLoader", () => {
  it("loads, caches and serves from cache", async () => {
    const backend = fakeBackend();
    const l = loader(backend);
    const pending = l.load("a");
    backend.call("a").respond(8);
    const a = await pending;
    expect(a.pcm.frames).toBe(8);
    expect(l.peek("a")).toBe(a);
    expect(await l.load("a")).toBe(a);
    expect(backend.calls).toHaveLength(1);
    expect(l.cachedBytes).toBe(32);
  });

  it("shares one fetch between prefetch and load", async () => {
    const backend = fakeBackend();
    const l = loader(backend);
    l.prefetch(["a", "b"]);
    const pending = l.load("a");
    expect(backend.calls.map((c) => c.id)).toEqual(["a", "b"]);
    backend.call("a").respond();
    expect((await pending).id).toBe("a");
  });

  it("aborts a superseded load", async () => {
    const backend = fakeBackend();
    const l = loader(backend);
    const first = l.load("a");
    const second = l.load("b");
    expect(backend.call("a").signal.aborted).toBe(true);
    await expect(first).rejects.toSatisfy(isAbort);
    backend.call("b").respond();
    expect((await second).id).toBe("b");
    expect(l.peek("a")).toBeUndefined();
    expect(l.inFlight).toEqual([]);
  });

  it("keeps a superseded load that is still a prefetch target", async () => {
    const backend = fakeBackend();
    const l = loader(backend);
    const first = l.load("a");
    l.prefetch(["a", "c"]);
    void l.load("b");
    expect(backend.call("a").signal.aborted).toBe(false);
    backend.call("a").respond();
    expect((await first).id).toBe("a");
  });

  it("aborts prefetches that fall out of the window, but not the wanted sample", () => {
    const backend = fakeBackend();
    const l = loader(backend);
    void l.load("w").catch(() => {});
    l.prefetch(["a", "b"]);
    l.prefetch(["b", "c"]);
    expect(backend.call("a").signal.aborted).toBe(true);
    expect(backend.call("b").signal.aborted).toBe(false);
    expect(backend.call("w").signal.aborted).toBe(false);
    expect(l.inFlight.sort()).toEqual(["b", "c", "w"]);
  });

  it("drops a response that arrives after the abort", async () => {
    // A fetch that ignores its signal, like a shim might.
    let release!: (r: Response) => void;
    const l = new BufferLoader<string>({
      fetchAudio: () => new Promise<Response>((resolve) => (release = resolve)),
      build: (id) => id,
    });
    const first = l.load("a");
    l.focus("b");
    release(new Response(new Float32Array(4).buffer, { headers: { "X-Rate": "44100", "X-Channels": "1", "X-Frames": "4" } }));
    await expect(first).rejects.toSatisfy(isAbort);
    expect(l.peek("a")).toBeUndefined();
  });

  it("reports the server's reason", async () => {
    const backend = fakeBackend();
    const l = loader(backend);
    const pending = l.load("a");
    backend.call("a").fail(404, "file is missing");
    await expect(pending).rejects.toThrow("file is missing");
    // A failure is not cached: the next load tries again.
    void l.load("a").catch(() => {});
    expect(backend.calls).toHaveLength(2);
  });

  it("evicts by byte budget", async () => {
    const backend = fakeBackend();
    const l = loader(backend, { maxBytes: 100 });
    for (const id of ["a", "b", "c"]) {
      const pending = l.load(id);
      backend.call(id).respond(10); // 40 bytes each
      await pending;
    }
    expect(l.cachedIds).toEqual(["b", "c"]);
  });

  it("swallows prefetch failures", async () => {
    const backend = fakeBackend();
    const l = loader(backend);
    l.prefetch(["a"]);
    backend.call("a").fail(500, "boom");
    await new Promise((r) => setTimeout(r, 0));
    expect(l.peek("a")).toBeUndefined();
    expect(l.inFlight).toEqual([]);
  });
});
