// Page cache for one result list (one query + sort). Rows are fetched in
// fixed-size pages on demand and kept in an LRU, so a 50k-row list holds only
// the pages around where the user has been.

import type { Sample, SearchResult } from "../types";

export type FetchPage = (offset: number, limit: number) => Promise<SearchResult>;

export interface PageCacheOptions {
  pageSize?: number;
  /** Pages kept before the least recently used are dropped. */
  maxPages?: number;
  /** Called whenever rows, the total or the error changed. */
  onChange?: () => void;
}

interface Page {
  rows: Sample[];
  /** Fetched before the last invalidation: shown, but refetched when needed. */
  stale: boolean;
}

export class PageCache {
  /** Matches of the query; null until the first page has arrived. */
  total: number | null = null;
  /** Query parse problem or fetch failure, "" if none. */
  error = "";

  readonly pageSize: number;
  private readonly maxPages: number;
  private readonly onChange: () => void;
  private readonly pages = new Map<number, Page>();
  private readonly inflight = new Map<number, Promise<void>>();
  private readonly failed = new Set<number>();
  private generation = 0;

  constructor(
    private readonly fetchPage: FetchPage,
    options: PageCacheOptions = {},
  ) {
    this.pageSize = options.pageSize ?? 200;
    this.maxPages = options.maxPages ?? 48;
    this.onChange = options.onChange ?? (() => {});
  }

  /** The row at `index` if its page is loaded (possibly stale). */
  row(index: number): Sample | undefined {
    if (index < 0) return undefined;
    return this.pages.get(Math.floor(index / this.pageSize))?.rows[index % this.pageSize];
  }

  /** True when the row's page is loaded and newer than the last invalidation. */
  isFresh(index: number): boolean {
    const page = this.pages.get(Math.floor(index / this.pageSize));
    return page !== undefined && !page.stale;
  }

  /** Index of a sample among the loaded rows, -1 if it is not there. */
  indexOf(id: string): number {
    for (const [number, page] of this.pages) {
      const i = page.rows.findIndex((r) => r.id === id);
      if (i >= 0) return number * this.pageSize + i;
    }
    return -1;
  }

  /** Ids of rows first..last inclusive, or null when some are not loaded. */
  idsInRange(first: number, last: number): string[] | null {
    const ids: string[] = [];
    for (let i = first; i <= last; i++) {
      const row = this.row(i);
      if (!row) return null;
      ids.push(row.id);
    }
    return ids;
  }

  /** Requests whatever is missing or stale among the pages covering first..last. */
  ensure(first: number, last: number): void {
    const from = Math.max(0, Math.floor(first / this.pageSize));
    let to = Math.floor(Math.max(first, last) / this.pageSize);
    if (this.total !== null) to = Math.min(to, Math.floor(Math.max(0, this.total - 1) / this.pageSize));
    for (let p = from; p <= to; p++) {
      const page = this.pages.get(p);
      if (page) {
        // Re-insert to mark it most recently used.
        this.pages.delete(p);
        this.pages.set(p, page);
        if (!page.stale) continue;
      }
      if (this.failed.has(p)) continue;
      void this.request(p);
    }
  }

  /** Resolves with the fresh row at `index`, or undefined if there is none. */
  async load(index: number): Promise<Sample | undefined> {
    if (index < 0) return undefined;
    const p = Math.floor(index / this.pageSize);
    // A response that raced an invalidation leaves the page stale; ask again.
    for (let attempt = 0; attempt < 3 && !this.isFresh(index) && !this.failed.has(p); attempt++) {
      await this.request(p);
    }
    return this.row(index);
  }

  /** Rows may have changed on the backend: keep showing them, refetch on demand. */
  invalidate(): void {
    this.generation++;
    this.failed.clear();
    for (const page of this.pages.values()) page.stale = true;
  }

  private request(p: number): Promise<void> {
    const running = this.inflight.get(p);
    if (running) return running;
    const generation = this.generation;
    const promise = this.fetchPage(p * this.pageSize, this.pageSize)
      .then((result) => {
        this.pages.delete(p);
        this.pages.set(p, { rows: result.rows, stale: generation !== this.generation });
        this.total = result.total;
        this.error = result.error;
        this.evict();
      })
      .catch((err: unknown) => {
        this.failed.add(p);
        this.error = err instanceof Error ? err.message : String(err);
        if (this.total === null) this.total = 0;
      })
      .finally(() => {
        this.inflight.delete(p);
        this.onChange();
      });
    this.inflight.set(p, promise);
    return promise;
  }

  private evict(): void {
    for (const p of this.pages.keys()) {
      if (this.pages.size <= this.maxPages) break;
      this.pages.delete(p);
    }
  }
}
