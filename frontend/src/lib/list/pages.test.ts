import { describe, expect, it, vi } from "vitest";
import type { Sample, SearchResult } from "../types";
import { PageCache } from "./pages";

function sample(i: number, suffix = ""): Sample {
  return { id: `s${i}${suffix}`, name: `Sample ${i}` } as Sample;
}

/** A backend of `total` rows whose fetches resolve when the test says so. */
function backend(total: number) {
  let suffix = "";
  const waiting: (() => void)[] = [];
  const fetch = vi.fn((offset: number, limit: number) => {
    const stamp = suffix;
    return new Promise<SearchResult>((resolve) => {
      waiting.push(() => {
        const rows = [];
        for (let i = offset; i < Math.min(total, offset + limit); i++) rows.push(sample(i, stamp));
        resolve({ total, rows, error: "" });
      });
    });
  });
  return {
    fetch,
    /** Resolves every fetch issued so far. */
    async flush() {
      while (waiting.length > 0) {
        waiting.splice(0).forEach((resolve) => resolve());
        await new Promise((r) => setTimeout(r, 0));
      }
    },
    /** Rows fetched from now on carry this id suffix (a backend change). */
    mutate(next: string) {
      suffix = next;
    },
  };
}

describe("PageCache", () => {
  it("fetches only the pages covering the requested range, once", async () => {
    const b = backend(1000);
    const onChange = vi.fn();
    const cache = new PageCache(b.fetch, { pageSize: 100, onChange });
    expect(cache.total).toBeNull();
    cache.ensure(250, 320);
    cache.ensure(250, 320);
    expect(b.fetch.mock.calls).toEqual([
      [200, 100],
      [300, 100],
    ]);
    await b.flush();
    expect(cache.total).toBe(1000);
    expect(cache.row(250)?.id).toBe("s250");
    expect(cache.row(399)?.id).toBe("s399");
    expect(cache.row(400)).toBeUndefined();
    expect(cache.row(-1)).toBeUndefined();
    expect(onChange).toHaveBeenCalledTimes(2);
    cache.ensure(250, 320);
    expect(b.fetch).toHaveBeenCalledTimes(2);
  });

  it("does not ask past the end once the total is known", async () => {
    const b = backend(150);
    const cache = new PageCache(b.fetch, { pageSize: 100 });
    cache.ensure(0, 99);
    await b.flush();
    cache.ensure(100, 900);
    expect(b.fetch.mock.calls).toEqual([
      [0, 100],
      [100, 100],
    ]);
  });

  it("evicts the least recently used pages", async () => {
    const b = backend(1000);
    const cache = new PageCache(b.fetch, { pageSize: 100, maxPages: 2 });
    cache.ensure(0, 0);
    await b.flush();
    cache.ensure(100, 100);
    await b.flush();
    cache.ensure(0, 0); // touch page 0 so page 1 is the oldest
    cache.ensure(200, 200);
    await b.flush();
    expect(cache.row(0)).toBeDefined();
    expect(cache.row(100)).toBeUndefined();
    expect(cache.row(200)).toBeDefined();
  });

  it("keeps stale rows visible after invalidate and refetches them on demand", async () => {
    const b = backend(300);
    const cache = new PageCache(b.fetch, { pageSize: 100 });
    cache.ensure(0, 150);
    await b.flush();
    b.mutate("'");
    cache.invalidate();
    expect(cache.row(10)?.id).toBe("s10");
    expect(cache.isFresh(10)).toBe(false);
    cache.ensure(0, 50);
    await b.flush();
    expect(cache.row(10)?.id).toBe("s10'");
    expect(cache.isFresh(10)).toBe(true);
    expect(cache.isFresh(110)).toBe(false);
    expect(cache.row(110)?.id).toBe("s110");
  });

  it("treats a response that raced an invalidation as stale", async () => {
    const b = backend(100);
    const cache = new PageCache(b.fetch, { pageSize: 100 });
    cache.ensure(0, 0);
    cache.invalidate();
    await b.flush();
    expect(cache.row(0)).toBeDefined();
    expect(cache.isFresh(0)).toBe(false);
    cache.ensure(0, 0);
    await b.flush();
    expect(cache.isFresh(0)).toBe(true);
    expect(b.fetch).toHaveBeenCalledTimes(2);
  });

  it("load waits for a fresh row", async () => {
    const b = backend(500);
    const cache = new PageCache(b.fetch, { pageSize: 100 });
    const loading = cache.load(499);
    await b.flush();
    expect((await loading)?.id).toBe("s499");
    const beyond = cache.load(900);
    await b.flush();
    expect(await beyond).toBeUndefined();
  });

  it("finds loaded rows by id and collects id ranges", async () => {
    const b = backend(300);
    const cache = new PageCache(b.fetch, { pageSize: 100 });
    cache.ensure(100, 199);
    await b.flush();
    expect(cache.indexOf("s150")).toBe(150);
    expect(cache.indexOf("s20")).toBe(-1);
    expect(cache.idsInRange(120, 122)).toEqual(["s120", "s121", "s122"]);
    expect(cache.idsInRange(98, 101)).toBeNull();
  });

  it("reports a failed fetch once and retries only after invalidate", async () => {
    let fail = true;
    const fetch = vi.fn(async (): Promise<SearchResult> => {
      if (fail) throw new Error("backend down");
      return { total: 1, rows: [sample(0)], error: "" };
    });
    const cache = new PageCache(fetch, { pageSize: 100 });
    cache.ensure(0, 0);
    await new Promise((r) => setTimeout(r, 0));
    expect(cache.error).toBe("backend down");
    expect(cache.total).toBe(0);
    cache.ensure(0, 0);
    expect(fetch).toHaveBeenCalledTimes(1);
    fail = false;
    cache.invalidate();
    cache.ensure(0, 0);
    await new Promise((r) => setTimeout(r, 0));
    expect(cache.row(0)?.id).toBe("s0");
    expect(cache.error).toBe("");
  });
});
