<!-- Review of the packs in the Splice folder before importing them. -->
<script lang="ts">
  import Check from "@lucide/svelte/icons/check";
  import { tick } from "svelte";
  import { api } from "../../lib/api";
  import { closeOverlay, libraryUsable, settings, updateSettings } from "../../lib/state.svelte";
  import type { SpliceInfo } from "../../lib/types";
  import { plural } from "./format";
  import Surface from "./Surface.svelte";
  import { report } from "./targets.svelte";

  const uid = $props.id();

  let info = $state.raw<SpliceInfo | null>(null);
  let picked = $state<Record<string, boolean>>({});
  let active = $state(0);
  let busy = $state(false);
  let listEl = $state<HTMLElement>();

  const packs = $derived(info?.packs ?? []);
  const chosen = $derived(packs.filter((p) => picked[p.name]));
  const samples = $derived(chosen.reduce((n, p) => n + p.count, 0));

  const close = () => closeOverlay("splice");

  async function detect(): Promise<void> {
    try {
      const found = await api.detectSplice();
      info = found;
      // New packs are ticked; ones already in the library can be ticked to
      // pick up files added since.
      picked = Object.fromEntries(found.packs.map((p) => [p.name, !p.imported]));
      active = 0;
      await tick();
      listEl?.focus();
    } catch (err) {
      report(err);
      info = { found: false, folder: "", hasDb: false, packs: [] };
    }
  }

  void detect();

  async function chooseFolder(): Promise<void> {
    try {
      const folder = await api.chooseFolder("Choose your Splice folder");
      if (!folder) return;
      await updateSettings({ spliceFolder: folder });
      await detect();
    } catch (err) {
      report(err);
    }
  }

  function setAll(on: boolean): void {
    picked = Object.fromEntries(packs.map((p) => [p.name, on]));
  }

  function toggle(index: number): void {
    const pack = packs[index];
    if (pack) picked[pack.name] = !picked[pack.name];
  }

  function move(to: number): void {
    if (packs.length === 0) return;
    active = Math.max(0, Math.min(packs.length - 1, to));
    void tick().then(() => listEl?.querySelector('[data-active="true"]')?.scrollIntoView({ block: "nearest" }));
  }

  async function start(): Promise<void> {
    if (!info || chosen.length === 0 || busy || !libraryUsable()) return;
    busy = true;
    try {
      await api.importSplice(
        info.folder,
        chosen.map((p) => p.name),
      );
      close();
    } catch (err) {
      report(err);
    } finally {
      busy = false;
    }
  }

  function onkeydown(event: KeyboardEvent): void {
    switch (event.key) {
      case "ArrowDown":
        move(active + 1);
        break;
      case "ArrowUp":
        move(active - 1);
        break;
      case "Home":
        move(0);
        break;
      case "End":
        move(packs.length - 1);
        break;
      case " ":
        toggle(active);
        break;
      case "Enter":
        void start();
        break;
      case "a":
        if (!(event.metaKey || event.ctrlKey)) return;
        setAll(true);
        // Keep Cmd+A from reaching the list's select-all.
        event.stopPropagation();
        break;
      default:
        return;
    }
    event.preventDefault();
  }
</script>

<Surface label="Import from Splice" placement="center" onclose={close} width={520}>
  <header class="head">
    <h2>Import from Splice</h2>
    {#if info?.found}
      <p class="folder">
        <span class="truncate selectable" title={info.folder}>{info.folder}</span>
        <button class="link" onclick={chooseFolder}>Change…</button>
      </p>
    {/if}
  </header>

  {#if info === null}
    <p class="note">Looking for your Splice folder…</p>
  {:else if !info.found}
    <div class="missing">
      <p>
        No Splice folder was found{settings().spliceFolder ? ` at ${settings().spliceFolder}` : ""}. Point ssss at the
        folder Splice downloads into.
      </p>
      <button class="btn btn-primary" data-autofocus onclick={chooseFolder}>Choose folder…</button>
    </div>
  {:else if packs.length === 0}
    <p class="note">This Splice folder has no packs yet.</p>
  {:else}
    {#if !info.hasDb}
      <p class="note">
        Splice’s own database wasn’t found, so tags, BPM and key will come from file names and analysis instead.
      </p>
    {/if}
    <div
      class="list"
      role="listbox"
      tabindex="0"
      aria-label="Packs to import"
      aria-multiselectable="true"
      aria-activedescendant="{uid}-{active}"
      data-autofocus
      bind:this={listEl}
      {onkeydown}
    >
      {#each packs as pack, index (pack.name)}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <div
          class="row"
          class:active={index === active}
          class:imported={pack.imported}
          id="{uid}-{index}"
          role="option"
          tabindex="-1"
          aria-selected={picked[pack.name] ?? false}
          data-active={index === active}
          onclick={() => {
            active = index;
            toggle(index);
          }}
        >
          <span class="box" class:on={picked[pack.name]} aria-hidden="true">
            {#if picked[pack.name]}<Check size={12} strokeWidth={2.5} />{/if}
          </span>
          <span class="name truncate">{pack.name}</span>
          {#if pack.imported}<span class="tag">in library</span>{:else if pack.genre}<span class="tag">{pack.genre}</span>{/if}
          <span class="count num">{pack.count.toLocaleString()}</span>
        </div>
      {/each}
    </div>
    <footer class="foot">
      <button class="btn btn-ghost" onclick={() => setAll(true)}>All</button>
      <button class="btn btn-ghost" onclick={() => setAll(false)}>None</button>
      <span class="total num" aria-live="polite">
        {chosen.length === 0 ? "Nothing selected" : `${plural(chosen.length, "pack")} · ${plural(samples, "sample")}`}
      </span>
      <button class="btn btn-primary" disabled={chosen.length === 0 || busy || !libraryUsable()} onclick={start}>
        Import
      </button>
    </footer>
  {/if}
</Surface>

<style>
  .head {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-5) var(--space-5) var(--space-4);
  }

  h2 {
    font-size: var(--text-omnibar);
  }

  .folder {
    display: flex;
    align-items: baseline;
    gap: var(--space-3);
    color: var(--text-2);
    font-size: var(--text-meta);
  }

  .link {
    flex: none;
    color: var(--text-2);
    text-decoration: underline;
    text-decoration-color: var(--text-3);
    text-underline-offset: 2px;
  }

  .link:hover {
    color: var(--text-1);
  }

  .note,
  .missing {
    padding: 0 var(--space-5) var(--space-4);
    color: var(--text-2);
  }

  .missing {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--space-4);
    padding-bottom: var(--space-5);
  }

  .list {
    min-height: 0;
    max-height: min(360px, 50vh);
    overflow-y: auto;
    padding: var(--space-2);
    border-block: 1px solid var(--border);
  }

  .list:focus-visible {
    outline: none;
  }

  .row {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    height: 32px;
    padding: 0 var(--space-4);
    border-radius: var(--radius-control);
  }

  .row:hover,
  .list:focus-visible .row.active {
    background: var(--bg-3);
  }

  .row.imported:not([aria-selected="true"]) .name {
    color: var(--text-3);
  }

  .box {
    display: grid;
    flex: none;
    place-items: center;
    width: 16px;
    height: 16px;
    border: 1px solid var(--text-3);
    border-radius: var(--radius-chip);
    color: var(--bg-2);
  }

  .box.on {
    border-color: var(--text-1);
    background: var(--text-1);
  }

  .name {
    min-width: 0;
  }

  .tag {
    flex: 1;
    color: var(--text-3);
    font-size: var(--text-meta);
    white-space: nowrap;
  }

  .count {
    flex: none;
    margin-left: auto;
    color: var(--text-2);
    font-size: var(--text-meta);
  }

  .foot {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-4) var(--space-5);
  }

  .total {
    flex: 1;
    padding-right: var(--space-3);
    color: var(--text-2);
    font-size: var(--text-meta);
    text-align: right;
  }
</style>
