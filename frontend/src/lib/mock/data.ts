// Deterministic fake library for the mock backend: about 20k samples across
// 40 packs, with tags, tempo, keys, collections and saved views.

import type { MetaSource, Sample, SampleType, SavedView } from "../types";
import { haystack } from "./query";
import { Rng } from "./rng";

/** A sample plus the mock's private bookkeeping. */
export interface MockSample extends Sample {
  /** Lower-cased search text; rebuilt by `reindex` after edits. */
  hay: string;
  /** Drives the synthesised audio and waveform. */
  seed: number;
  /** Which synth voice to use. */
  voice: Voice;
  note: string;
  suggestedTags: string[];
}

export type Voice = "kick" | "snare" | "hat" | "perc" | "bass" | "tonal" | "pad" | "noise";

export interface MockPack {
  id: string;
  name: string;
  dir: string;
  source: string;
}

export interface MockCollection {
  id: string;
  name: string;
  items: Set<string>;
}

export interface MockLibrary {
  samples: MockSample[];
  packs: MockPack[];
  collections: MockCollection[];
  views: SavedView[];
  tagColors: Map<string, string>;
  /** Ids of the last import batch. */
  recent: Set<string>;
}

interface Category {
  tag: string;
  folder: string;
  /** Words used in file names. */
  words: string[];
  voice: Voice;
  /** Share of samples in this category that are loops. */
  loops: number;
  tonal: boolean;
  weight: number;
}

const CATEGORIES: Category[] = [
  { tag: "drums/kick", folder: "Drums/Kicks", words: ["Kick"], voice: "kick", loops: 0, tonal: false, weight: 10 },
  { tag: "drums/snare", folder: "Drums/Snares", words: ["Snare", "Rim"], voice: "snare", loops: 0, tonal: false, weight: 8 },
  { tag: "drums/clap", folder: "Drums/Claps", words: ["Clap", "Snap"], voice: "snare", loops: 0, tonal: false, weight: 5 },
  { tag: "drums/hat/closed", folder: "Drums/Hats", words: ["HiHat_Closed", "CH", "Hat"], voice: "hat", loops: 0, tonal: false, weight: 7 },
  { tag: "drums/hat/open", folder: "Drums/Hats", words: ["HiHat_Open", "OH"], voice: "hat", loops: 0, tonal: false, weight: 4 },
  { tag: "drums/perc", folder: "Drums/Percussion", words: ["Perc", "Conga", "Shaker", "Tom", "Bongo"], voice: "perc", loops: 0.2, tonal: false, weight: 7 },
  { tag: "drums/cymbal", folder: "Drums/Cymbals", words: ["Crash", "Ride"], voice: "hat", loops: 0, tonal: false, weight: 2 },
  { tag: "drums/loop", folder: "Loops/Drums", words: ["Drum_Loop", "Top_Loop", "Full_Drums", "Break"], voice: "kick", loops: 1, tonal: false, weight: 12 },
  { tag: "bass", folder: "Bass", words: ["Bass", "Sub", "Reese", "808"], voice: "bass", loops: 0.55, tonal: true, weight: 10 },
  { tag: "synth/lead", folder: "Synths/Leads", words: ["Lead", "Arp", "Stab"], voice: "tonal", loops: 0.6, tonal: true, weight: 7 },
  { tag: "synth/pad", folder: "Synths/Pads", words: ["Pad", "Strings", "Drone"], voice: "pad", loops: 0.5, tonal: true, weight: 5 },
  { tag: "synth/pluck", folder: "Synths/Plucks", words: ["Pluck", "Bell", "Mallet"], voice: "tonal", loops: 0.4, tonal: true, weight: 5 },
  { tag: "keys/piano", folder: "Keys", words: ["Piano", "Upright", "Grand"], voice: "tonal", loops: 0.7, tonal: true, weight: 4 },
  { tag: "keys/rhodes", folder: "Keys", words: ["Rhodes", "Wurli", "Organ"], voice: "tonal", loops: 0.7, tonal: true, weight: 3 },
  { tag: "guitar", folder: "Guitars", words: ["Guitar", "Nylon", "Strum"], voice: "tonal", loops: 0.8, tonal: true, weight: 4 },
  { tag: "vocal/chop", folder: "Vocals/Chops", words: ["Vox_Chop", "Vocal_Shot", "Adlib"], voice: "tonal", loops: 0.1, tonal: true, weight: 4 },
  { tag: "vocal/phrase", folder: "Vocals/Phrases", words: ["Vocal_Phrase", "Hook", "Verse"], voice: "pad", loops: 0.8, tonal: true, weight: 3 },
  { tag: "fx/riser", folder: "FX", words: ["Riser", "Uplifter", "Build"], voice: "noise", loops: 0, tonal: false, weight: 3 },
  { tag: "fx/impact", folder: "FX", words: ["Impact", "Boom", "Downer"], voice: "kick", loops: 0, tonal: false, weight: 3 },
  { tag: "texture/foley", folder: "Textures", words: ["Foley", "Vinyl", "Field"], voice: "noise", loops: 0.3, tonal: false, weight: 3 },
  { tag: "texture/ambience", folder: "Textures", words: ["Ambience", "Atmos", "Room_Tone"], voice: "pad", loops: 0.3, tonal: false, weight: 2 },
];

const ADJECTIVES = [
  "Deep", "Dark", "Warm", "Dusty", "Crisp", "Fat", "Tight", "Lush", "Gritty", "Clean", "Analog", "Hard", "Soft",
  "Bright", "Vintage", "Dirty", "Punchy", "Airy", "Heavy", "Short", "Long", "Wide", "Lofi", "Tape", "Glassy",
];

const GENRES = ["Deep House", "Techno", "Lo-Fi", "Trap", "Drum & Bass", "Garage", "Afro House", "Ambient", "Disco", "Synthwave"];
const PACK_KINDS = ["Essentials", "Drums", "Toolkit", "Sessions"];
const PREFIXES = ["DH", "TK", "LF", "TR", "DB", "UG", "AF", "AM", "DS", "SW"];
const TEMPOS = [
  [118, 126], [124, 138], [70, 92], [130, 150], [170, 176], [128, 136], [116, 124], [60, 110], [110, 124], [84, 110],
];

const KEYS = ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"];
const EXTS = ["wav", "wav", "wav", "wav", "wav", "wav", "wav", "aif", "flac", "mp3", "ogg"];
const DAY = 86_400_000;
// A fixed "now" keeps the generated `added` times stable between loads.
const EPOCH = Date.UTC(2026, 10, 1);

function weighted(rng: Rng): Category {
  const total = CATEGORIES.reduce((n, c) => n + c.weight, 0);
  let roll = rng.next() * total;
  for (const c of CATEGORIES) {
    roll -= c.weight;
    if (roll < 0) return c;
  }
  return CATEGORIES[0];
}

function source(rng: Rng, fromFilename: boolean): MetaSource {
  if (rng.chance(0.06)) return "user";
  if (rng.chance(0.2)) return "splice";
  return fromFilename ? "filename" : "detected";
}

/** Recomputes the derived search text after a sample was edited. */
export function reindex(s: MockSample): void {
  s.hay = haystack(s);
}

/**
 * Builds one sample. `index` only makes names unique within a pack.
 */
export function makeSample(
  rng: Rng,
  pack: MockPack,
  genre: number,
  index: number,
  added: number,
): MockSample {
  const cat = weighted(rng);
  const loop = rng.chance(cat.loops);
  const type: SampleType = loop ? "loop" : "oneshot";
  const word = rng.pick(cat.words);
  const adjective = rng.pick(ADJECTIVES);
  const minor = rng.chance(0.65);
  const hasKey = cat.tonal && rng.chance(0.85);
  const key = hasKey ? rng.pick(KEYS) + (minor ? "m" : "") : "";
  const [lo, hi] = TEMPOS[genre];
  const bpm = loop ? rng.int(lo, hi) : 0;

  const parts = [PREFIXES[genre]];
  if (loop) parts.push(String(bpm));
  if (key && rng.chance(0.7)) parts.push(key);
  parts.push(adjective, word);
  if (loop && !/Loop/.test(word)) parts.push("Loop");
  parts.push(String((index % 97) + 1).padStart(2, "0"));
  const name = parts.join("_");
  const ext = rng.pick(EXTS);

  const bars = rng.pick([1, 2, 2, 4, 4, 8]);
  const dur = loop
    ? (bars * 4 * 60) / bpm
    : cat.voice === "pad" || cat.voice === "noise"
      ? 1.5 + rng.next() * 6
      : 0.08 + rng.next() * rng.next() * 2.2;

  const analyzed = !rng.chance(0.015);
  const tagged = !rng.chance(0.1);
  const tags = tagged ? [cat.tag] : [];
  if (tagged && rng.chance(0.25)) tags.push(`mood/${adjective.toLowerCase()}`);
  const ch = cat.voice === "kick" || cat.voice === "bass" ? rng.pick([1, 2]) : 2;
  const sr = rng.pick([44100, 44100, 44100, 48000]);

  const id = rng.hex(32);
  const bpmSrc: MetaSource = analyzed && bpm ? source(rng, true) : "";
  const keySrc: MetaSource = analyzed && key ? source(rng, name.includes(`_${key}_`)) : "";
  const s: MockSample = {
    id,
    packId: pack.id,
    packName: pack.name,
    name,
    ext,
    relPath: `${cat.folder}/${name}.${ext}`,
    size: Math.round(dur * sr * ch * (ext === "mp3" || ext === "ogg" ? 0.3 : 3)) + 44,
    dur: Math.round(dur * 1000) / 1000,
    sr,
    ch,
    type: analyzed ? type : "",
    bpm: analyzed ? bpm : 0,
    bpmSrc,
    key: analyzed ? key : "",
    keySrc,
    // As analysis does it: the detected key of a one-shot is its root note,
    // written like a major key.
    keyRoot: keySrc === "detected" && type === "oneshot" && !key.endsWith("m"),
    tags,
    fav: rng.chance(0.03),
    added,
    missing: rng.chance(0.002),
    analyzed,
    mini: "",
    hay: "",
    seed: rng.int(1, 0x7fffffff),
    voice: cat.voice,
    note: "",
    suggestedTags: tagged ? [] : [cat.tag],
  };
  reindex(s);
  return s;
}

export function generateLibrary(sampleCount: number, seed = 20261115): MockLibrary {
  const rng = new Rng(seed);
  const packs: MockPack[] = [];
  const genreOf = new Map<string, number>();

  for (let g = 0; g < GENRES.length; g++) {
    for (let k = 0; k < PACK_KINDS.length; k++) {
      const name = `${GENRES[g]} ${PACK_KINDS[k]}${k === 3 ? ` Vol ${rng.int(1, 6)}` : ""}`;
      const splice = rng.chance(0.4);
      const pack: MockPack = {
        id: `pk_${rng.hex(26)}`,
        name,
        dir: splice ? `Splice/${name}` : name,
        source: splice ? "splice" : "import",
      };
      packs.push(pack);
      genreOf.set(pack.id, g);
    }
  }

  // Uneven pack sizes: a few big packs and a long tail, like a real library.
  const weights = packs.map(() => 0.3 + rng.next() * rng.next() * 3);
  const weightSum = weights.reduce((a, b) => a + b, 0);
  const samples: MockSample[] = [];
  const recent = new Set<string>();

  packs.forEach((pack, p) => {
    const last = p === packs.length - 1;
    const count = last
      ? Math.max(0, sampleCount - samples.length)
      : Math.round((weights[p] / weightSum) * sampleCount);
    // Packs were imported one after another over the past year.
    const importedAt = EPOCH - (packs.length - p) * 9 * DAY;
    for (let i = 0; i < count && samples.length < sampleCount; i++) {
      const s = makeSample(rng, pack, genreOf.get(pack.id)!, i, importedAt + i * 50);
      samples.push(s);
      if (last) recent.add(s.id);
    }
  });

  const collections: MockCollection[] = ["Techno set", "Friday sketch", "Best kicks", "Vocal ideas", "To resample"].map(
    (name) => {
      const items = new Set<string>();
      const size = Math.min(rng.int(12, 80), samples.length);
      while (items.size < size) items.add(rng.pick(samples).id);
      return { id: `col_${rng.hex(26)}`, name, items };
    },
  );

  const views: SavedView[] = [
    { id: `view_${rng.hex(8)}`, name: "Dark kicks", query: "#kick dark" },
    { id: `view_${rng.hex(8)}`, name: "House loops", query: "type:loop bpm:120-128" },
    { id: `view_${rng.hex(8)}`, name: "A minor, long", query: "key:compatible:Am len:>4s" },
  ];

  const tagColors = new Map<string, string>([
    ["drums", "#F5B83D"],
    ["bass", "#F2555A"],
    ["synth", "#7C9CFF"],
    ["vocal", "#3DD68C"],
  ]);

  return { samples, packs, collections, views, tagColors, recent };
}

/** Pack names offered by the fake Splice folder. */
export const SPLICE_PACKS = [
  { name: "Night Drive Synthwave", count: 214, genre: "Synthwave" },
  { name: "Basement Techno Tools", count: 380, genre: "Techno" },
  { name: "Soulful House Keys", count: 126, genre: "House" },
  { name: "Tape Drums Vol 2", count: 92, genre: "Lo-Fi" },
  { name: "Jungle Breaks 94", count: 158, genre: "Drum & Bass" },
];

export function genreIndex(name: string): number {
  const i = GENRES.findIndex((g) => name.toLowerCase().includes(g.toLowerCase()));
  return i >= 0 ? i : 0;
}
