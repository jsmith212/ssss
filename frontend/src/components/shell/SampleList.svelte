<!--
  The virtualised result list: fixed-height rows positioned absolutely inside
  a spacer as tall as the whole result, with only the visible window mounted.
-->
<script lang="ts">
  import { onMount, untrack } from "svelte";
  import { prefetch } from "../../lib/audio";
  import { isMac } from "../../lib/commands";
  import { addFiles, registerShellCommands } from "../../lib/list/commands";
  import {
    attachViewport,
    canDragOut,
    dragOut,
    ensureRange,
    settleRename,
    list,
    LIST_PAD,
    OVERSCAN,
    pick,
    rowAt,
    rowHeight,
    sync,
  } from "../../lib/list/controller.svelte";
  import { DragTracker, LostRelease, type PressEvent } from "../../lib/list/dragout";
  import { visibleRange } from "../../lib/list/selection";
  import { watchTheme } from "../../lib/list/theme.svelte";
  import { singlePack } from "../../lib/query";
  import {
    app,
    libraryUsable,
    openOverlay,
    playback,
    selection,
    setQuery,
    settings,
    sidebar,
    view,
  } from "../../lib/state.svelte";
  import { openSampleMenu } from "../overlays/menus";
  import SampleRow from "./SampleRow.svelte";

  let viewport = $state<HTMLElement>();

  onMount(() => {
    const stops = [registerShellCommands(), watchTheme(), watchPressesElsewhere()];
    if (viewport) stops.push(attachViewport(viewport));
    return () => stops.forEach((stop) => stop());
  });

  $effect(() => sync(view.query, view.sort, view.desc, view.revision));

  const height = $derived(rowHeight());
  const first = $derived(visibleRange(list.scrollTop, list.viewportHeight, height, list.total, OVERSCAN, LIST_PAD).first);
  const last = $derived(visibleRange(list.scrollTop, list.viewportHeight, height, list.total, OVERSCAN, LIST_PAD).last);
  /**
   * The row being renamed stays mounted wherever the list is scrolled to, so
   * its field keeps the keyboard. The rename is always of the focused sample.
   */
  const renameIndex = $derived(list.renamingId !== null ? selection.focusIndex : -1);
  const indexes = $derived.by(() => {
    const mounted = Array.from({ length: Math.max(0, last - first + 1) }, (_, i) => first + i);
    if (renameIndex >= 0 && renameIndex < list.total && (renameIndex < first || renameIndex > last)) {
      mounted.push(renameIndex);
    }
    return mounted;
  });

  $effect(() => {
    void list.version;
    ensureRange(first, last);
    if (renameIndex >= 0) ensureRange(renameIndex, renameIndex);
  });

  $effect(() => {
    void selection.focusSample;
    void list.renamingId;
    untrack(settleRename);
  });

  // Warm the audio cache for the rows either side of the focus, nearest
  // first, so the next arrow key sounds at once.
  $effect(() => {
    void list.version;
    const at = selection.focusIndex;
    if (at < 0 || !settings().playOnSelect || !canDragOut()) return;
    const near = [at + 1, at - 1, at + 2, at - 2].map(rowAt).filter((row) => row !== undefined);
    untrack(() => prefetch(near));
  });

  const showPack = $derived(singlePack(view.query) === null);
  const multi = $derived(selection.ids.size > 1);
  const offline = $derived(app.state?.status === "unavailable");
  const emptyLibrary = $derived(list.ready && list.total === 0 && view.query.trim() === "" && (sidebar.data?.all ?? 0) === 0);
  const noResults = $derived(list.ready && list.total === 0 && !emptyLibrary);

  // --- pointer: select on press, drag out after 5px ------------------------------

  /** A stale :hover can survive the native drag; hide hover until the pointer moves. */
  let hoverSuppressed = $state(false);
  /** Row whose multi-selection collapses on release unless a drag started. */
  let collapseOnRelease = -1;

  const lostRelease = new LostRelease();

  const tracker = new DragTracker<number>((index) => {
    collapseOnRelease = -1;
    stopTracking();
    hoverSuppressed = true;
    lostRelease.dragStarted();
    void dragOut(index);
  });

  /**
   * The press that follows a drag-out may land outside the list. Seen in
   * the capture phase, it settles the lost release before a later, ordinary
   * click in the list could be mistaken for it.
   */
  function watchPressesElsewhere(): () => void {
    const types: PressEvent[] = ["pointerdown", "mousedown", "click"];
    const settle = (e: Event) => {
      if (!viewport?.contains(e.target as Node | null)) lostRelease.settle();
    };
    for (const type of types) window.addEventListener(type, settle, true);
    return () => {
      for (const type of types) window.removeEventListener(type, settle, true);
    };
  }

  function rowIndex(target: EventTarget | null): number {
    const row = (target as HTMLElement | null)?.closest?.<HTMLElement>("[data-index]");
    return row ? Number(row.dataset.index) : -1;
  }

  function isControl(target: EventTarget | null): boolean {
    return (target as HTMLElement | null)?.closest?.("[data-no-pick]") != null;
  }

  function onWindowMove(e: PointerEvent): void {
    tracker.move(e);
    if (!tracker.armed) stopTracking();
  }

  function onWindowUp(e: PointerEvent): void {
    tracker.up();
    stopTracking();
    if (collapseOnRelease >= 0 && rowIndex(e.target) === collapseOnRelease) pick(collapseOnRelease);
    collapseOnRelease = -1;
  }

  function stopTracking(): void {
    window.removeEventListener("pointermove", onWindowMove);
    window.removeEventListener("pointerup", onWindowUp);
    window.removeEventListener("pointercancel", onWindowUp);
  }

  /**
   * A primary-button press or click on a row. A press selects and arms the
   * drag; a click (a press whose start the webview withheld) only selects.
   */
  function onpress(e: MouseEvent): void {
    const role = lostRelease.role(e.type as PressEvent);
    if (role === null || e.button !== 0) return;
    // A click that never had its press is targeted at whatever the last
    // press the webview did see has in common with it; go by where it is.
    const target = role === "click" ? document.elementFromPoint(e.clientX, e.clientY) : e.target;
    if (isControl(target)) return;
    const index = rowIndex(target);
    const sample = index >= 0 ? rowAt(index) : undefined;
    if (!sample) return;
    const mods = { range: e.shiftKey, toggle: isMac() ? e.metaKey : e.ctrlKey };
    const plain = !mods.range && !mods.toggle;
    if (role === "click") {
      pick(index, mods);
      return;
    }
    // Pressing inside a multi-selection must not collapse it yet: the press
    // may be the start of dragging all of it out.
    if (plain && selection.ids.size > 1 && selection.ids.has(sample.id)) collapseOnRelease = index;
    else pick(index, mods);
    if (plain || selection.ids.has(sample.id)) {
      tracker.down(e, index);
      window.addEventListener("pointermove", onWindowMove);
      window.addEventListener("pointerup", onWindowUp);
      window.addEventListener("pointercancel", onWindowUp);
    }
  }

  function onpointermove(): void {
    if (hoverSuppressed && !list.dragging) hoverSuppressed = false;
  }

  // --- context menu -----------------------------------------------------------------

  function oncontextmenu(e: MouseEvent): void {
    e.preventDefault();
    const index = rowIndex(e.target);
    const sample = index >= 0 ? rowAt(index) : undefined;
    if (!sample) return;
    if (!selection.ids.has(sample.id)) pick(index);
    openSampleMenu(e, sample);
  }
</script>

<!-- Rows are picked with the pointer here and with the arrow keys through the command map. -->
<!-- svelte-ignore a11y_click_events_have_key_events -->
<div
  class="viewport"
  class:no-hover={hoverSuppressed || list.dragging}
  class:offline
  bind:this={viewport}
  bind:clientHeight={list.viewportHeight}
  role="listbox"
  aria-label="Samples"
  aria-multiselectable="true"
  tabindex="-1"
  onscroll={(e) => (list.scrollTop = e.currentTarget.scrollTop)}
  onpointerdown={onpress}
  onmousedown={onpress}
  onclick={onpress}
  {onpointermove}
  {oncontextmenu}
>
  <div class="spacer" style:height="{list.total * height + 2 * LIST_PAD}px">
    {#each indexes as index (index)}
      {@const sample = rowAt(index)}
      <SampleRow
        {sample}
        {index}
        top={LIST_PAD + index * height}
        {height}
        selected={sample !== undefined && selection.ids.has(sample.id)}
        focus={index === selection.focusIndex}
        focused={index === selection.focusIndex && (multi || (sample !== undefined && !selection.ids.has(sample.id)))}
        playing={sample !== undefined && playback.playingId === sample.id}
        matched={sample !== undefined && playback.matched && playback.playingId === sample.id}
        renaming={sample !== undefined && list.renamingId === sample.id}
        {showPack}
        skeleton={list.slow}
      />
    {/each}
  </div>

  {#if emptyLibrary}
    <div class="empty">
      <p class="lead invite">Drop samples or folders here</p>
      {#if libraryUsable()}
        <p class="note">A folder becomes a pack. Files are copied into your library.</p>
        <p class="actions">
          <button class="btn" type="button" onclick={() => openOverlay("splice")}>Import Splice folder</button>
          <button class="btn" type="button" onclick={addFiles}>Add files…</button>
        </p>
      {/if}
    </div>
  {:else if noResults}
    <div class="empty">
      <p class="lead">
        Nothing matches
        {#if view.query.trim() !== ""}
          <span class="dot">·</span>
          <button class="link" type="button" onclick={() => setQuery("")}>Clear filters</button>
        {/if}
      </p>
    </div>
  {/if}
</div>

<style>
  .viewport {
    position: absolute;
    inset: 0;
    overflow-x: hidden;
    overflow-y: auto;
    container: list / inline-size;
    outline: none;
  }

  .spacer {
    position: relative;
    /* Rows are positioned inside; keep their layout out of the page's. */
    contain: layout style;
  }

  .offline .spacer {
    opacity: 0.5;
  }

  .empty {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--space-4);
    color: var(--text-2);
  }

  .lead {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }

  .invite {
    color: var(--text-1);
    font-size: var(--text-omnibar);
    font-weight: 500;
  }

  .note {
    margin-top: calc(-1 * var(--space-3));
    color: var(--text-3);
  }

  .actions {
    display: flex;
    gap: var(--space-3);
    margin-top: var(--space-3);
  }

  .dot {
    color: var(--text-3);
  }

  .link {
    color: var(--text-1);
    text-decoration: underline;
    text-decoration-color: var(--text-3);
    text-underline-offset: 3px;
  }

  .link:hover {
    text-decoration-color: currentColor;
  }
</style>
