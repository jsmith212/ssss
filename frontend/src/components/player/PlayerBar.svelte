<!-- Transport for the focused sample: play/stop, scrub waveform, loop, match, volume, project. -->
<script lang="ts">
  import { onMount } from "svelte";
  import Play from "@lucide/svelte/icons/play";
  import Repeat from "@lucide/svelte/icons/repeat";
  import Square from "@lucide/svelte/icons/square";
  import {
    formatBpm,
    formatSemitones,
    initPlayer,
    isActive,
    loopFor,
    matchFor,
    player,
    shownSample,
    toggleKeyMatch,
    toggleLoop,
    togglePlay,
    toggleTempoMatch,
  } from "../../lib/audio";
  import { formatKeys } from "../../lib/commands";
  import { app, settings } from "../../lib/state.svelte";
  import MatchToggle from "./MatchToggle.svelte";
  import ProjectPill from "./ProjectPill.svelte";
  import Volume from "./Volume.svelte";
  import Waveform from "./Waveform.svelte";

  onMount(() => initPlayer());

  const sample = $derived(shownSample());
  const current = $derived(sample !== null && sample.id === player.id);
  const active = $derived(isActive(sample));
  const loop = $derived(loopFor(sample));
  const match = $derived(matchFor(sample));
  // Files can be heard whenever the library folder is reachable.
  const online = $derived(app.state?.status === "ok" || app.state?.status === "readonly");
  const playable = $derived(sample !== null && !sample.missing && online);

  const duration = $derived(current && player.duration > 0 ? player.duration : (sample?.dur ?? 0));

  function clock(seconds: number): string {
    const whole = Math.floor(seconds);
    return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`;
  }

  const status = $derived.by(() => {
    if (!sample) return "";
    if (sample.missing) return "File is missing";
    if (current && player.error) return `Can’t play: ${player.error}`;
    // One-shots read in hundredths; a running clock only makes sense beyond a second.
    if (duration < 1) return `${duration.toFixed(2)}s`;
    const length = clock(Math.round(duration));
    return current && player.playing ? `${clock(player.position)} / ${length}` : length;
  });
  const failed = $derived(sample !== null && (sample.missing || (current && player.error !== "")));

  const tempoValue = $derived(match.effectiveBpm > 0 ? formatBpm(match.effectiveBpm) : "–");
  const keyValue = $derived(
    match.effectiveKey === "" ? "–" : match.key ? formatSemitones(match.semitones) : match.effectiveKey,
  );
  const tempoTitle = $derived(
    !sample || match.canTempo
      ? `Match tempo to the project (${formatBpm(settings().projectBpm)} BPM)`
      : sample.type === "loop"
        ? "Tempo match needs a BPM for this sample"
        : "Tempo match applies to loops",
  );
  const keyTitle = $derived(
    settings().projectKey === ""
      ? `Key match needs a project key (${formatKeys("B")})`
      : !sample || match.canKey
        ? `Match key to the project (${settings().projectKey})${match.key ? ` · sounds in ${match.effectiveKey}` : ""}`
        : "Key match needs a key for this sample",
  );
</script>

<footer class="player" class:empty={!sample}>
  <button
    class="play"
    class:active
    disabled={!playable}
    aria-label={active ? "Stop" : "Play"}
    title="{active ? 'Stop' : 'Play'} ({formatKeys('Space')})"
    onclick={togglePlay}
  >
    {#if active}
      <Square class="icon glyph" />
    {:else}
      <Play class="icon glyph" />
    {/if}
  </button>

  <div class="about">
    {#if sample}
      <span class="name truncate" title={sample.name}>{sample.name}<span class="ext">.{sample.ext}</span></span>
      <span class="meta num truncate" class:failed>{status}</span>
    {:else}
      <span class="meta">No sample selected</span>
    {/if}
  </div>

  <Waveform {sample} enabled={playable} />

  <div class="controls">
    <button
      class="icon-button"
      class:on={loop}
      aria-pressed={loop}
      aria-label="Loop"
      title="Loop ({formatKeys('L')})"
      onclick={toggleLoop}
    >
      <Repeat class="icon" />
    </button>
    <MatchToggle
      caption="Tempo"
      value={tempoValue}
      on={settings().matchTempo}
      applies={match.tempo}
      title="{tempoTitle} · {formatKeys('M')} toggles tempo and key"
      onclick={toggleTempoMatch}
    />
    <MatchToggle
      caption="Key"
      value={keyValue}
      on={settings().matchKey}
      applies={match.key}
      title="{keyTitle} · {formatKeys('M')} toggles tempo and key"
      onclick={toggleKeyMatch}
    />
    <Volume />
    <ProjectPill />
  </div>
</footer>

<style>
  .player {
    display: flex;
    align-items: center;
    gap: var(--space-5);
    flex: none;
    height: var(--player-height);
    padding: 0 var(--space-5);
    border-top: 1px solid var(--border);
    background: var(--bg-1);
  }

  .play {
    display: grid;
    flex: none;
    place-items: center;
    width: 32px;
    height: 32px;
    border-radius: 50%;
    background: var(--bg-3);
    transition: background var(--dur-fast) var(--ease);
  }

  .play:hover {
    background: color-mix(in srgb, var(--bg-3) 80%, var(--text-1));
  }

  .play:disabled {
    opacity: 0.4;
    pointer-events: none;
  }

  .play :global(.glyph) {
    color: var(--text-1);
    fill: currentColor;
  }

  .play.active :global(.glyph) {
    width: 12px;
    height: 12px;
    color: var(--accent);
  }

  /* The triangle's visual centre sits left of its box centre. */
  .play:not(.active) :global(.glyph) {
    translate: 1px 0;
  }

  .about {
    display: flex;
    flex: none;
    flex-direction: column;
    gap: var(--space-1);
    width: 176px;
    min-width: 0;
  }

  .name {
    font-weight: 500;
  }

  .ext,
  .meta {
    color: var(--text-3);
  }

  .ext {
    font-weight: 400;
  }

  .meta {
    font-size: var(--text-meta);
  }

  .meta.failed {
    color: var(--danger);
  }

  .controls {
    display: flex;
    flex: none;
    align-items: center;
    gap: var(--space-2);
  }

  .controls > :global(.project) {
    margin-left: var(--space-3);
  }

  .icon-button {
    display: grid;
    flex: none;
    place-items: center;
    width: 28px;
    height: 28px;
    border-radius: var(--radius-control);
    transition: background var(--dur-fast) var(--ease);
  }

  .icon-button:hover {
    background: var(--bg-2);
  }

  .icon-button.on :global(.icon) {
    color: var(--accent);
  }

  @media (max-width: 760px) {
    .about {
      width: 120px;
    }
  }
</style>
