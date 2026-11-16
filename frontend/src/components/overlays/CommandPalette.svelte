<!-- Cmd+K: every command with its shortcut, plus jumps to tags, packs, collections and views. -->
<script lang="ts">
  import Search from "@lucide/svelte/icons/search";
  import { tick } from "svelte";
  import {
    formatKeys,
    isComposing,
    list as listCommands,
    run as runCommand,
    type Command,
  } from "../../lib/commands";
  import { formatToken } from "../../lib/query";
  import { closeOverlay, overlays, setQuery, sidebar } from "../../lib/state.svelte";
  import { rank, segments } from "./fuzzy";
  import Surface from "./Surface.svelte";
  import { report } from "./targets.svelte";

  interface Entry {
    key: string;
    title: string;
    /** Dimmed context after the title: the command's section or the kind of destination. */
    kind: string;
    keys?: string;
    command: boolean;
    run: () => void;
  }

  const LIMIT = 50;
  const uid = $props.id();

  let filter = $state("");
  let active = $state(0);
  let listEl = $state<HTMLElement>();

  /**
   * The commands as they will be when one is chosen, which is after the
   * palette has closed: many are only available while no overlay is open.
   */
  function availableCommands(): Command[] {
    const stack = overlays.stack;
    overlays.stack = stack.filter((o) => o.id !== "palette");
    try {
      return listCommands();
    } finally {
      overlays.stack = stack;
    }
  }

  // Read once: the palette is short-lived and commands do not change under it.
  const commands: Entry[] = availableCommands().map((c) => ({
      key: `c:${c.id}`,
      title: c.title,
      kind: c.section ?? "",
      keys: Array.isArray(c.keys) ? c.keys[0] : c.keys,
      command: true,
      run: () => void runCommand(c.id),
    }));

  const destinations = $derived.by((): Entry[] => {
    const data = sidebar.data;
    const go = (key: string, title: string, kind: string, query: string): Entry => ({
      key,
      title,
      kind,
      command: false,
      run: () => setQuery(query),
    });
    return [
      go("g:all", "Library", "Go to", ""),
      go("g:fav", "Favourites", "Go to", "is:fav"),
      go("g:untagged", "Untagged", "Go to", "is:untagged"),
      go("g:recent", "Recent import", "Go to", "is:recent"),
      ...(data?.views ?? []).map((v) => go(`v:${v.id}`, v.name, "View", v.query)),
      ...(data?.collections ?? []).map((c) => go(`col:${c.id}`, c.name, "Collection", formatToken({ kind: "in", value: c.id }))),
      ...(data?.tags ?? []).map((t) => go(`t:${t.tag}`, t.tag, "Tag", formatToken({ kind: "tag", value: t.tag }))),
      ...(data?.packs ?? []).map((p) => go(`p:${p.id}`, p.name, "Pack", formatToken({ kind: "pack", value: p.id }))),
    ];
  });

  const rows = $derived.by(() => {
    const query = filter.trim();
    // Nothing typed: the commands, as a menu. Typing searches everything,
    // with a command winning a tie against a destination.
    const pool = query ? [...commands, ...destinations] : commands;
    return rank(query, pool, (e) => e.title, { limit: LIMIT, bonus: (e) => (e.command ? 6 : 0) }).map(
      ({ item, match }) => ({ entry: item, parts: segments(item.title, match.positions) }),
    );
  });

  const activeIndex = $derived(Math.min(active, rows.length - 1));

  const close = () => closeOverlay("palette");

  function move(to: number): void {
    if (rows.length === 0) return;
    active = (to + rows.length) % rows.length;
    void tick().then(() => listEl?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" }));
  }

  async function choose(entry: Entry | undefined): Promise<void> {
    if (!entry) return;
    close();
    // The palette returns focus first, so a command that opens a popover
    // anchors to, and later returns to, the list rather than the palette.
    await tick();
    try {
      entry.run();
    } catch (err) {
      report(err);
    }
  }

  function onkeydown(event: KeyboardEvent): void {
    if (isComposing(event)) return;
    switch (event.key) {
      case "ArrowDown":
        move(activeIndex + 1);
        break;
      case "ArrowUp":
        move(activeIndex - 1);
        break;
      case "PageDown":
        move(Math.min(rows.length - 1, activeIndex + 8));
        break;
      case "PageUp":
        move(Math.max(0, activeIndex - 8));
        break;
      case "Enter":
        void choose(rows[activeIndex]?.entry);
        break;
      default:
        return;
    }
    event.preventDefault();
  }
</script>

<Surface label="Command palette" placement="top" onclose={close} width={560}>
  <div class="head">
    <Search class="icon" aria-hidden="true" />
    <input
      class="filter"
      type="text"
      data-autofocus
      role="combobox"
      aria-label="Search commands, tags, packs and collections"
      aria-expanded="true"
      aria-controls="{uid}-list"
      aria-activedescendant={rows.length ? `${uid}-${activeIndex}` : undefined}
      aria-autocomplete="list"
      autocomplete="off"
      spellcheck="false"
      placeholder="Run a command or jump to a tag, pack or collection…"
      bind:value={filter}
      oninput={() => (active = 0)}
      {onkeydown}
    />
  </div>
  <div class="list" id="{uid}-list" role="listbox" aria-label="Results" bind:this={listEl}>
    {#each rows as { entry, parts }, index (entry.key)}
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <div
        class="row"
        id="{uid}-{index}"
        role="option"
        tabindex="-1"
        aria-selected={index === activeIndex}
        onpointermove={() => (active = index)}
        onclick={() => void choose(entry)}
      >
        <span class="title truncate">
          {#each parts as part, i (i)}{#if part.hit}<mark>{part.text}</mark>{:else}{part.text}{/if}{/each}
        </span>
        {#if entry.kind}<span class="kind">{entry.kind}</span>{/if}
        {#if entry.keys}<kbd class="ov-kbd">{formatKeys(entry.keys)}</kbd>{/if}
      </div>
    {:else}
      <p class="empty">Nothing matches “{filter.trim()}”</p>
    {/each}
  </div>
</Surface>

<style>
  .head {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    padding: 0 var(--space-5);
    border-bottom: 1px solid var(--border);
  }

  .filter {
    flex: 1;
    min-width: 0;
    height: 48px;
    border: 0;
    background: none;
    font-size: var(--text-omnibar);
  }

  .filter:focus-visible {
    outline: none;
  }

  .list {
    min-height: 0;
    max-height: min(396px, 60vh);
    overflow-y: auto;
    padding: var(--space-2);
  }

  .row {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    height: 32px;
    padding: 0 var(--space-4);
    border-radius: var(--radius-control);
  }

  .row[aria-selected="true"] {
    background: var(--bg-3);
  }

  .title {
    min-width: 0;
  }

  mark {
    background: none;
    color: inherit;
    font-weight: 600;
  }

  .kind {
    flex: 1;
    color: var(--text-3);
    font-size: var(--text-meta);
    white-space: nowrap;
  }

  .empty {
    padding: var(--space-5) var(--space-4);
    color: var(--text-3);
  }
</style>
