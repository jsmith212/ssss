import { describe, expect, it } from "vitest";
import { MockBackend } from "./api.mock";
import { FULL_PEAK_BUCKETS, MINI_PEAKS, type ApiEvents } from "./types";

const backend = () => new MockBackend({ samples: 3000 });
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

describe("mock library", () => {
  it("is deterministic", async () => {
    const a = await backend().search({ query: "", sort: "added", desc: true, offset: 0, limit: 50 });
    const b = await backend().search({ query: "", sort: "added", desc: true, offset: 0, limit: 50 });
    expect(a.total).toBe(3000);
    expect(a.rows.map((r) => r.id)).toEqual(b.rows.map((r) => r.id));
  });

  it("generates about 20k samples across 40 packs by default", async () => {
    const sidebar = await new MockBackend().getSidebar();
    expect(sidebar.all).toBe(20000);
    expect(sidebar.packs.length).toBe(40);
    expect(sidebar.packs.reduce((n, p) => n + p.count, 0)).toBe(20000);
  });
});

describe("search", () => {
  it("pages without gaps or overlaps", async () => {
    const api = backend();
    const idsAll = await api.searchIds("type:loop", "name", false);
    const first = await api.search({ query: "type:loop", sort: "name", desc: false, offset: 0, limit: 200 });
    const second = await api.search({ query: "type:loop", sort: "name", desc: false, offset: 200, limit: 200 });
    expect(first.total).toBe(idsAll.length);
    expect([...first.rows, ...second.rows].map((r) => r.id)).toEqual(idsAll.slice(0, 400));
  });

  it.each([
    ["bpm", (r: { bpm: number }) => r.bpm || Infinity],
    ["dur", (r: { dur: number }) => r.dur],
    ["added", (r: { added: number }) => r.added],
  ] as const)("sorts by %s ascending", async (sort, value) => {
    const { rows } = await backend().search({ query: "", sort, desc: false, offset: 0, limit: 500 });
    const values = rows.map(value as (r: unknown) => number);
    expect(values).toEqual([...values].sort((a, b) => a - b));
  });

  it("returns analysed rows with 96 mini peaks", async () => {
    const { rows } = await backend().search({ query: "-is:unanalyzed", sort: "added", desc: true, offset: 0, limit: 5 });
    for (const row of rows) expect(atob(row.mini).length).toBe(MINI_PEAKS);
  });

  it("reports query errors but still answers", async () => {
    const res = await backend().search({ query: "len:long", sort: "added", desc: true, offset: 0, limit: 1 });
    expect(res.error).not.toBe("");
    expect(res.total).toBe(3000);
  });
});

describe("sidebar", () => {
  it("counts tags including descendants", async () => {
    const api = backend();
    const sidebar = await api.getSidebar();
    const drums = sidebar.tags.find((t) => t.tag === "drums")!;
    expect(drums.count).toBe((await api.searchIds("tag:drums", "added", true)).length);
    expect(sidebar.favorites).toBe((await api.searchIds("is:fav", "added", true)).length);
    expect(sidebar.untagged).toBe((await api.searchIds("is:untagged", "added", true)).length);
    expect(sidebar.recent).toBe((await api.searchIds("is:recent", "added", true)).length);
  });
});

describe("mutations", () => {
  it("tags, announces the change, and undoes and redoes", async () => {
    const api = backend();
    const events: ApiEvents["samples:changed"][] = [];
    api.on("samples:changed", (e) => events.push(e));
    const [id] = await api.searchIds("is:untagged", "added", true);

    await api.addTags([id], ["mine/test"]);
    await settle();
    expect(events).toEqual([{ ids: [id], structural: true }]);
    expect(await api.searchIds("#mine/test", "added", true)).toEqual([id]);

    expect(await api.undo()).toContain("mine/test");
    expect(await api.searchIds("#mine/test", "added", true)).toEqual([]);
    expect(await api.redo()).toContain("mine/test");
    expect(await api.searchIds("#mine/test", "added", true)).toEqual([id]);
    expect(await api.redo()).toBe("");
  });

  it("renames a tag with its descendants", async () => {
    const api = backend();
    const before = (await api.searchIds("tag:drums", "added", true)).length;
    await api.renameTag("drums", "perc");
    expect((await api.searchIds("tag:drums", "added", true)).length).toBe(0);
    expect((await api.searchIds("tag:perc/kick", "added", true)).length).toBeGreaterThan(0);
    expect((await api.searchIds("tag:perc", "added", true)).length).toBe(before);
  });

  it("removes and restores samples", async () => {
    const api = backend();
    const ids = (await api.searchIds("", "added", true)).slice(0, 3);
    await api.removeSamples(ids);
    expect((await api.getSidebar()).all).toBe(2997);
    await api.undo();
    expect((await api.getSidebar()).all).toBe(3000);
  });

  it("marks user edits and normalises keys", async () => {
    const api = backend();
    const [id] = await api.searchIds("", "added", true);
    await api.setMeta([id], { bpm: 133, key: "Bb minor" });
    const [detail] = await api.getSamples([id]);
    expect(detail).toMatchObject({ bpm: 133, bpmSrc: "user", key: "A#m", keySrc: "user" });
    await expect(api.setMeta([id], { key: "H#" })).rejects.toThrow();
  });

  it("manages collections and views", async () => {
    const api = backend();
    const ids = (await api.searchIds("", "added", true)).slice(0, 4);
    const col = await api.createCollection("Scratch");
    await api.addToCollection(col.id, ids);
    expect((await api.searchIds('in:"Scratch"', "added", true)).length).toBe(4);
    await api.removeFromCollection(col.id, ids.slice(0, 1));
    expect((await api.getSidebar()).collections.find((c) => c.id === col.id)?.count).toBe(3);
    const view = await api.saveView("Loops", "type:loop");
    expect((await api.getSidebar()).views.some((v) => v.id === view.id)).toBe(true);
    await api.deleteView(view.id);
    expect((await api.getSidebar()).views.some((v) => v.id === view.id)).toBe(false);
  });

  it("refuses to write when the library is unavailable", async () => {
    const api = new MockBackend({ samples: 10, status: "unavailable" });
    await expect(api.setFavorite(["x"], true)).rejects.toThrow();
  });
});

describe("import", () => {
  it("adds samples, reports progress and marks them recent", async () => {
    const api = backend();
    const progress: ApiEvents["ingest:progress"][] = [];
    api.on("ingest:progress", (p) => progress.push(p));
    const jobId = await api.import(["/tmp/a.wav", "/tmp/b.flac"]);
    await new Promise((resolve) => setTimeout(resolve, 1200));
    const last = progress.at(-1)!;
    expect(last).toMatchObject({ jobId, kind: "import", finished: true, done: 2, total: 2, added: 2 });
    expect((await api.getSidebar()).all).toBe(3002);
    expect((await api.searchIds("is:recent", "added", true)).length).toBe(2);
    expect((await api.searchIds('pack:"Unsorted"', "added", true)).length).toBe(2);
  });
});

describe("audio endpoints", () => {
  it("serves planar float32 with the wire headers, and peaks", async () => {
    const api = backend();
    const [id] = await api.searchIds("type:oneshot -is:missing", "dur", false);
    const audio = api.respond(api.audioUrl(id))!;
    const frames = Number(audio.headers.get("X-Frames"));
    const channels = Number(audio.headers.get("X-Channels"));
    expect(Number(audio.headers.get("X-Rate"))).toBe(44100);
    expect((await audio.arrayBuffer()).byteLength).toBe(frames * channels * 4);

    const peaks = api.respond(api.peaksUrl(id))!;
    expect((await peaks.arrayBuffer()).byteLength).toBe(FULL_PEAK_BUCKETS * 2);
  });

  it("answers 404 JSON for unknown ids and ignores other paths", async () => {
    const api = backend();
    const res = api.respond("/audio/ffff.f32")!;
    expect(res.status).toBe(404);
    expect(await res.json()).toHaveProperty("error");
    expect(api.respond("/index.html")).toBeNull();
  });
});
