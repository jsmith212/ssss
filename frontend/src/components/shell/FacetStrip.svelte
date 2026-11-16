<!--
  Mouse-friendly filters: Type · BPM · Key · Tags · Pack · Length. Every
  control reads its state from the query and writes tokens back into it.
-->
<script lang="ts">
  import {
    formatBpmRange,
    formatKeyValue,
    LENGTH_PRESETS,
    parseBpmRange,
    parseKeyValue,
  } from "../../lib/list/facets";
  import { KEYS, referents, setKind, toggleToken, tokensOf, type FilterKind } from "../../lib/query";
  import { setQuery, sidebar, view } from "../../lib/state.svelte";
  import FacetMenu from "./FacetMenu.svelte";
  import FacetPicker from "./FacetPicker.svelte";

  const NOTES = KEYS.slice(0, 12);

  function valueOf(kind: FilterKind): string {
    return tokensOf(view.query, kind)[0]?.value ?? "";
  }

  /** Sets a single-valued facet; choosing the current value clears it. */
  function choose(kind: FilterKind, value: string): void {
    const same = valueOf(kind).toLowerCase() === value.toLowerCase();
    setQuery(setKind(view.query, kind, same ? null : value));
  }

  const type = $derived(valueOf("type").toLowerCase());
  const length = $derived(valueOf("len").toLowerCase());

  // --- BPM ----------------------------------------------------------------------

  const bpm = $derived(parseBpmRange(valueOf("bpm")));

  function setBpm(end: "min" | "max", raw: string): void {
    const n = raw.trim() === "" ? null : Number(raw);
    const next = { ...bpm, [end]: n !== null && Number.isFinite(n) ? n : null };
    setQuery(setKind(view.query, "bpm", formatBpmRange(next)));
  }

  // --- key ----------------------------------------------------------------------

  let keyOpen = $state(false);
  const key = $derived(parseKeyValue(valueOf("key")));
  const keyLabel = $derived(key.key === "" ? "" : key.compatible ? `≈ ${key.key}` : key.key);

  function setKey(next: string): void {
    const same = key.key.toLowerCase() === next.toLowerCase();
    setQuery(setKind(view.query, "key", same ? null : formatKeyValue(next, key.compatible)));
  }

  function setCompatible(compatible: boolean): void {
    if (key.key === "") return;
    setQuery(setKind(view.query, "key", formatKeyValue(key.key, compatible)));
  }

  // --- tags and pack --------------------------------------------------------------

  let packOpen = $state(false);
  const tags = $derived(tokensOf(view.query, "tag").map((t) => t.value));
  // The pack token holds an id (names repeat), or a name the user typed.
  const packs = $derived(referents(valueOf("pack"), sidebar.data?.packs ?? []));
  const packLabel = $derived(packs.length === 1 ? packs[0].name : valueOf("pack"));

  function choosePack(id: string): void {
    const same = packs.some((p) => p.id === id);
    setQuery(setKind(view.query, "pack", same ? null : id));
  }
  const tagOptions = $derived(
    [...(sidebar.data?.tags ?? [])]
      .sort((a, b) => b.count - a.count)
      .map((t) => ({ value: t.tag, label: t.tag, count: t.count })),
  );
  const packOptions = $derived((sidebar.data?.packs ?? []).map((p) => ({ value: p.id, label: p.name, count: p.count })));
  const tagsLabel = $derived(tags.length === 0 ? "" : tags.length === 1 ? tags[0] : String(tags.length));

  const anySet = $derived(
    (["type", "bpm", "key", "tag", "pack", "len"] as const).some((kind) => tokensOf(view.query, kind).length > 0),
  );

  function clear(): void {
    let query = view.query;
    for (const kind of ["type", "bpm", "key", "tag", "pack", "len"] as const) query = setKind(query, kind, null);
    setQuery(query);
  }
</script>

<div class="facets" role="toolbar" aria-label="Filters">
  <div class="segmented" role="group" aria-label="Type">
    <button type="button" aria-pressed={type === "loop"} onclick={() => choose("type", "loop")}>Loops</button>
    <button type="button" aria-pressed={type === "oneshot"} onclick={() => choose("type", "oneshot")}>One-shots</button>
  </div>

  <div class="range" role="group" aria-label="BPM range">
    <span class="name">BPM</span>
    <input
      class="num"
      type="text"
      inputmode="decimal"
      placeholder="min"
      aria-label="Minimum BPM"
      value={bpm.min ?? ""}
      onchange={(e) => setBpm("min", e.currentTarget.value)}
    />
    <span class="dash">–</span>
    <input
      class="num"
      type="text"
      inputmode="decimal"
      placeholder="max"
      aria-label="Maximum BPM"
      value={bpm.max ?? ""}
      onchange={(e) => setBpm("max", e.currentTarget.value)}
    />
  </div>

  <FacetMenu label="Key" value={keyLabel} bind:open={keyOpen}>
    <div class="keys">
      {#each [false, true] as minor (minor)}
        <div class="keyrow">
          {#each NOTES as note (note)}
            {@const name = minor ? note + "m" : note}
            <button
              type="button"
              class="keycell num"
              aria-pressed={key.key.toLowerCase() === name.toLowerCase()}
              onclick={() => setKey(name)}>{name}</button
            >
          {/each}
        </div>
      {/each}
      <label class="compatible" class:disabled={key.key === ""}>
        <input
          type="checkbox"
          checked={key.compatible}
          disabled={key.key === ""}
          onchange={(e) => setCompatible(e.currentTarget.checked)}
        />
        Include compatible keys
      </label>
    </div>
  </FacetMenu>

  <FacetMenu label="Tags" value={tagsLabel}>
    <FacetPicker
      options={tagOptions}
      selected={tags}
      placeholder="Filter tags"
      onpick={(tag) => setQuery(toggleToken(view.query, { kind: "tag", value: tag }))}
    />
  </FacetMenu>

  <FacetMenu label="Pack" value={packLabel} bind:open={packOpen}>
    <FacetPicker
      options={packOptions}
      selected={packs.map((p) => p.id)}
      placeholder="Filter packs"
      onpick={(id) => {
        choosePack(id);
        packOpen = false;
      }}
    />
  </FacetMenu>

  <div class="segmented" role="group" aria-label="Length">
    {#each LENGTH_PRESETS as preset (preset.value)}
      <button type="button" aria-pressed={length === preset.value} onclick={() => choose("len", preset.value)}
        >{preset.label}</button
      >
    {/each}
  </div>

  {#if anySet}
    <button class="clear" type="button" onclick={clear}>Clear</button>
  {/if}
</div>

<style>
  .facets {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: var(--space-3);
    flex: none;
    padding: var(--space-3) var(--space-5);
    border-bottom: 1px solid var(--border);
    font-size: var(--text-meta);
  }

  .segmented {
    display: inline-flex;
    flex: none;
    padding: 2px;
    border-radius: var(--radius-control);
    background: var(--bg-2);
  }

  .segmented button {
    height: 20px;
    padding: 0 var(--space-3);
    border-radius: var(--radius-chip);
    color: var(--text-2);
  }

  .segmented button:hover {
    color: var(--text-1);
  }

  .segmented button[aria-pressed="true"] {
    background: var(--bg-0);
    color: var(--text-1);
    font-weight: 500;
    box-shadow: 0 0 0 1px var(--border);
  }

  .range {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    flex: none;
    height: 24px;
    padding: 0 var(--space-2) 0 var(--space-3);
    border-radius: var(--radius-control);
    background: var(--bg-2);
    color: var(--text-2);
  }

  .range input {
    width: 38px;
    height: 20px;
    padding: 0 var(--space-2);
    border: 0;
    border-radius: var(--radius-chip);
    background: var(--bg-0);
    color: var(--text-1);
    text-align: center;
  }

  .range input:focus-visible {
    outline-offset: 0;
  }

  .dash {
    color: var(--text-3);
  }

  .keys {
    display: grid;
    gap: var(--space-2);
  }

  .keyrow {
    display: grid;
    grid-template-columns: repeat(12, 34px);
    gap: var(--space-1);
  }

  .keycell {
    height: 26px;
    border-radius: var(--radius-chip);
    background: var(--bg-3);
    color: var(--text-2);
  }

  .keycell:hover {
    color: var(--text-1);
  }

  .keycell[aria-pressed="true"] {
    background: var(--text-1);
    color: var(--bg-0);
    font-weight: 500;
  }

  .compatible {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    margin-top: var(--space-2);
    color: var(--text-2);
  }

  .compatible.disabled {
    color: var(--text-3);
  }

  .compatible input {
    accent-color: var(--accent);
  }

  .clear {
    margin-left: auto;
    color: var(--text-2);
    text-decoration: underline;
    text-decoration-color: var(--text-3);
    text-underline-offset: 3px;
  }

  .clear:hover {
    color: var(--text-1);
  }
</style>
