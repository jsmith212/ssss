<!-- The shared context menu; contents come from menus.ts through the overlay props. -->
<script lang="ts">
  import { tick } from "svelte";
  import { formatKeys } from "../../lib/commands";
  import { closeOverlay, overlay } from "../../lib/state.svelte";
  import Check from "@lucide/svelte/icons/check";
  import type { MenuEntry, MenuItem, MenuSwatches } from "./menus";
  import Surface from "./Surface.svelte";
  import { report } from "./targets.svelte";

  const opened = $derived(overlay("context"));
  const entries = $derived((opened?.props?.items as MenuEntry[] | undefined) ?? []);
  const label = $derived((opened?.props?.label as string | undefined) ?? "Actions");

  let menu = $state<HTMLElement>();

  const close = () => closeOverlay("context");

  function buttons(): HTMLElement[] {
    return [...(menu?.querySelectorAll<HTMLElement>('[role^="menuitem"]:not([aria-disabled="true"])') ?? [])];
  }

  function onkeydown(event: KeyboardEvent): void {
    const items = buttons();
    const at = items.indexOf(document.activeElement as HTMLElement);
    let next: HTMLElement | undefined;
    switch (event.key) {
      case "ArrowDown":
        next = items[(at + 1) % items.length];
        break;
      case "ArrowUp":
        next = items[(at - 1 + items.length) % items.length];
        break;
      case "Home":
        next = items[0];
        break;
      case "End":
        next = items.at(-1);
        break;
      case "Tab":
        // A menu is left with Escape or a choice, not walked with Tab.
        event.preventDefault();
        return;
      default: {
        // Type-ahead: jump to the next item starting with the typed letter.
        if (event.key.length !== 1 || event.metaKey || event.ctrlKey) return;
        const letter = event.key.toLowerCase();
        const order = [...items.slice(at + 1), ...items.slice(0, at + 1)];
        next = order.find((el) => el.textContent?.trim().toLowerCase().startsWith(letter));
        if (!next) return;
      }
    }
    event.preventDefault();
    next?.focus();
  }

  function pick(row: MenuSwatches, color: string): void {
    void choose({ disabled: row.disabled, run: () => row.pick(color) });
  }

  async function choose(item: Pick<MenuItem, "disabled" | "run">): Promise<void> {
    if (item.disabled) return;
    close();
    // Let the menu give focus back before the action opens something else.
    await tick();
    try {
      await item.run();
    } catch (err) {
      report(err);
    }
  }
</script>

<Surface {label} anchor={opened?.anchor} onclose={close} role="none">
  <!-- svelte-ignore a11y_interactive_supports_focus -->
  <div class="menu" role="menu" aria-label={label} bind:this={menu} {onkeydown}>
    {#each entries as entry, index (index)}
      {#if entry === "separator"}
        <div class="separator" role="separator"></div>
      {:else if "swatches" in entry}
        <div class="swatches" role="group" aria-label="Colour">
          {#each entry.swatches as swatch (swatch.color)}
            <button
              class="swatch"
              class:none={swatch.color === ""}
              role="menuitemradio"
              aria-label={swatch.label}
              aria-checked={swatch.color === entry.current}
              aria-disabled={entry.disabled ? "true" : undefined}
              tabindex={entry.disabled ? -1 : 0}
              title={swatch.label}
              style:--swatch={swatch.color || undefined}
              onclick={() => pick(entry, swatch.color)}
            >
              {#if swatch.color === entry.current}<Check size={10} strokeWidth={3} aria-hidden="true" />{/if}
            </button>
          {/each}
        </div>
      {:else}
        <button
          class="item"
          class:danger={entry.danger}
          role="menuitem"
          aria-disabled={entry.disabled ? "true" : undefined}
          tabindex={entry.disabled ? -1 : 0}
          onpointermove={(e) => !entry.disabled && e.currentTarget.focus()}
          onclick={() => void choose(entry)}
        >
          <span class="truncate">{entry.label}</span>
          {#if entry.keys}<kbd class="num">{formatKeys(entry.keys)}</kbd>{/if}
        </button>
      {/if}
    {/each}
  </div>
</Surface>

<style>
  .menu {
    min-width: 200px;
    max-width: 320px;
    overflow-y: auto;
    padding: var(--space-2);
  }

  .item {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-6);
    width: 100%;
    height: 28px;
    padding: 0 var(--space-3);
    border-radius: var(--radius-control);
    text-align: left;
  }

  .item:focus,
  .item:focus-visible {
    outline: none;
    background: var(--bg-3);
  }

  .item[aria-disabled="true"] {
    color: var(--text-3);
  }

  .item.danger:not([aria-disabled="true"]) {
    color: var(--danger);
  }

  .swatches {
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: 28px;
    padding: 0 var(--space-3);
  }

  .swatch {
    display: grid;
    place-items: center;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: var(--swatch);
    color: #ffffff;
  }

  .swatch.none {
    border: 1px solid var(--text-3);
    color: var(--text-2);
  }

  .swatch:hover,
  .swatch:focus-visible {
    outline: 2px solid var(--text-2);
    outline-offset: 1px;
  }

  .swatch[aria-disabled="true"] {
    opacity: 0.4;
    pointer-events: none;
  }

  kbd {
    flex: none;
    color: var(--text-3);
    font: inherit;
    font-size: var(--text-meta);
  }

  .separator {
    height: 1px;
    margin: var(--space-2) calc(-1 * var(--space-2));
    background: var(--border);
  }
</style>
