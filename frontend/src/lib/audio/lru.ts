// Least-recently-used cache bounded by both entry count and total size.

export class Lru<V> {
  private readonly entries = new Map<string, { value: V; size: number }>();
  private total = 0;

  constructor(
    private readonly maxEntries: number,
    private readonly maxSize: number = Number.POSITIVE_INFINITY,
  ) {}

  /** Returns the value and marks it most recently used. */
  get(key: string): V | undefined {
    const entry = this.entries.get(key);
    if (!entry) return undefined;
    this.entries.delete(key);
    this.entries.set(key, entry);
    return entry.value;
  }

  has(key: string): boolean {
    return this.entries.has(key);
  }

  /**
   * Stores a value, evicting the least recently used until both limits hold.
   * The newest entry always stays, even when it alone exceeds the size limit.
   */
  set(key: string, value: V, size = 1): void {
    this.delete(key);
    this.entries.set(key, { value, size });
    this.total += size;
    for (const oldest of this.entries.keys()) {
      if (this.entries.size <= this.maxEntries && this.total <= this.maxSize) break;
      if (oldest === key) break;
      this.delete(oldest);
    }
  }

  delete(key: string): void {
    const entry = this.entries.get(key);
    if (!entry) return;
    this.entries.delete(key);
    this.total -= entry.size;
  }

  clear(): void {
    this.entries.clear();
    this.total = 0;
  }

  get size(): number {
    return this.total;
  }

  get count(): number {
    return this.entries.size;
  }

  keys(): string[] {
    return [...this.entries.keys()];
  }
}
