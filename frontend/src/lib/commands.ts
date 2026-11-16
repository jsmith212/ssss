// Command registry: one list of actions that the keyboard map, the command
// palette and context menus all draw from.
//
// Key syntax: modifiers joined with "+", then one key, e.g. "Mod+K",
// "Mod+Shift+Z", "Shift+ArrowUp", "Space", "Delete", "?", "[", "Mod+\\".
// "Mod" is Cmd on macOS and Ctrl elsewhere. Keys are `KeyboardEvent.key`
// names ("ArrowUp", "Enter", "Escape", "Backspace", "PageDown"...), plus
// "Space"; letters are case-insensitive.

export interface Command {
  id: string;
  /** Shown in the palette and shortcut sheet. */
  title: string;
  /** One binding or several alternatives. */
  keys?: string | string[];
  /** Palette / shortcut sheet grouping, e.g. "Playback". */
  section?: string;
  /** The command is available only while this returns true. */
  when?: () => boolean;
  /**
   * Fire the shortcut even while a text input has focus. Without it, plain
   * keys (no Mod) and the text-editing combos (Mod+A/C/V/X/Z, Mod+Backspace,
   * Mod+arrows) are left to the input.
   */
  allowInInput?: boolean;
  /** Fire again while the key is held down (navigation); default false. */
  repeat?: boolean;
  /** Keep out of the palette (pure navigation keys and the like). */
  hidden?: boolean;
  /**
   * How the shortcut sheet shows the command: false leaves it out, a string
   * stands in for its keys (one line for a family of bindings).
   */
  sheet?: false | string;
  run: (event?: KeyboardEvent) => void | Promise<void>;
}

interface Binding {
  mod: boolean;
  shift: boolean;
  alt: boolean;
  /** Lower-cased key name. */
  key: string;
}

const commands = new Map<string, Command>();
let mac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/i.test(navigator.platform);

/** Overrides platform detection (the backend knows better than the UA). */
export function setMac(isMac: boolean): void {
  mac = isMac;
}

export function isMac(): boolean {
  return mac;
}

/** Adds a command, replacing any with the same id. Returns the unregister function. */
export function register(command: Command): () => void {
  commands.set(command.id, command);
  return () => {
    if (commands.get(command.id) === command) commands.delete(command.id);
  };
}

/** Registers several commands; the returned function removes them all. */
export function registerAll(list: Command[]): () => void {
  const undo = list.map(register);
  return () => undo.forEach((fn) => fn());
}

export function get(id: string): Command | undefined {
  return commands.get(id);
}

/** Commands for the palette: visible and currently available, in registration order. */
export function list(): Command[] {
  return [...commands.values()].filter((c) => !c.hidden && available(c));
}

/** Every registered command, for the shortcut sheet. */
export function all(): Command[] {
  return [...commands.values()];
}

/** Runs a command by id; false when it is unknown or unavailable. */
export function run(id: string, event?: KeyboardEvent): boolean {
  const command = commands.get(id);
  if (!command || !available(command)) return false;
  execute(command, event);
  return true;
}

function available(command: Command): boolean {
  return command.when ? command.when() : true;
}

function execute(command: Command, event?: KeyboardEvent): void {
  try {
    const result = command.run(event);
    if (result instanceof Promise) result.catch((err) => report(command, err));
  } catch (err) {
    report(command, err);
  }
}

let onError: (command: Command, error: unknown) => void = (command, error) =>
  console.error(`command ${command.id} failed`, error);

/** Routes command failures somewhere visible, e.g. a toast. */
export function setErrorHandler(handler: (command: Command, error: unknown) => void): void {
  onError = handler;
}

function report(command: Command, error: unknown): void {
  onError(command, error);
}

export function parseBinding(spec: string): Binding {
  // The key may itself be "+", so only split off known modifiers.
  const binding: Binding = { mod: false, shift: false, alt: false, key: "" };
  let rest = spec;
  for (;;) {
    const m = /^(mod|cmd|ctrl|shift|alt)\+(?=.)/i.exec(rest);
    if (!m) break;
    const name = m[1].toLowerCase();
    if (name === "shift") binding.shift = true;
    else if (name === "alt") binding.alt = true;
    else binding.mod = true;
    rest = rest.slice(m[0].length);
  }
  binding.key = rest.toLowerCase();
  return binding;
}

/** The key of an event in binding form: lower-cased `key`, " " as "space". */
function eventKey(e: KeyboardEvent): string {
  if (e.key === " " || e.code === "Space") return "space";
  // On non-Latin layouts `key` is not the letter printed in our shortcuts;
  // fall back to the physical key.
  if (e.key.length === 1 && e.key.charCodeAt(0) > 127) {
    const m = /^(?:Key([A-Z])|Digit(\d))$/.exec(e.code);
    if (m) return (m[1] ?? m[2]).toLowerCase();
  }
  return e.key.toLowerCase();
}

function isNamedOrAlnum(key: string): boolean {
  return key.length > 1 || /^[a-z0-9]$/.test(key);
}

/** Where the symbols we bind sit on a US keyboard, as `KeyboardEvent.code`. */
const PHYSICAL: Record<string, string> = {
  "\\": "Backslash",
  "[": "BracketLeft",
  "]": "BracketRight",
  ",": "Comma",
  ".": "Period",
  "/": "Slash",
};

export function matchesBinding(binding: Binding, e: KeyboardEvent, isMacPlatform = mac): boolean {
  const symbol = !isNamedOrAlnum(binding.key);
  let mod = isMacPlatform ? e.metaKey : e.ctrlKey;
  const otherMod = isMacPlatform ? e.ctrlKey : e.metaKey;
  let alt = e.altKey;
  if (symbol && !binding.alt) {
    // Many layouts type "[", "]" or "\" with Option (macOS) or AltGr, which
    // Windows reports as Ctrl+Alt. Those modifiers produced the character;
    // they are not part of the shortcut.
    if (!isMacPlatform && mod && alt) mod = false;
    alt = false;
  }
  if (binding.mod !== mod || otherMod || binding.alt !== alt) return false;
  const key = eventKey(e);
  // Symbols such as "?" or "+" already imply their Shift state on whatever
  // layout produced them, so Shift is only compared for letters, digits and
  // named keys.
  if (!symbol) return key === binding.key && binding.shift === e.shiftKey;
  if (key === binding.key) return true;
  // Mod+symbol cannot be typed through AltGr (it would read as the plain
  // symbol), so such layouts get the key at the symbol's US position.
  return binding.mod && !e.altKey && e.code === PHYSICAL[binding.key];
}

/**
 * True for a keydown that belongs to an IME composition. WebKit sends the
 * Enter that confirms a candidate after `compositionend`, with `isComposing`
 * already false; it is still recognisable by the key code 229.
 */
export function isComposing(e: KeyboardEvent): boolean {
  return e.isComposing || e.keyCode === 229;
}

const TEXT_EDITING = new Set(["a", "c", "v", "x", "z", "y", "backspace", "delete", "arrowleft", "arrowright", "arrowup", "arrowdown"]);

/** True when the event target is somewhere the user types text. */
export function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  if (!el || typeof el.tagName !== "string") return false;
  if (el.isContentEditable) return true;
  if (el.tagName === "TEXTAREA" || el.tagName === "SELECT") return true;
  if (el.tagName !== "INPUT") return false;
  const type = (el as HTMLInputElement).type;
  return !["checkbox", "radio", "button", "range", "submit", "reset", "color", "file"].includes(type);
}

/** Whether a binding must be left to a focused text input. */
function reservedForInput(binding: Binding): boolean {
  return !binding.mod || TEXT_EDITING.has(binding.key);
}

/**
 * Enter and Space on a focused button, link or menu item activate it; a
 * global shortcut on the same key must not steal that.
 */
function activatesControl(target: EventTarget | null, binding: Binding): boolean {
  if (binding.mod || binding.alt || (binding.key !== "enter" && binding.key !== "space")) return false;
  const el = target as HTMLElement | null;
  if (!el || typeof el.closest !== "function") return false;
  return el.closest("button, a[href], summary, [role='button'], [role='menuitem']") !== null;
}

/**
 * Finds the command for a key event. Later registrations win, so an open
 * overlay can shadow a global shortcut by registering its own.
 */
export function resolve(e: KeyboardEvent): Command | undefined {
  const typing = isTyping(e.target);
  const candidates = [...commands.values()].reverse();
  for (const command of candidates) {
    if (!command.keys) continue;
    if (e.repeat && !command.repeat) continue;
    const specs = Array.isArray(command.keys) ? command.keys : [command.keys];
    for (const spec of specs) {
      const binding = parseBinding(spec);
      if (!matchesBinding(binding, e)) continue;
      if (typing && !command.allowInInput && reservedForInput(binding)) continue;
      if (!command.allowInInput && activatesControl(e.target, binding)) continue;
      if (available(command)) return command;
    }
  }
  return undefined;
}

/** Handles a keydown; true when a command ran (the event is then consumed). */
export function dispatchKey(e: KeyboardEvent): boolean {
  if (e.defaultPrevented || isComposing(e)) return false;
  const command = resolve(e);
  if (!command) return false;
  e.preventDefault();
  execute(command, e);
  return true;
}

/** Installs the single global keydown listener. Returns the uninstall function. */
export function installKeyboard(target: Window | HTMLElement = window): () => void {
  const handler = (e: Event) => void dispatchKey(e as KeyboardEvent);
  target.addEventListener("keydown", handler);
  return () => target.removeEventListener("keydown", handler);
}

const KEY_LABELS: Record<string, string> = {
  arrowup: "↑",
  arrowdown: "↓",
  arrowleft: "←",
  arrowright: "→",
  escape: "Esc",
  backspace: "⌫",
  delete: "Del",
  enter: "↵",
  space: "Space",
  pageup: "PgUp",
  pagedown: "PgDn",
  home: "Home",
  end: "End",
};

/** Human-readable form of a binding: "⌘⇧Z" on macOS, "Ctrl+Shift+Z" elsewhere. */
export function formatKeys(spec: string, isMacPlatform = mac): string {
  const b = parseBinding(spec);
  const key = KEY_LABELS[b.key] ?? (b.key.length === 1 || /^f\d+$/.test(b.key) ? b.key.toUpperCase() : b.key);
  if (isMacPlatform) {
    return `${b.mod ? "⌘" : ""}${b.alt ? "⌥" : ""}${b.shift ? "⇧" : ""}${key}`;
  }
  return [b.mod && "Ctrl", b.alt && "Alt", b.shift && "Shift", key].filter(Boolean).join("+");
}

/** Removes every command; for tests. */
export function reset(): void {
  commands.clear();
}
