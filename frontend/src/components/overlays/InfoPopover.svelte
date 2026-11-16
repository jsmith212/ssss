<!-- Cmd+I: the details the list leaves out, and the editable ones. -->
<script lang="ts">
  import Plus from "@lucide/svelte/icons/plus";
  import X from "@lucide/svelte/icons/x";
  import { api } from "../../lib/api";
  import { formatToken } from "../../lib/query";
  import {
    closeOverlay,
    libraryReadable,
    libraryUsable,
    overlay,
    selectedIds,
    setQuery,
    sidebar,
  } from "../../lib/state.svelte";
  import type { MetaPatch, SampleType } from "../../lib/types";
  import { formatBpm, formatSummary, normaliseKey, parseBpm, sourceLabel } from "./format";
  import { copyPaths, openCollections, openTags } from "./menus";
  import Surface from "./Surface.svelte";
  import { attempt, primaryTargetId, useDetails } from "./targets.svelte";

  const opened = overlay("info");
  const id = (opened?.props?.id as string | undefined) ?? primaryTargetId();
  const others = Math.max(0, selectedIds().length - 1);
  const details = useDetails(() => (id ? [id] : []));
  const sample = $derived(details.list[0]);
  const editable = $derived(libraryUsable() && !sample?.missing);

  // Field text is local while typing and resynced whenever the sample changes.
  let bpm = $state("");
  let key = $state("");
  let note = $state("");
  let invalid = $state<{ bpm?: boolean; key?: boolean }>({});

  $effect(() => {
    if (!sample) return;
    // Unrelated backend changes refetch the sample; they must not overwrite
    // a field that is being typed in.
    const editing = document.activeElement?.id;
    if (editing !== "info-bpm") bpm = formatBpm(sample.bpm);
    if (editing !== "info-key") key = sample.key;
    if (editing !== "info-note") note = sample.note;
  });

  const collections = $derived(
    (sample?.collections ?? []).map((cid) => ({
      id: cid,
      name: sidebar.data?.collections.find((c) => c.id === cid)?.name ?? "Collection",
    })),
  );

  const close = () => closeOverlay("info");

  function patch(change: MetaPatch): void {
    if (sample) void attempt(() => api.setMeta([sample.id], change));
  }

  function commitBpm(): void {
    if (!sample) return;
    const value = parseBpm(bpm);
    invalid.bpm = value === null;
    if (value !== null && value !== sample.bpm) patch({ bpm: value });
  }

  function commitKey(): void {
    if (!sample) return;
    const value = normaliseKey(key);
    invalid.key = value === null;
    if (value === null) return;
    key = value;
    if (value !== sample.key) patch({ key: value });
  }

  function commitNote(): void {
    if (sample && note !== sample.note) patch({ note });
  }

  /** Enter commits a single-line field and keeps the popover open. */
  function onEnter(commit: () => void): (event: KeyboardEvent) => void {
    return (event) => {
      if (event.key === "Enter") commit();
    };
  }

  function go(query: string): void {
    close();
    setQuery(query);
  }
</script>

<Surface label="Info" anchor={opened?.anchor} onclose={close} width={380}>
  {#if !sample}
    <p class="empty">{details.loaded ? "Select a sample to see its details." : " "}</p>
  {:else}
    <div class="body">
      <header>
        <h2 class="selectable">{sample.name}<span class="ext">.{sample.ext}</span></h2>
        <p class="meta num">{formatSummary(sample)}</p>
        {#if sample.missing}<p class="meta missing">The file is missing from the library folder.</p>{/if}
        {#if others > 0}<p class="meta">Showing the focused sample; {others.toLocaleString()} more selected.</p>{/if}
      </header>

      <div class="path">
        <p class="meta selectable" title={sample.absPath}>{sample.absPath}</p>
        <div class="path-actions">
          <button class="btn" disabled={!libraryReadable()} onclick={() => void attempt(() => api.reveal(sample.id))}>
            Reveal
          </button>
          <button class="btn" onclick={() => void copyPaths([sample.id])}>Copy path</button>
        </div>
      </div>

      <dl class="fields">
        <dt>Pack</dt>
        <dd>
          <button class="link truncate" onclick={() => go(formatToken({ kind: "pack", value: sample.packId }))}>{sample.packName}</button>
        </dd>

        <dt><label for="info-bpm">BPM</label></dt>
        <dd>
          <input
            id="info-bpm"
            class="ov-field short num"
            type="text"
            inputmode="decimal"
            autocomplete="off"
            placeholder="–"
            disabled={!editable}
            aria-invalid={invalid.bpm || undefined}
            aria-describedby="info-bpm-src"
            bind:value={bpm}
            onblur={commitBpm}
            onkeydown={onEnter(commitBpm)}
          />
          <span class="source" id="info-bpm-src">{invalid.bpm ? "20–999" : sourceLabel(sample.bpmSrc)}</span>
        </dd>

        <dt><label for="info-key">Key</label></dt>
        <dd>
          <input
            id="info-key"
            class="ov-field short"
            type="text"
            autocomplete="off"
            spellcheck="false"
            placeholder="–"
            disabled={!editable}
            aria-invalid={invalid.key || undefined}
            aria-describedby="info-key-src"
            bind:value={key}
            onblur={commitKey}
            onkeydown={onEnter(commitKey)}
          />
          <span class="source" id="info-key-src">{invalid.key ? "e.g. Am, F#" : sourceLabel(sample.keySrc)}</span>
        </dd>

        <dt><label for="info-type">Type</label></dt>
        <dd>
          <select
            id="info-type"
            class="ov-field"
            disabled={!editable}
            value={sample.type}
            onchange={(e) => patch({ type: e.currentTarget.value as SampleType })}
          >
            <option value="loop">Loop</option>
            <option value="oneshot">One-shot</option>
            <option value="">Unknown</option>
          </select>
        </dd>

        <dt>Tags</dt>
        <dd class="chips">
          {#each sample.tags as tag (tag)}
            <span class="chip">
              {tag}
              <button
                class="chip-x"
                aria-label="Remove tag {tag}"
                disabled={!editable}
                onclick={() => void attempt(() => api.removeTags([sample.id], [tag]))}
              >
                <X size={11} strokeWidth={2} aria-hidden="true" />
              </button>
            </span>
          {/each}
          {#each sample.suggestedTags as tag (tag)}
            <button
              class="chip ghost"
              title="Suggested from the file name; click to add"
              disabled={!editable}
              onclick={() => void attempt(() => api.addTags([sample.id], [tag]))}
            >
              <Plus size={11} strokeWidth={2} aria-hidden="true" />{tag}
            </button>
          {/each}
          <button class="chip add" disabled={!editable} onclick={() => openTags([sample.id], opened?.anchor)}>Tag…</button>
        </dd>

        <dt>Collections</dt>
        <dd class="chips">
          {#each collections as c (c.id)}
            <button class="chip" onclick={() => go(formatToken({ kind: "in", value: c.id }))}>{c.name}</button>
          {/each}
          <button class="chip add" disabled={!editable} onclick={() => openCollections([sample.id], opened?.anchor)}>
            Add…
          </button>
        </dd>

        <dt><label for="info-note">Note</label></dt>
        <dd>
          <textarea
            id="info-note"
            class="ov-field note"
            rows="2"
            placeholder="Add a note"
            disabled={!editable}
            bind:value={note}
            onblur={commitNote}
          ></textarea>
        </dd>
      </dl>
    </div>
  {/if}
</Surface>

<style>
  .body {
    display: flex;
    flex-direction: column;
    gap: var(--space-4);
    padding: var(--space-5);
    overflow-y: auto;
  }

  .empty {
    padding: var(--space-5);
    color: var(--text-3);
  }

  header {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }

  h2 {
    font-size: var(--text-omnibar);
    overflow-wrap: anywhere;
  }

  .ext {
    color: var(--text-3);
    font-weight: 400;
  }

  .meta {
    color: var(--text-2);
    font-size: var(--text-meta);
    overflow-wrap: anywhere;
  }

  .missing {
    color: var(--warning);
  }

  .path {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding: var(--space-3) var(--space-4);
    border-radius: var(--radius-control);
    background: var(--bg-0);
  }

  .path-actions {
    display: flex;
    gap: var(--space-3);
  }

  .path-actions .btn {
    height: 24px;
    padding: 0 var(--space-3);
    font-size: var(--text-meta);
  }

  .fields {
    display: grid;
    grid-template-columns: 76px minmax(0, 1fr);
    align-items: center;
    gap: var(--space-3) var(--space-4);
    margin: 0;
  }

  dt {
    align-self: start;
    color: var(--text-3);
    font-size: var(--text-meta);
    line-height: 28px;
  }

  dd {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    min-width: 0;
    min-height: 28px;
    margin: 0;
  }

  .short {
    width: 72px;
  }

  .source {
    color: var(--text-3);
    font-size: var(--text-meta);
  }

  .link {
    max-width: 100%;
    text-align: left;
  }

  .link:hover {
    text-decoration: underline;
    text-decoration-color: var(--text-3);
    text-underline-offset: 2px;
  }

  .chips {
    flex-wrap: wrap;
    gap: var(--space-2);
  }

  .chip {
    gap: 2px;
    height: 20px;
    background: var(--bg-3);
  }

  .chip-x {
    display: grid;
    place-items: center;
    width: 14px;
    height: 14px;
    margin-right: -3px;
    border-radius: 3px;
    color: var(--text-3);
  }

  .chip-x:hover {
    color: var(--text-1);
  }

  .chip.ghost,
  .chip.add {
    border: 1px dashed var(--text-3);
    background: none;
  }

  .chip.add {
    border-style: solid;
    border-color: var(--border);
  }

  button.chip:hover:not(:disabled) {
    color: var(--text-1);
  }

  .note {
    width: 100%;
    height: auto;
    min-height: 44px;
    padding: var(--space-2) var(--space-3);
    line-height: 1.4;
    resize: vertical;
  }
</style>
