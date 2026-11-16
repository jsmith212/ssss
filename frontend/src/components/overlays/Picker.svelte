<!--
  Filter-and-toggle list behind the tag and collection popovers: type to
  filter, Enter toggles the highlighted row on every target and keeps the
  popover open, an unmatched name offers to create it.
-->
<script lang="ts" module>
  import type { CheckState } from "./targets.svelte";

  export interface PickerItem {
    id: string;
    label: string;
    /** Right-aligned detail, e.g. a count. */
    hint?: string;
    state: CheckState;
    color?: string;
  }
</script>

<script lang="ts">
  import Check from "@lucide/svelte/icons/check";
  import Minus from "@lucide/svelte/icons/minus";
  import Plus from "@lucide/svelte/icons/plus";
  import { tick } from "svelte";
  import type { Point } from "./focus";
  import { isComposing } from "../../lib/commands";
  import { rank, segments, splitSegments, type Segment } from "./fuzzy";
  import Surface from "./Surface.svelte";

  interface Props {
    label: string;
    placeholder: string;
    /** What the toggles apply to, e.g. "3 samples". */
    subject: string;
    anchor?: Point | null;
    /** In display order for an empty filter. */
    items: PickerItem[];
    /**
     * Labels are slash-separated paths (tags). A long one then gives way in
     * its parents, so the last segment, which tells siblings apart, stays.
     */
    paths?: boolean;
    /** Suggestions offered as one-click chips above the list. */
    ghosts?: string[];
    /** The name a typed filter would create, or "" if it cannot be created. */
    creatable: (text: string) => string;
    emptyText: string;
    ontoggle: (item: PickerItem) => void | Promise<void>;
    oncreate: (name: string) => void | Promise<void>;
    onghost?: (ghost: string) => void | Promise<void>;
    onclose: () => void;
  }

  let {
    label,
    placeholder,
    subject,
    anchor = null,
    items,
    paths = false,
    ghosts = [],
    creatable,
    emptyText,
    ontoggle,
    oncreate,
    onghost,
    onclose,
  }: Props = $props();

  // Enough to scan; typing narrows the rest into view.
  const VISIBLE = 80;
  const uid = $props.id();

  type Row =
    | { kind: "item"; key: string; item: PickerItem; parents: Segment[]; leaf: Segment[] }
    | { kind: "create"; key: string; name: string; parents: string; leaf: string };

  /** Where a label divides into parents and leaf; the slash goes with the leaf. */
  function leafStart(label: string): number {
    return paths ? Math.max(0, label.lastIndexOf("/")) : 0;
  }

  let filter = $state("");
  let active = $state(0);
  let listEl = $state<HTMLElement>();

  const rows = $derived.by((): Row[] => {
    const query = filter.trim();
    const matched = rank(query, items, (i) => i.label, { limit: VISIBLE });
    const out: Row[] = matched.map(({ item, match }) => {
      const [parents, leaf] = splitSegments(segments(item.label, match.positions), leafStart(item.label));
      return { kind: "item", key: `i:${item.id}`, item, parents, leaf };
    });
    const name = query ? creatable(query) : "";
    if (name && !items.some((i) => i.label.toLowerCase() === name.toLowerCase())) {
      const cut = leafStart(name);
      out.push({ kind: "create", key: "create", name, parents: name.slice(0, cut), leaf: name.slice(cut) });
    }
    return out;
  });

  const activeRow = $derived(rows[Math.min(active, rows.length - 1)]);

  function move(delta: number): void {
    if (rows.length === 0) return;
    active = (Math.min(active, rows.length - 1) + delta + rows.length) % rows.length;
    void tick().then(() => listEl?.querySelector('[data-active="true"]')?.scrollIntoView({ block: "nearest" }));
  }

  async function activate(row: Row | undefined): Promise<void> {
    if (!row) return;
    if (row.kind === "create") await oncreate(row.name);
    else await ontoggle(row.item);
    // Ready for the next one: the popover stays open.
    filter = "";
    active = 0;
  }

  function onkeydown(event: KeyboardEvent): void {
    if (isComposing(event)) return;
    switch (event.key) {
      case "ArrowDown":
        move(1);
        break;
      case "ArrowUp":
        move(-1);
        break;
      case "Enter":
        void activate(activeRow);
        break;
      default:
        return;
    }
    event.preventDefault();
  }

  function ariaChecked(state: CheckState): "true" | "false" | "mixed" {
    return state === "on" ? "true" : state === "mixed" ? "mixed" : "false";
  }
</script>

{#snippet marked(parts: Segment[])}{#each parts as part, i (i)}{#if part.hit}<mark>{part.text}</mark>{:else}{part.text}{/if}{/each}{/snippet}

<Surface {label} {anchor} {onclose} width={300}>
  <div class="head">
    <input
      class="filter"
      type="text"
      data-autofocus
      role="combobox"
      aria-label={label}
      aria-expanded="true"
      aria-controls="{uid}-list"
      aria-activedescendant={activeRow ? `${uid}-${Math.min(active, rows.length - 1)}` : undefined}
      aria-autocomplete="list"
      autocomplete="off"
      spellcheck="false"
      {placeholder}
      bind:value={filter}
      oninput={() => (active = 0)}
      {onkeydown}
    />
    <span class="subject num">{subject}</span>
  </div>

  {#if ghosts.length > 0 && filter.trim() === ""}
    <div class="ghosts" role="group" aria-label="Suggested">
      <span class="section-label">Suggested</span>
      {#each ghosts as ghost (ghost)}
        <button class="chip ghost" onclick={() => onghost?.(ghost)} title="Add {ghost}">
          <Plus size={12} strokeWidth={1.5} aria-hidden="true" />{ghost}
        </button>
      {/each}
    </div>
  {/if}

  <div class="list" id="{uid}-list" role="listbox" aria-label={label} aria-multiselectable="true" bind:this={listEl}>
    {#each rows as row, index (row.key)}
      {@const isActive = row === activeRow}
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <div
        class="row"
        class:active={isActive}
        id="{uid}-{index}"
        role="option"
        tabindex="-1"
        aria-selected={isActive}
        aria-checked={row.kind === "item" ? ariaChecked(row.item.state) : undefined}
        title={row.kind === "item" ? row.item.label : row.name}
        data-active={isActive}
        onpointermove={() => (active = index)}
        onclick={() => void activate(row)}
      >
        {#if row.kind === "item"}
          <span class="box {row.item.state}" style:--tint={row.item.color || undefined} aria-hidden="true">
            {#if row.item.state === "on"}<Check size={12} strokeWidth={2.5} />{/if}
            {#if row.item.state === "mixed"}<Minus size={12} strokeWidth={2.5} />{/if}
          </span>
          <span class="name">
            {#if row.parents.length > 0}<span class="parents truncate">{@render marked(row.parents)}</span>{/if}
            <span class="leaf truncate">{@render marked(row.leaf)}</span>
          </span>
          {#if row.item.hint}<span class="hint num">{row.item.hint}</span>{/if}
        {:else}
          <span class="box create" aria-hidden="true"><Plus size={12} strokeWidth={2} /></span>
          <span class="name">
            <span class="verb">Create</span>
            {#if row.parents !== ""}<strong class="parents truncate">{row.parents}</strong>{/if}
            <strong class="leaf truncate">{row.leaf}</strong>
          </span>
        {/if}
      </div>
    {:else}
      <p class="empty">{emptyText}</p>
    {/each}
  </div>

  <div class="foot" aria-hidden="true">
    <span><kbd>↵</kbd> toggle</span>
    <span><kbd>↑↓</kbd> move</span>
    <span><kbd>Esc</kbd> done</span>
  </div>
</Surface>

<style>
  .head {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    padding: var(--space-2) var(--space-4) var(--space-2) var(--space-2);
    border-bottom: 1px solid var(--border);
  }

  .filter {
    flex: 1;
    min-width: 0;
    height: 32px;
    padding: 0 var(--space-3);
    border: 0;
    background: none;
  }

  .filter:focus-visible {
    outline: none;
  }

  .subject {
    flex: none;
    color: var(--text-3);
    font-size: var(--text-meta);
  }

  .ghosts {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-3) var(--space-4);
    border-bottom: 1px solid var(--border);
  }

  .ghosts .section-label {
    margin-right: var(--space-2);
  }

  .ghost {
    gap: 2px;
    height: 20px;
    border: 1px dashed var(--text-3);
    background: none;
    transition:
      color var(--dur-fast) var(--ease),
      border-color var(--dur-fast) var(--ease);
  }

  .ghost:hover,
  .ghost:focus-visible {
    border-color: var(--text-2);
    color: var(--text-1);
  }

  .list {
    min-height: 0;
    max-height: 296px;
    overflow-y: auto;
    padding: var(--space-2);
  }

  .row {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    height: 28px;
    padding: 0 var(--space-3);
    border-radius: var(--radius-control);
  }

  .row.active {
    background: var(--bg-3);
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
    border-color: var(--tint, var(--text-1));
    background: var(--tint, var(--text-1));
  }

  /* Some of the selection: outlined, so a column of them stays quiet. */
  .box.mixed {
    border-color: var(--tint, var(--text-2));
    color: var(--tint, var(--text-1));
  }

  .box.create {
    border-style: dashed;
    color: var(--text-2);
  }

  .name {
    display: flex;
    flex: 1;
    min-width: 0;
  }

  /* The parents give way first: their share of any shrinking is so much
     larger that the leaf loses nothing until they are gone. */
  .parents {
    flex: 0 1000000 auto;
    min-width: 0;
  }

  .leaf {
    flex: 0 1 auto;
    min-width: 0;
  }

  .verb {
    flex: none;
    margin-right: 0.25em;
  }

  mark {
    background: none;
    color: inherit;
    font-weight: 600;
  }

  strong {
    font-weight: 500;
  }

  .hint {
    flex: none;
    color: var(--text-3);
    font-size: var(--text-meta);
  }

  .empty {
    padding: var(--space-4) var(--space-3);
    color: var(--text-3);
  }

  .foot {
    display: flex;
    gap: var(--space-4);
    padding: var(--space-3) var(--space-4);
    border-top: 1px solid var(--border);
    color: var(--text-3);
    font-size: var(--text-label);
  }

  kbd {
    margin-right: 2px;
    color: var(--text-2);
    font: inherit;
  }
</style>
