// Audio: what the rest of the app needs from the player.
//
//   audition(sample)   play it now (the list calls this as the focus moves)
//   prefetch(samples)  warm the cache for the rows around the focus
//   stop()             silence

export {
  audition,
  editingProject,
  initPlayer,
  isActive,
  loopFor,
  matchFor,
  nudgeProjectBpm,
  nudgeVolume,
  player,
  prefetch,
  seekBy,
  seekFraction,
  seekTo,
  setProject,
  setVolume,
  shownSample,
  stop,
  toggleKeyMatch,
  toggleLoop,
  toggleMatch,
  toggleMute,
  togglePlay,
  toggleTempoMatch,
} from "./player.svelte";
export { formatBpm, formatSemitones, type Match } from "./match";
export { formatProject, parseProject } from "./project";
export {
  barCount,
  barsFromMini,
  barsFromPeaks,
  beatTicks,
  drawWaveform,
  prepareCanvas,
  readColors,
  watchAppearance,
  type Tick,
  type WaveColors,
} from "./waveform";
