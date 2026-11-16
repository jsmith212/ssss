import { describe, expect, it } from "vitest";
import { Lru } from "./lru";

describe("Lru", () => {
  it("evicts the least recently used past the entry limit", () => {
    const lru = new Lru<number>(2);
    lru.set("a", 1);
    lru.set("b", 2);
    expect(lru.get("a")).toBe(1); // a is now the most recent
    lru.set("c", 3);
    expect(lru.keys()).toEqual(["a", "c"]);
    expect(lru.get("b")).toBeUndefined();
  });

  it("evicts by size budget", () => {
    const lru = new Lru<string>(10, 100);
    lru.set("a", "a", 40);
    lru.set("b", "b", 40);
    lru.set("c", "c", 40);
    expect(lru.keys()).toEqual(["b", "c"]);
    expect(lru.size).toBe(80);
  });

  it("keeps a single entry that exceeds the budget", () => {
    const lru = new Lru<string>(10, 100);
    lru.set("a", "a", 40);
    lru.set("huge", "huge", 500);
    expect(lru.keys()).toEqual(["huge"]);
    lru.set("b", "b", 10);
    expect(lru.keys()).toEqual(["b"]);
    expect(lru.size).toBe(10);
  });

  it("replaces an existing key without double counting", () => {
    const lru = new Lru<number>(10, 100);
    lru.set("a", 1, 60);
    lru.set("a", 2, 30);
    expect(lru.size).toBe(30);
    expect(lru.count).toBe(1);
    expect(lru.get("a")).toBe(2);
  });

  it("deletes and clears", () => {
    const lru = new Lru<number>(10, 100);
    lru.set("a", 1, 10);
    lru.set("b", 2, 10);
    lru.delete("a");
    expect(lru.has("a")).toBe(false);
    expect(lru.size).toBe(10);
    lru.clear();
    expect(lru.count).toBe(0);
    expect(lru.size).toBe(0);
  });
});
