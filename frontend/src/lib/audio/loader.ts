// Fetches decoded audio, keeps recent samples in memory, and drops work
// nobody is waiting for any more.

import { Lru } from "./lru";
import { decodeWire, pcmBytes, type Pcm } from "./wire";

export type FetchAudio = (id: string, signal: AbortSignal) => Promise<Response>;

export interface LoaderOptions<T> {
  fetchAudio: FetchAudio;
  /** Turns wire PCM into what the cache holds (an AudioBuffer in the app). */
  build: (id: string, pcm: Pcm) => T;
  /** Cache limits; SPEC section 7 asks for about 64 samples or 200 MB. */
  maxEntries?: number;
  maxBytes?: number;
}

interface Flight<T> {
  controller: AbortController;
  promise: Promise<T>;
}

export function isAbort(err: unknown): boolean {
  return err instanceof DOMException ? err.name === "AbortError" : (err as Error)?.name === "AbortError";
}

async function failure(response: Response): Promise<Error> {
  let reason = "";
  try {
    reason = ((await response.json()) as { error?: string }).error ?? "";
  } catch {
    // Not JSON; the status alone has to do.
  }
  return new Error(reason || `audio request failed (${response.status})`);
}

export class BufferLoader<T> {
  private readonly cache: Lru<T>;
  private readonly flights = new Map<string, Flight<T>>();
  /** The sample playback is waiting for, if any. */
  private wanted: string | null = null;
  private neighbours = new Set<string>();

  constructor(private readonly options: LoaderOptions<T>) {
    this.cache = new Lru<T>(options.maxEntries ?? 64, options.maxBytes ?? 200 * 1024 * 1024);
  }

  /** The cached sample, without fetching. */
  peek(id: string): T | undefined {
    return this.cache.get(id);
  }

  /**
   * Declares which sample playback wants now. Fetches for anything else are
   * aborted unless they are current prefetch targets.
   */
  focus(id: string | null): void {
    this.wanted = id;
    this.prune();
  }

  /** Loads the sample playback wants now. Rejects with an AbortError when superseded. */
  load(id: string): Promise<T> {
    this.focus(id);
    const cached = this.cache.get(id);
    if (cached !== undefined) return Promise.resolve(cached);
    return this.fetch(id);
  }

  /**
   * Warms the cache for the rows around the focus. Replaces the previous
   * prefetch set; fetches that fell out of it are aborted.
   */
  prefetch(ids: string[]): void {
    this.neighbours = new Set(ids);
    this.prune();
    for (const id of ids) {
      if (this.cache.has(id) || this.flights.has(id)) continue;
      // A failed prefetch is not worth reporting; playing the sample will retry.
      this.fetch(id).catch(() => {});
    }
  }

  /** Forgets a cached sample, e.g. after its file changed. */
  invalidate(id: string): void {
    this.cache.delete(id);
  }

  clear(): void {
    this.wanted = null;
    this.neighbours.clear();
    this.prune();
    this.cache.clear();
  }

  get cachedBytes(): number {
    return this.cache.size;
  }

  get cachedIds(): string[] {
    return this.cache.keys();
  }

  get inFlight(): string[] {
    return [...this.flights.keys()];
  }

  private prune(): void {
    for (const [id, flight] of this.flights) {
      if (id === this.wanted || this.neighbours.has(id)) continue;
      this.flights.delete(id);
      flight.controller.abort();
    }
  }

  private fetch(id: string): Promise<T> {
    const existing = this.flights.get(id);
    if (existing) return existing.promise;

    const controller = new AbortController();
    const promise = (async () => {
      try {
        const response = await this.options.fetchAudio(id, controller.signal);
        if (!response.ok) throw await failure(response);
        const body = await response.arrayBuffer();
        // A fetch shim may ignore the signal; honour the abort here too.
        controller.signal.throwIfAborted();
        const pcm = decodeWire(response.headers, body);
        const value = this.options.build(id, pcm);
        this.cache.set(id, value, pcmBytes(pcm));
        return value;
      } finally {
        if (this.flights.get(id)?.controller === controller) this.flights.delete(id);
      }
    })();
    this.flights.set(id, { controller, promise });
    return promise;
  }
}
