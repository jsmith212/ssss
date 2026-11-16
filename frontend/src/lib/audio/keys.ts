// Musical keys in the app's notation: sharps only, major = "C", minor = "Cm".

const NOTES = ["C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"];
const NATURAL: Record<string, number> = { c: 0, d: 2, e: 4, f: 5, g: 7, a: 9, b: 11 };

export interface Key {
  /** Pitch class 0..11, C = 0. */
  pc: number;
  minor: boolean;
}

/**
 * Parses "Am", "a minor", "Bb", "F#maj", "dbm"; null when the text is not a
 * key. Case-insensitive, so "AM" reads as A minor.
 */
export function parseKey(input: string): Key | null {
  const m = /^([a-g])([#♯b♭]?)\s*(.*)$/i.exec(input.trim());
  if (!m) return null;
  let pc = NATURAL[m[1].toLowerCase()];
  if (m[2] === "#" || m[2] === "♯") pc += 1;
  else if (m[2] !== "") pc -= 1;
  const mode = m[3].toLowerCase();
  let minor: boolean;
  if (mode === "" || mode === "maj" || mode === "major") minor = false;
  else if (mode === "m" || mode === "min" || mode === "minor") minor = true;
  else return null;
  return { pc: (pc + 12) % 12, minor };
}

export function formatKey(key: Key): string {
  return NOTES[key.pc] + (key.minor ? "m" : "");
}

/** The relative major of a minor key and vice versa (Am <-> C). */
export function relativeKey(key: Key): Key {
  return { pc: (key.pc + (key.minor ? 3 : 9)) % 12, minor: !key.minor };
}

export function transposeKey(key: Key, semitones: number): Key {
  return { pc: (((key.pc + semitones) % 12) + 12) % 12, minor: key.minor };
}
