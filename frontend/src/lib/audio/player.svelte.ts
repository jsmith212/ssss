// The player: reactive playback state for the UI, and the actions that
// drive the engine. The list calls audition/prefetch/stop; the player bar
// and keyboard commands use the rest.

import { api } from "../api";
import { registerAll } from "../commands";
import {
  isOverlayOpen,
  playback,
  previewSettings,
  pushToast,
  selection,
  setAuditionHandler,
  settings,
  toggleOverlay,
  updateSettings,
} from "../state.svelte";
import type { Sample, Settings } from "../types";
import { Engine, type EngineState, type PlayParams } from "./engine";
import { computeMatch, isAltered, type Match } from "./match";
import { nudgeBpm, type ProjectPatch } from "./project";

const engine = new Engine({
  fetchAudio: (id, signal) => fetch(api.audioUrl(id), { signal }),
  onChange: applyEngineState,
  onStretchUnavailable: (reason) => {
    console.error("stretcher unavailable", reason);
    pushToast("Tempo and key matching is not available; samples play unmatched.", { level: "error" });
  },
});

const NOTHING = { type: "", bpm: 0, key: "" };

/** Whether a sample loops: loops do, one-shots do not, unless L overrode it this session. */
export function loopFor(sample: Pick<Sample, "type"> | null): boolean {
  return playback.loopOverride ?? sample?.type === "loop";
}

/** How a sample is tempo and key matched under the current project settings. */
export function matchFor(sample: Pick<Sample, "type" | "bpm" | "key" | "keyRoot"> | null): Match {
  return computeMatch(sample ?? NOTHING, settings());
}

class Player {
  /** The sample loaded in the player; it stays after playback ends. */
  sample = $state.raw<Sample | null>(null);
  playing = $state(false);
  /** Waiting for the audio to arrive. */
  loading = $state(false);
  /** Seconds; the listed length until the audio has loaded. */
  duration = $state(0);
  /** Playhead, seconds into the sample. Updated every frame while playing. */
  position = $state(0);
  /** Why the sample cannot be played; "" when fine. */
  error = $state("");

  readonly loop = $derived(loopFor(this.sample));
  /** Match flags, rate, shift and the tempo and key the sample sounds at. */
  readonly match = $derived(matchFor(this.sample));
  /** The matched (stretched) preview is what is sounding. */
  readonly matched = $derived(this.playing && isAltered(this.match));

  get id(): string | null {
    return this.sample?.id ?? null;
  }
}

export const player = new Player();

function paramsFor(sample: Sample): PlayParams {
  const match = matchFor(sample);
  return { loop: loopFor(sample), rate: match.rate, semitones: match.semitones };
}

function applyEngineState(state: EngineState): void {
  // Late news about a sample the player has moved on from.
  if (state.id !== player.id) return;
  player.playing = state.playing;
  player.loading = state.loading;
  player.error = state.error;
  if (state.duration > 0) player.duration = state.duration;
  if (!state.playing) player.position = 0;
  playback.playingId = state.playing ? state.id : null;
  if (state.playing) startTicker();
}

let ticking = false;

function startTicker(): void {
  if (ticking) return;
  ticking = true;
  const tick = () => {
    if (!player.playing) {
      ticking = false;
      return;
    }
    player.position = engine.position();
    requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
}

// --- the API for the list ---------------------------------------------------------

/**
 * Plays a sample now, from `offset` seconds, replacing whatever is sounding.
 * Safe to call as fast as keys repeat: the latest call wins. The caller
 * decides whether to call it at all (play-on-select, offline library).
 */
export function audition(sample: Sample, offset = 0): void {
  player.sample = sample;
  player.duration = sample.dur;
  player.position = offset;
  player.error = "";
  void engine.play(sample.id, paramsFor(sample), offset);
}

/** Warms the cache for samples likely to be auditioned next (rows around the focus). */
export function prefetch(samples: readonly Sample[]): void {
  engine.prefetch(
    samples
      .filter((s) => !s.missing)
      .slice(0, 8)
      .map((s) => s.id),
  );
}

export function stop(): void {
  engine.stop();
}

// --- transport ----------------------------------------------------------------------

/** The sample the player bar is about: the focused row, else the loaded sample. */
export function shownSample(): Sample | null {
  return selection.focusSample ?? player.sample;
}

/** Whether `sample` is the one sounding (or about to). */
export function isActive(sample: Sample | null): boolean {
  return sample !== null && sample.id === player.id && (player.playing || player.loading);
}

function playable(sample: Sample | null): sample is Sample {
  return sample !== null && !sample.missing;
}

/** Space: stops what is playing, otherwise plays the shown sample from the top. */
export function togglePlay(): void {
  const sample = shownSample();
  if (player.playing || player.loading) {
    const other = playable(sample) && sample.id !== player.id;
    stop();
    // Playback of a different row than the focused one: Space moves on to it.
    if (other) audition(sample);
    return;
  }
  if (playable(sample)) audition(sample);
}

/** Moves the playhead of `sample` (default: the shown one) and plays from there. */
export function seekTo(seconds: number, sample: Sample | null = shownSample()): void {
  if (!playable(sample)) return;
  if (sample.id === player.id && player.playing) {
    engine.seek(seconds);
    player.position = engine.position();
  } else {
    audition(sample, Math.max(0, seconds));
  }
}

/** Scrubbing: `fraction` 0..1 of the sample's length. */
export function seekFraction(fraction: number, sample: Sample | null = shownSample()): void {
  if (!sample) return;
  const duration = sample.id === player.id && player.duration > 0 ? player.duration : sample.dur;
  seekTo(Math.min(1, Math.max(0, fraction)) * duration, sample);
}

/** Arrow keys: jumps by a fraction of the length (±0.05), staying inside the sample. */
export function seekBy(fraction: number): void {
  const sample = shownSample();
  if (!sample) return;
  const active = sample.id === player.id;
  const duration = active && player.duration > 0 ? player.duration : sample.dur;
  const from = active && player.playing ? engine.position() : 0;
  let target = from + fraction * duration;
  if (active && player.loop && duration > 0) target = ((target % duration) + duration) % duration;
  seekTo(Math.min(Math.max(0, target), duration * 0.999), sample);
}

/**
 * L: flips looping. The choice overrides the per-type default for the
 * session; flipping back to what the shown sample does by default returns
 * to the defaults, so a one-shot does not stay looped by accident.
 */
export function toggleLoop(): void {
  const sample = shownSample();
  const next = !loopFor(sample);
  playback.loopOverride = next === (sample?.type === "loop") ? null : next;
}

// --- matching and the project -----------------------------------------------------------

export function toggleTempoMatch(): void {
  void updateSettings({ matchTempo: !settings().matchTempo });
}

export function toggleKeyMatch(): void {
  void updateSettings({ matchKey: !settings().matchKey });
}

/** M: tempo and key matching together; off if either is on. */
export function toggleMatch(): void {
  const on = !(settings().matchTempo || settings().matchKey);
  void updateSettings({ matchTempo: on, matchKey: on });
}

export function setProject(patch: ProjectPatch): void {
  if (patch.projectBpm === undefined && patch.projectKey === undefined) return;
  void updateSettings(patch);
}

export function nudgeProjectBpm(delta: number): void {
  const next = nudgeBpm(settings().projectBpm, delta);
  if (next !== settings().projectBpm) void updateSettings({ projectBpm: next });
}

/** Whether the project pill is in edit mode (B). */
export function editingProject(): boolean {
  return isOverlayOpen("project");
}

// --- volume ---------------------------------------------------------------------------------

/** Slider position to gain: squared, so the lower half of the slider is usable. */
function gainFor(volume: number): number {
  return volume * volume;
}

/**
 * Sets the volume (0..1). While a slider is being dragged pass
 * `persist: false` and persist once at the end.
 */
export function setVolume(volume: number, options: { persist?: boolean } = {}): void {
  const next = Math.round(Math.min(1, Math.max(0, volume)) * 100) / 100;
  if (options.persist === false) previewSettings({ volume: next });
  else void updateSettings({ volume: next });
}

export function nudgeVolume(delta: number): void {
  setVolume(settings().volume + delta);
}

let beforeMute = 0;

export function toggleMute(): void {
  const volume = settings().volume;
  if (volume > 0) {
    beforeMute = volume;
    setVolume(0);
  } else {
    setVolume(beforeMute > 0 ? beforeMute : 0.8);
  }
}

// --- wiring -------------------------------------------------------------------------------------

function registerCommands(): () => void {
  const hasSample = () => shownSample() !== null;
  return registerAll([
    {
      id: "playback.toggle",
      title: "Play / stop",
      keys: "Space",
      section: "Playback",
      when: hasSample,
      run: togglePlay,
    },
    {
      id: "playback.seekBack",
      title: "Seek back 5%",
      keys: "ArrowLeft",
      section: "Playback",
      when: hasSample,
      repeat: true,
      hidden: true,
      run: () => seekBy(-0.05),
    },
    {
      id: "playback.seekForward",
      title: "Seek forward 5%",
      keys: "ArrowRight",
      section: "Playback",
      when: hasSample,
      repeat: true,
      hidden: true,
      run: () => seekBy(0.05),
    },
    {
      id: "playback.loop",
      title: "Toggle loop",
      keys: "L",
      section: "Playback",
      run: toggleLoop,
    },
    {
      id: "playback.match",
      title: "Toggle tempo and key match",
      keys: "M",
      section: "Playback",
      run: toggleMatch,
    },
    {
      id: "playback.matchTempo",
      title: "Toggle tempo match",
      section: "Playback",
      run: toggleTempoMatch,
    },
    {
      id: "playback.matchKey",
      title: "Toggle key match",
      section: "Playback",
      run: toggleKeyMatch,
    },
    {
      id: "project.edit",
      title: "Edit project tempo and key",
      keys: "B",
      section: "Playback",
      run: () => toggleOverlay("project"),
    },
    {
      id: "project.bpmDown",
      title: "Project tempo −1",
      keys: "[",
      section: "Playback",
      repeat: true,
      run: () => nudgeProjectBpm(-1),
    },
    {
      id: "project.bpmUp",
      title: "Project tempo +1",
      keys: "]",
      section: "Playback",
      repeat: true,
      run: () => nudgeProjectBpm(1),
    },
    {
      id: "playback.volumeDown",
      title: "Volume down",
      keys: "-",
      section: "Playback",
      repeat: true,
      run: () => nudgeVolume(-0.05),
    },
    {
      id: "playback.volumeUp",
      title: "Volume up",
      keys: ["=", "+"],
      section: "Playback",
      repeat: true,
      run: () => nudgeVolume(0.05),
    },
  ]);
}

/** Webviews keep audio suspended until the first click or key press. */
function unlockOnGesture(): () => void {
  const unlock = () => {
    engine.unlock();
    if (engine.context.state === "running") remove();
  };
  const remove = () => {
    window.removeEventListener("pointerdown", unlock, true);
    window.removeEventListener("keydown", unlock, true);
  };
  window.addEventListener("pointerdown", unlock, true);
  window.addEventListener("keydown", unlock, true);
  return remove;
}

function follow(): () => void {
  return $effect.root(() => {
    $effect(() => {
      engine.setVolume(gainFor(settings().volume));
    });

    // Loop, match toggles, project tempo/key or the sample's own metadata
    // changed: carry on from the same spot with the new sound.
    $effect(() => {
      const sample = player.sample;
      if (!sample) return;
      engine.update(paramsFor(sample));
    });

    $effect(() => {
      playback.matched = player.matched;
    });

    // The list refetches rows after edits; pick up the fresh copy of the
    // loaded sample (new BPM or key) when the focus still points at it.
    $effect(() => {
      const focused = selection.focusSample;
      if (focused && focused.id === player.id && focused !== player.sample) player.sample = focused;
    });
  });
}

let mounted = 0;
let teardown: (() => void) | null = null;

/**
 * Connects the player to the app: the audition hook, keyboard commands, the
 * first-gesture unlock and the settings it follows. Call once from the
 * player bar; returns the disconnect function.
 */
export function initPlayer(): () => void {
  if (mounted++ === 0) {
    const off = [setAuditionHandler((sample) => audition(sample)), registerCommands(), unlockOnGesture(), follow()];
    teardown = () => off.forEach((fn) => fn());
  }
  return () => {
    if (--mounted > 0) return;
    stop();
    teardown?.();
    teardown = null;
  };
}

/** The engine, for diagnostics and end-to-end checks. */
export function audioEngine(): Engine {
  return engine;
}
