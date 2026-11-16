import { describe, expect, it } from "vitest";
import {
  addToken,
  chipLabel,
  describeQuery,
  refMatcher,
  referents,
  singleCollection,
  suggestViewName,
  toggleRef,
  formatToken,
  hasFreeText,
  hasToken,
  isFilterWord,
  joinQuery,
  removeToken,
  setKind,
  singlePack,
  splitQuery,
  suggest,
  toggleToken,
  tokenMatcher,
  tokenize,
  tokensOf,
  wordAt,
} from "./query";

const brief = (q: string) => tokenize(q).map((t) => [t.kind, t.value, t.negated]);

describe("tokenize", () => {
  it.each([
    ["", []],
    ["kick", [["text", "kick", false]]],
    ["  deep   kick ", [["text", "deep", false], ["text", "kick", false]]],
    ["tag:drums/kick", [["tag", "drums/kick", false]]],
    ["#drums/kick/", [["tag", "drums/kick", false]]],
    ["@KSHMR", [["pack", "KSHMR", false]]],
    ['pack:"KSHMR Vol 4" x', [["pack", "KSHMR Vol 4", false], ["text", "x", false]]],
    ['in:"Techno set"', [["in", "Techno set", false]]],
    ["-tag:fx", [["tag", "fx", true]]],
    ["-#fx", [["tag", "fx", true]]],
    ["-kick", [["text", "kick", true]]],
    ["-", [["text", "-", false]]],
    ["BPM:120-128", [["bpm", "120-128", false]]],
    ["key:compatible:Am", [["key", "compatible:Am", false]]],
    ["len:<2s is:fav type:loop fmt:wav", [["len", "<2s", false], ["is", "fav", false], ["type", "loop", false], ["fmt", "wav", false]]],
    ["foo:bar", [["text", "foo:bar", false]]],
    ["bpm:", [["text", "bpm:", false]]],
    ["#", [["text", "#", false]]],
    ['"two words"', [["text", "two words", false]]],
    ['pack:"unterminated quote', [["pack", "unterminated quote", false]]],
  ])("%j", (query, expected) => {
    expect(brief(query)).toEqual(expected);
  });

  it("keeps the written form", () => {
    expect(tokenize('#kick pack:"A B"').map((t) => t.raw)).toEqual(["#kick", 'pack:"A B"']);
  });
});

describe("formatToken and chipLabel", () => {
  it.each([
    [{ kind: "tag", value: "drums/kick" }, "tag:drums/kick", "#drums/kick"],
    [{ kind: "pack", value: "KSHMR Vol 4" }, 'pack:"KSHMR Vol 4"', "@KSHMR Vol 4"],
    [{ kind: "in", value: "Techno set", negated: true }, '-in:"Techno set"', "-in:Techno set"],
    [{ kind: "bpm", value: "120-128" }, "bpm:120-128", "bpm:120-128"],
    [{ kind: "text", value: "two words" }, '"two words"', "two words"],
    [{ kind: "pack", value: 'Say "hi"' }, 'pack:"Say \\"hi\\""', '@Say "hi"'],
  ] as const)("%j", (spec, written, label) => {
    expect(formatToken(spec)).toBe(written);
    expect(chipLabel(spec)).toBe(label);
  });

  it("round-trips through tokenize", () => {
    const spec = { kind: "pack", value: "KSHMR Vol 4", negated: true } as const;
    const [token] = tokenize(formatToken(spec));
    expect([token.kind, token.value, token.negated]).toEqual(["pack", "KSHMR Vol 4", true]);
  });
});

describe("token helpers", () => {
  it("treats # and tag: and letter case as the same token", () => {
    expect(hasToken("#Drums/Kick", { kind: "tag", value: "drums/kick" })).toBe(true);
    expect(hasToken("-tag:fx", { kind: "tag", value: "fx" })).toBe(false);
    expect(hasToken("-tag:fx", { kind: "tag", value: "fx", negated: true })).toBe(true);
  });

  it("adds once", () => {
    expect(addToken("", { kind: "is", value: "fav" })).toBe("is:fav");
    expect(addToken("kick", { kind: "is", value: "fav" })).toBe("kick is:fav");
    expect(addToken("kick is:fav", { kind: "is", value: "fav" })).toBe("kick is:fav");
  });

  it("removes every spelling and keeps the rest as written", () => {
    expect(removeToken('#kick pack:"A B" tag:kick deep', { kind: "tag", value: "kick" })).toBe('pack:"A B" deep');
  });

  it("toggles", () => {
    const spec = { kind: "pack", value: "A B" } as const;
    expect(toggleToken("x", spec)).toBe('x pack:"A B"');
    expect(toggleToken('x pack:"A B"', spec)).toBe("x");
  });

  it("setKind swaps positive tokens of a kind and leaves negated ones", () => {
    expect(setKind("bpm:120 kick bpm:>90 -bpm:100", "bpm", "100-130")).toBe("kick -bpm:100 bpm:100-130");
    expect(setKind("bpm:120 kick", "bpm", null)).toBe("kick");
    expect(setKind("kick", "type", "loop")).toBe("kick type:loop");
    expect(setKind("type:loop", "type", " ")).toBe("");
  });

  it("tokensOf skips negated tokens", () => {
    expect(tokensOf("#a -#b tag:c", "tag").map((t) => t.value)).toEqual(["a", "c"]);
  });

  it("tokenMatcher answers for many tokens", () => {
    const has = tokenMatcher('is:fav pack:"A B"');
    expect(has({ kind: "is", value: "fav" })).toBe(true);
    expect(has({ kind: "pack", value: "a b" })).toBe(true);
    expect(has({ kind: "is", value: "untagged" })).toBe(false);
  });

  it.each([
    ["", false],
    ["is:fav #kick", false],
    ["kick", true],
    ["-kick", false],
    ["foo:bar", true],
    ["is:fav deep", true],
  ])("hasFreeText(%j) = %s", (query, expected) => {
    expect(hasFreeText(query)).toBe(expected);
  });

  it.each([
    ['pack:"A B" kick', "A B"],
    ["@A @B", null],
    ["-pack:A", null],
    ["kick", null],
  ])("singlePack(%j) = %j", (query, expected) => {
    expect(singlePack(query)).toBe(expected);
  });
});

describe("splitQuery / joinQuery", () => {
  it("separates chips from free text", () => {
    const { chips, text } = splitQuery('deep #kick "sub bass" bpm:120');
    expect(chips.map((c) => c.raw)).toEqual(["#kick", "bpm:120"]);
    expect(text).toBe('deep "sub bass"');
    expect(joinQuery(chips, text + " ")).toBe('#kick bpm:120 deep "sub bass"');
  });

  it("joins empty parts to an empty query", () => {
    expect(joinQuery([], "  ")).toBe("");
  });
});

describe("wordAt", () => {
  it.each([
    ["kick #dr", 8, "#dr", 5, 8],
    ["kick #dr", 2, "kick", 0, 4],
    ["kick ", 5, "", 5, 5],
    ['pack:"A B" x', 9, 'pack:"A B"', 0, 10],
    ["", 0, "", 0, 0],
  ])("%j @%i", (text, caret, word, start, end) => {
    expect(wordAt(text, caret)).toEqual({ word, start, end });
  });
});

describe("isFilterWord", () => {
  it.each([
    ["#", true],
    ["-#ki", true],
    ["@", true],
    ["bpm:", true],
    ["KEY:a", true],
    ["foo:bar", false],
    ["kick", false],
    ["", false],
    [":x", false],
  ])("%j -> %s", (word, expected) => {
    expect(isFilterWord(word)).toBe(expected);
  });
});

describe("suggest", () => {
  const sources = {
    tags: [
      { tag: "drums", count: 50, color: "" },
      { tag: "drums/kick", count: 20, color: "" },
      { tag: "drums/snare", count: 10, color: "" },
      { tag: "fx/kickback", count: 3, color: "" },
      { tag: "bass", count: 30, color: "" },
    ],
    packs: [
      { id: "pk_1", name: "KSHMR Vol 4", dir: "", source: "", count: 100 },
      { id: "pk_2", name: "Kick Essentials", dir: "", source: "", count: 40 },
    ],
    collections: [{ id: "col_1", name: "Techno set", count: 12 }],
  };

  it("completes tags, best leaf match first", () => {
    expect(suggest("#ki", sources).map((s) => s.token)).toEqual(["tag:drums/kick", "tag:fx/kickback"]);
    expect(suggest("tag:dr", sources).map((s) => s.label)).toEqual(["#drums", "#drums/kick", "#drums/snare"]);
  });

  it("lists everything of a kind for a bare prefix", () => {
    expect(suggest("#", sources)).toHaveLength(5);
    // Packs and collections are labelled by name and written by id.
    expect(suggest("@", sources).map((s) => [s.label, s.token])).toEqual([
      ["@KSHMR Vol 4", "pack:pk_1"],
      ["@Kick Essentials", "pack:pk_2"],
    ]);
    expect(suggest("in:", sources).map((s) => [s.label, s.token])).toEqual([["in:Techno set", "in:col_1"]]);
    expect(suggest('-pack:"kshmr v', sources).map((s) => [s.label, s.token])).toEqual([["-@KSHMR Vol 4", "-pack:pk_1"]]);
  });

  it("keeps the negation", () => {
    expect(suggest("-#bass", sources).map((s) => s.token)).toEqual(["-tag:bass"]);
  });

  it("completes fixed vocabularies", () => {
    expect(suggest("key:f", sources).map((s) => s.token)).toEqual(["key:F", "key:F#", "key:Fm", "key:F#m"]);
    expect(suggest("type:", sources).map((s) => s.token)).toEqual(["type:loop", "type:oneshot"]);
    expect(suggest("is:un", sources).map((s) => s.token)).toEqual(["is:untagged", "is:unanalyzed"]);
    expect(suggest("bpm:12", sources)).toEqual([]);
  });

  it("offers tags, packs and collections for plain text of two or more letters", () => {
    expect(suggest("k", sources)).toEqual([]);
    expect(suggest("kick", sources).map((s) => s.token)).toEqual([
      "tag:drums/kick",
      "tag:fx/kickback",
      "pack:pk_2",
    ]);
    expect(suggest("techno", sources).map((s) => s.token)).toEqual(["in:col_1"]);
  });
});

describe("quoting", () => {
  // Names the library accepts: tags and pack names keep quotes and backslashes.
  it.each([
    ["tag", '12"'],
    ["tag", '"q"'],
    ["tag", "a\\b"],
    ["tag", "c\\d e\\"],
    ["pack", '12" Vinyl Cuts'],
    ["pack", "AC\\DC live\\"],
    ["in", 'Tom\'s "best" kicks'],
    ["text", 'say "hi"'],
  ] as const)("%s %j survives being written and read back", (kind, value) => {
    const written = formatToken({ kind, value });
    const tokens = tokenize(`kick ${written} is:fav`);
    expect(tokens.map((t) => t.kind)).toEqual(["text", kind, "is"]);
    expect(tokens[1].value).toBe(value);
    expect(tokens[1].raw).toBe(written);
    expect(hasToken(`kick ${written}`, { kind, value })).toBe(true);
    expect(removeToken(`kick ${written} is:fav`, { kind, value })).toBe("kick is:fav");
  });

  it("writes escapes the way the Go lexer reads them", () => {
    expect(formatToken({ kind: "tag", value: '12"' })).toBe('tag:"12\\""');
    expect(formatToken({ kind: "pack", value: "AC\\DC live\\" })).toBe('pack:"AC\\\\DC live\\\\"');
    // Nothing to protect: no quotes.
    expect(formatToken({ kind: "tag", value: "drums/kick" })).toBe("tag:drums/kick");
  });

  it("keeps an escaped quote inside the word under the caret", () => {
    const text = 'pack:"a \\" b" x';
    expect(wordAt(text, 9)).toEqual({ word: 'pack:"a \\" b"', start: 0, end: 13 });
  });
});

describe("pack and collection references", () => {
  const packs = [
    { id: "pk_A", name: "Drums" },
    { id: "pk_B", name: "Drums" },
    { id: "pk_C", name: '12" Vinyl Breaks' },
  ];
  const collections = [
    { id: "col_1", name: "Techno set" },
    { id: "col_2", name: "Warm" },
  ];
  const names = { packs, collections };

  it("an id selects one, a name every item called that", () => {
    expect(referents("pk_B", packs).map((p) => p.id)).toEqual(["pk_B"]);
    expect(referents("drums", packs).map((p) => p.id)).toEqual(["pk_A", "pk_B"]);
    expect(referents("nope", packs)).toEqual([]);
  });

  it("highlights by what the tokens select", () => {
    const byId = refMatcher("kick pack:pk_A", "pack");
    expect(packs.map(byId)).toEqual([true, false, false]);
    const byName = refMatcher("@Drums", "pack");
    expect(packs.map(byName)).toEqual([true, true, false]);
    expect(packs.map(refMatcher("-pack:pk_A", "pack"))).toEqual([false, false, false]);
  });

  it("toggles the id token, and removes a typed name that selects the item", () => {
    expect(toggleRef("kick", "pack", packs[2])).toBe("kick pack:pk_C");
    expect(toggleRef("kick pack:pk_C", "pack", packs[2])).toBe("kick");
    expect(toggleRef('kick pack:"12\\" Vinyl Breaks"', "pack", packs[2])).toBe("kick");
    expect(toggleRef("in:col_1", "in", collections[1])).toBe("in:col_1 in:col_2");
  });

  it("names the one collection a query is confined to", () => {
    expect(singleCollection("in:col_1 kick", collections)?.name).toBe("Techno set");
    expect(singleCollection('in:"techno set"', collections)?.id).toBe("col_1");
    expect(singleCollection("in:col_1 in:col_2", collections)).toBeNull();
    expect(singleCollection("-in:col_1", collections)).toBeNull();
    expect(singleCollection("in:gone", collections)).toBeNull();
    expect(singleCollection("in:Twin", [{ id: "a", name: "Twin" }, { id: "b", name: "twin" }])).toBeNull();
  });

  it("labels id chips with the name", () => {
    expect(chipLabel({ kind: "pack", value: "pk_C" }, names)).toBe('@12" Vinyl Breaks');
    expect(chipLabel({ kind: "in", value: "col_1", negated: true }, names)).toBe("-in:Techno set");
    expect(chipLabel({ kind: "pack", value: "pk_gone" }, names)).toBe("@pk_gone");
    expect(chipLabel({ kind: "pack", value: "pk_C" })).toBe("@pk_C");
  });

  it("describes a query in words", () => {
    expect(describeQuery("type:loop bpm:120-130 key:Am #drums/kick", names)).toBe("Loops · 120-130 BPM · Am · kick");
    expect(describeQuery("deep pack:pk_C -is:fav in:col_2 fmt:wav", names)).toBe(
      'deep · 12" Vinyl Breaks · not Favourites · Warm · WAV',
    );
    expect(describeQuery("")).toBe("");
  });

  it("suggests a view name that does not replace another view by accident", () => {
    const views = [
      { id: "v1", name: "Loops · Am", query: "type:loop key:Am" },
      { id: "v2", name: "loops · 124 bpm", query: "type:loop bpm:120" },
    ];
    // Already saved: the same name, so saving again is a no-op.
    expect(suggestViewName(" type:loop key:Am ", views)).toBe("Loops · Am");
    // The description is taken by a view with another query.
    expect(suggestViewName("type:loop bpm:124", views)).toBe("Loops · 124 BPM 2");
    expect(suggestViewName("is:fav", views)).toBe("Favourites");
  });
});
