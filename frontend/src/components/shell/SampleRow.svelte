<!--
  One list row. Presentational: clicks, drags and the context menu are
  handled by SampleList through the data-index attribute.
-->
<script lang="ts">
  import Heart from "@lucide/svelte/icons/heart";
  import { api } from "../../lib/api";
  import { cancelRename, commitRename, focusList, list } from "../../lib/list/controller.svelte";
  import { formatBpm, formatLength, rowTags, unknownMark } from "../../lib/list/format";
  import { drawMini, MINI_WIDTH } from "../../lib/list/mini";
  import { cssColor } from "../../lib/list/theme.svelte";
  import { isComposing } from "../../lib/commands";
  import { errorMessage, libraryUsable, pushToast } from "../../lib/state.svelte";
  import type { Sample } from "../../lib/types";

  interface Props {
    /** Undefined while the row's page is loading. */
    sample: Sample | undefined;
    index: number;
    top: number;
    height: number;
    selected: boolean;
    /** The keyboard focus is on this row. */
    focus: boolean;
    /** Keyboard focus that is worth showing (it differs from the selection). */
    focused: boolean;
    playing: boolean;
    /** Tempo/key matched preview is sounding for this row. */
    matched: boolean;
    renaming: boolean;
    showPack: boolean;
    skeleton: boolean;
  }

  let { sample, index, top, height, selected, focus, focused, playing, matched, renaming, showPack, skeleton }: Props =
    $props();

  let canvas = $state<HTMLCanvasElement>();
  const miniHeight = $derived(height - 12);

  $effect(() => {
    if (canvas) drawMini(canvas, sample?.mini ?? "", cssColor("--text-3"), miniHeight);
  });

  const tags = $derived(rowTags(sample?.tags ?? []));

  async function toggleFav(s: Sample): Promise<void> {
    try {
      await api.setFavorite([s.id], !s.fav);
    } catch (err) {
      pushToast(errorMessage(err), { level: "error" });
    }
  }

  /**
   * The inline rename field. The draft and the rename itself live in the
   * list controller, because this input comes and goes with its row: a
   * remounted field picks the draft up where the last one left it.
   */
  function editName(input: HTMLInputElement, s: Sample) {
    input.value = list.renameDraft;
    input.focus();
    // A fresh rename starts with the name selected, ready to be replaced;
    // once it has been edited the caret goes to the end instead.
    if (list.renameDraft === s.name) input.select();
    const oninput = () => {
      list.renameDraft = input.value;
    };
    const onkeydown = (e: KeyboardEvent) => {
      if (isComposing(e)) return;
      if (e.key === "Enter") void commitRename();
      else if (e.key === "Escape") cancelRename();
      else return;
      e.preventDefault();
      e.stopPropagation();
      focusList();
    };
    // Chromium fires blur while the row is being removed. That is not the
    // user leaving the field, and state must not be written during teardown,
    // so look again once the removal is over.
    const onblur = () => {
      queueMicrotask(() => {
        if (input.isConnected && document.activeElement !== input) void commitRename();
      });
    };
    input.addEventListener("input", oninput);
    input.addEventListener("keydown", onkeydown);
    input.addEventListener("blur", onblur);
    return {
      destroy() {
        input.removeEventListener("input", oninput);
        input.removeEventListener("keydown", onkeydown);
        input.removeEventListener("blur", onblur);
      },
    };
  }
</script>

<div
  class="row"
  class:selected
  class:focused
  class:playing
  class:missing={sample?.missing}
  role="option"
  aria-selected={selected}
  data-index={index}
  data-focused={focus}
  style:top="{top}px"
  style:height="{height}px"
>
  {#if sample}
    <span class="glyph" aria-hidden="true">
      {#if playing}
        <span class="bars"><i></i><i></i><i></i></span>
      {:else if sample.type === "loop"}
        <span class="loop"></span>
      {:else if sample.type === "oneshot"}
        <span class="shot"></span>
      {/if}
    </span>
    <span class="title">
      {#if renaming}
        <input
          class="rename"
          type="text"
          spellcheck="false"
          autocomplete="off"
          aria-label="Sample name"
          data-no-pick
          use:editName={sample}
        />
      {:else}
        <span class="name truncate" title={sample.missing ? "File is missing from the library folder" : undefined}
          >{sample.name}<span class="ext">.{sample.ext}</span></span
        >
        {#if showPack}
          <span class="pack truncate">{sample.packName}</span>
        {/if}
      {/if}
    </span>
    <canvas class="mini" bind:this={canvas} style:width="{MINI_WIDTH}px" style:height="{miniHeight}px"></canvas>
    <span class="tags">
      {#each tags.shown as tag (tag)}
        <span class="chip truncate">{tag}</span>
      {/each}
      {#if tags.more > 0}
        <span class="more num">+{tags.more}</span>
      {/if}
    </span>
    <span class="num cell bpm" class:user={sample.bpmSrc === "user"} class:matched={matched && sample.bpm > 0}
      >{formatBpm(sample.bpm) || unknownMark(sample)}</span
    >
    <span class="num cell key" class:user={sample.keySrc === "user"} class:matched={matched && sample.key !== ""}
      >{sample.key || unknownMark(sample)}</span
    >
    <span class="num cell len">{formatLength(sample.dur)}</span>
    <button
      class="fav"
      class:on={sample.fav}
      type="button"
      tabindex="-1"
      data-no-pick
      disabled={!libraryUsable()}
      aria-label={sample.fav ? "Remove from favourites" : "Add to favourites"}
      aria-pressed={sample.fav}
      onclick={() => toggleFav(sample)}
    >
      <Heart size={14} strokeWidth={1.5} fill={sample.fav ? "currentColor" : "none"} />
    </button>
  {:else if skeleton}
    <span class="glyph"></span>
    <span class="title"><span class="bone" style:width="{90 + ((index * 53) % 140)}px"></span></span>
    <span class="bone mini" style:width="{MINI_WIDTH}px"></span>
  {/if}
</div>

<style>
  .row {
    position: absolute;
    left: var(--space-3);
    right: var(--space-3);
    display: flex;
    align-items: center;
    gap: var(--space-4);
    padding: 0 var(--space-3);
    border-radius: var(--radius-control);
    user-select: none;
    -webkit-user-select: none;
    -webkit-user-drag: none;
  }

  .row:hover {
    background: var(--bg-2);
  }

  /* After a native drag the webview can keep a stale :hover on the row the
     drag started from; SampleList sets no-hover until the pointer moves. */
  :global(.no-hover) .row:hover {
    background: none;
  }

  .row.selected,
  :global(.no-hover) .row.selected:hover {
    background: var(--bg-3);
  }

  .row.focused {
    box-shadow: inset 0 0 0 1px var(--text-3);
  }

  .glyph {
    display: grid;
    place-items: center;
    flex: none;
    width: 12px;
    height: 12px;
  }

  .loop {
    width: 8px;
    height: 8px;
    border: 1.5px solid var(--text-3);
    border-radius: 50%;
  }

  .shot {
    width: 4px;
    height: 4px;
    border-radius: 50%;
    background: var(--text-3);
  }

  .bars {
    display: flex;
    align-items: flex-end;
    gap: 1px;
    height: 10px;
  }

  .bars i {
    width: 2px;
    height: 100%;
    border-radius: 1px;
    background: var(--accent);
    transform-origin: bottom;
    animation: bounce 0.9s ease-in-out infinite;
  }

  .bars i:nth-child(2) {
    animation-delay: -0.3s;
  }

  .bars i:nth-child(3) {
    animation-delay: -0.6s;
  }

  @keyframes bounce {
    0%,
    100% {
      transform: scaleY(0.35);
    }
    50% {
      transform: scaleY(1);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .bars i {
      animation: none;
    }

    .bars i:nth-child(2) {
      transform: scaleY(0.55);
    }
  }

  .title {
    display: flex;
    align-items: baseline;
    gap: var(--space-3);
    flex: 1 1 0;
    min-width: 0;
  }

  .name {
    flex: 0 1 auto;
    min-width: 48px;
  }

  .selected .name,
  .playing .name {
    font-weight: 500;
  }

  .ext {
    color: var(--text-3);
    font-weight: 400;
  }

  .pack {
    flex: 0 100 auto;
    min-width: 0;
    color: var(--text-3);
    font-size: var(--text-meta);
  }

  .missing .name {
    color: var(--text-3);
    text-decoration: line-through;
  }

  .rename {
    flex: 1;
    min-width: 0;
    height: 22px;
    margin-left: -5px;
    padding: 0 4px;
    border: 1px solid var(--accent);
    border-radius: var(--radius-chip);
    background: var(--bg-0);
    font-weight: 500;
  }

  .mini {
    flex: none;
  }

  .tags {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    flex: none;
    width: 148px;
    min-width: 0;
    overflow: hidden;
  }

  .chip {
    display: inline-block;
    flex: 0 1 auto;
    min-width: 0;
    line-height: 18px;
  }

  .selected .chip {
    background: var(--bg-0);
  }

  .more {
    flex: none;
    color: var(--text-3);
    font-size: var(--text-meta);
  }

  .cell {
    flex: none;
    color: var(--text-2);
    font-size: var(--text-meta);
    text-align: right;
  }

  .cell.user {
    color: var(--text-1);
  }

  .cell.matched {
    color: var(--accent-text);
  }

  .bpm {
    width: 36px;
  }

  .key {
    width: 28px;
    text-align: left;
  }

  .len {
    width: 40px;
  }

  .fav {
    display: grid;
    place-items: center;
    flex: none;
    width: 20px;
    height: 20px;
    border-radius: var(--radius-chip);
    color: var(--text-3);
    opacity: 0;
  }

  .row:hover .fav,
  .fav.on {
    opacity: 1;
  }

  :global(.no-hover) .row:hover .fav:not(.on) {
    opacity: 0;
  }

  .fav.on,
  .fav:hover {
    color: var(--text-1);
  }

  .bone {
    display: block;
    height: 8px;
    border-radius: var(--radius-chip);
    background: var(--bg-2);
  }

  /* Columns leave from the right as the list narrows; before that, the
     tags give the name some of their room. */
  @container list (max-width: 700px) {
    .tags {
      width: 84px;
    }

    .pack {
      display: none;
    }
  }


  @container list (max-width: 600px) {
    .fav,
    .len {
      display: none;
    }
  }

  @container list (max-width: 540px) {
    .key {
      display: none;
    }
  }

  @container list (max-width: 490px) {
    .bpm {
      display: none;
    }
  }

  @container list (max-width: 440px) {
    .tags {
      display: none;
    }
  }

  @container list (max-width: 300px) {
    .mini {
      display: none;
    }
  }
</style>
