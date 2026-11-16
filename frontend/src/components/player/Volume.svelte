<!-- Volume: the icon mutes; a slider appears above it on hover or focus. -->
<script lang="ts">
  import Volume1 from "@lucide/svelte/icons/volume-1";
  import Volume2 from "@lucide/svelte/icons/volume-2";
  import VolumeX from "@lucide/svelte/icons/volume-x";
  import { nudgeVolume, setVolume, toggleMute } from "../../lib/audio";
  import { settings } from "../../lib/state.svelte";

  const volume = $derived(settings().volume);
  const Icon = $derived(volume === 0 ? VolumeX : volume < 0.5 ? Volume1 : Volume2);

  let track = $state<HTMLDivElement>();
  let dragging = $state(false);

  function valueAt(event: PointerEvent): number {
    const rect = track!.getBoundingClientRect();
    return rect.height > 0 ? 1 - (event.clientY - rect.top) / rect.height : 0;
  }

  function onpointerdown(event: PointerEvent): void {
    if (event.button !== 0) return;
    event.preventDefault();
    track!.setPointerCapture(event.pointerId);
    dragging = true;
    setVolume(valueAt(event), { persist: false });
  }

  function onpointermove(event: PointerEvent): void {
    if (dragging) setVolume(valueAt(event), { persist: false });
  }

  function endDrag(): void {
    if (!dragging) return;
    dragging = false;
    setVolume(settings().volume);
  }

  function onkeydown(event: KeyboardEvent): void {
    const step = event.key === "ArrowUp" || event.key === "ArrowRight" ? 0.05 : event.key === "ArrowDown" || event.key === "ArrowLeft" ? -0.05 : 0;
    if (step === 0) return;
    // Keep the arrows for the slider instead of the list and the playhead.
    event.preventDefault();
    event.stopPropagation();
    nudgeVolume(step);
  }

  function onwheel(event: WheelEvent): void {
    event.preventDefault();
    nudgeVolume(event.deltaY < 0 ? 0.05 : -0.05);
  }
</script>

<div class="volume" class:dragging {onwheel}>
  <button class="mute" aria-label={volume === 0 ? "Unmute" : "Mute"} title="Volume (− / =)" onclick={toggleMute}>
    <Icon class="icon" />
  </button>
  <div class="pop popover">
    <div
      bind:this={track}
      class="track"
      role="slider"
      tabindex="0"
      aria-label="Volume"
      aria-orientation="vertical"
      aria-valuemin="0"
      aria-valuemax="100"
      aria-valuenow={Math.round(volume * 100)}
      {onpointerdown}
      {onpointermove}
      onpointerup={endDrag}
      onpointercancel={endDrag}
      onlostpointercapture={endDrag}
      {onkeydown}
    >
      <div class="rail"><div class="fill" style:height="{volume * 100}%"></div></div>
      <div class="thumb" style:bottom="{volume * 100}%"></div>
    </div>
  </div>
</div>

<style>
  .volume {
    position: relative;
    flex: none;
  }

  .mute {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    border-radius: var(--radius-control);
    transition: background var(--dur-fast) var(--ease);
  }

  .mute:hover,
  .volume:hover .mute {
    background: var(--bg-2);
  }

  .pop {
    position: absolute;
    bottom: 100%;
    left: 50%;
    z-index: var(--z-popover);
    padding: var(--space-4) var(--space-3);
    translate: -50% 0;
    opacity: 0;
    visibility: hidden;
    transition:
      opacity var(--dur-fast) var(--ease),
      visibility var(--dur-fast);
  }

  /* The padding bridges the gap to the icon, so the pointer can travel up
     without the popover closing. */
  .pop::after {
    content: "";
    position: absolute;
    inset: 100% 0 auto;
    height: var(--space-3);
  }

  .volume:hover .pop,
  .volume:focus-within .pop,
  .volume.dragging .pop {
    opacity: 1;
    visibility: visible;
  }

  .track {
    position: relative;
    width: 16px;
    height: 88px;
    touch-action: none;
  }

  .rail {
    position: absolute;
    inset: 0 6px;
    display: flex;
    align-items: flex-end;
    overflow: hidden;
    border-radius: 2px;
    background: var(--bg-3);
  }

  .fill {
    width: 100%;
    background: var(--text-2);
  }

  .thumb {
    position: absolute;
    left: 50%;
    width: 12px;
    height: 12px;
    border-radius: 50%;
    background: var(--text-1);
    translate: -50% 50%;
  }
</style>
