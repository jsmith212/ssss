<!--
  The large scrub waveform: click or drag to move the playhead. Draws the
  row's mini peaks at once and swaps in the full-resolution peaks when they
  arrive.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import { player, seekFraction } from "../../lib/audio";
  import { cachedPeaks, loadPeaks } from "../../lib/audio/peaks";
  import {
    barCount,
    barsFromMini,
    barsFromPeaks,
    beatTicks,
    drawWaveform,
    prepareCanvas,
    readColors,
    watchAppearance,
    type WaveColors,
  } from "../../lib/audio/waveform";
  import type { Sample } from "../../lib/types";

  interface Props {
    sample: Sample | null;
    /** Scrubbing is possible (the file can be played). */
    enabled: boolean;
  }

  let { sample, enabled }: Props = $props();

  let box = $state<HTMLDivElement>();
  let canvas = $state<HTMLCanvasElement>();
  let width = $state(0);
  let height = $state(0);
  let colors = $state.raw<WaveColors | null>(null);
  let peaks = $state.raw<{ id: string; data: Int8Array } | null>(null);
  let scrubbing = $state(false);

  // Full peaks, fetched per sample; a fast run through the list aborts the
  // requests it leaves behind.
  $effect(() => {
    const id = sample?.id;
    const analyzed = sample?.analyzed;
    if (!id || sample?.missing) return;
    const hit = cachedPeaks(id);
    if (hit) {
      peaks = { id, data: hit };
      return;
    }
    void analyzed; // refetch once analysis has produced peaks
    const controller = new AbortController();
    loadPeaks(id, api.peaksUrl(id), controller.signal).then(
      (data) => (peaks = { id, data }),
      () => {
        // The mini peaks stay; nothing to report for a missing overview.
      },
    );
    return () => controller.abort();
  });

  $effect(() => {
    if (!box) return;
    const el = box;
    const measure = () => {
      width = el.clientWidth;
      height = el.clientHeight;
      colors = readColors(el);
    };
    measure();
    return watchAppearance(el, measure);
  });

  const bars = $derived.by(() => {
    const count = barCount(width);
    if (!sample || count === 0) return new Float32Array(0);
    if (peaks && peaks.id === sample.id) return barsFromPeaks(peaks.data, count);
    return barsFromMini(sample.mini, count);
  });

  const current = $derived(sample !== null && sample.id === player.id);
  const duration = $derived(current && player.duration > 0 ? player.duration : (sample?.dur ?? 0));
  const progress = $derived(current && duration > 0 ? Math.min(1, player.position / duration) : 0);
  const showPlayhead = $derived(current && (player.playing || player.position > 0));
  const ticks = $derived(sample && sample.type === "loop" ? beatTicks(sample.bpm, duration, width) : []);

  $effect(() => {
    if (!canvas || !colors) return;
    const ctx = prepareCanvas(canvas, width, height);
    if (!ctx) return;
    drawWaveform(ctx, { width, height, bars, colors, progress, playhead: showPlayhead, ticks });
  });

  function fractionAt(event: PointerEvent): number {
    const rect = box!.getBoundingClientRect();
    return rect.width > 0 ? (event.clientX - rect.left) / rect.width : 0;
  }

  let pendingFraction: number | null = null;

  /** At most one seek per frame while dragging. */
  function scrubTo(fraction: number): void {
    const scheduled = pendingFraction !== null;
    pendingFraction = fraction;
    if (scheduled) return;
    requestAnimationFrame(() => {
      const f = pendingFraction;
      pendingFraction = null;
      if (f !== null) seekFraction(f, sample);
    });
  }

  function onpointerdown(event: PointerEvent): void {
    if (event.button !== 0 || !enabled || !sample) return;
    event.preventDefault();
    box!.setPointerCapture(event.pointerId);
    scrubbing = true;
    seekFraction(fractionAt(event), sample);
  }

  function onpointermove(event: PointerEvent): void {
    if (scrubbing) scrubTo(fractionAt(event));
  }

  function endScrub(): void {
    scrubbing = false;
  }
</script>

<div
  bind:this={box}
  class="waveform"
  class:enabled={enabled && sample !== null}
  role="slider"
  tabindex="-1"
  aria-label="Playback position"
  aria-valuemin="0"
  aria-valuemax={Math.round(duration * 100) / 100}
  aria-valuenow={Math.round(progress * duration * 100) / 100}
  aria-disabled={!enabled || sample === null}
  {onpointerdown}
  {onpointermove}
  onpointerup={endScrub}
  onpointercancel={endScrub}
  onlostpointercapture={endScrub}
>
  <canvas bind:this={canvas}></canvas>
</div>

<style>
  .waveform {
    flex: 1;
    min-width: 0;
    height: 48px;
    touch-action: none;
  }

  .waveform:not(.enabled) {
    opacity: 0.5;
  }

  canvas {
    width: 100%;
    height: 100%;
  }
</style>
