<!-- A facet button with a small popover under it. -->
<script lang="ts">
  import ChevronDown from "@lucide/svelte/icons/chevron-down";
  import type { Snippet } from "svelte";
  import { dismissable } from "../../lib/list/dismiss";

  interface Props {
    label: string;
    /** Current value, shown after the label; "" when the facet is unset. */
    value?: string;
    open?: boolean;
    children: Snippet;
  }

  let { label, value = "", open = $bindable(false), children }: Props = $props();
</script>

<div class="facet" data-facet={label} use:dismissable={{ open: () => open, close: () => (open = false) }}>
  <button
    class="trigger"
    class:set={value !== ""}
    type="button"
    aria-haspopup="dialog"
    aria-expanded={open}
    onclick={() => (open = !open)}
  >
    <span class="name">{label}</span>
    {#if value !== ""}<span class="value truncate">{value}</span>{/if}
    <ChevronDown size={12} strokeWidth={1.5} />
  </button>
  {#if open}
    <div class="panel popover" role="dialog" aria-label={label}>
      {@render children()}
    </div>
  {/if}
</div>

<style>
  .facet {
    position: relative;
    flex: none;
  }

  .trigger {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    max-width: 200px;
    height: 24px;
    padding: 0 var(--space-3);
    border-radius: var(--radius-control);
    background: var(--bg-2);
    color: var(--text-2);
    font-size: var(--text-meta);
    transition: background var(--dur-fast) var(--ease);
  }

  .trigger:hover,
  .trigger[aria-expanded="true"] {
    background: var(--bg-3);
    color: var(--text-1);
  }

  .trigger.set {
    color: var(--text-1);
  }

  .value {
    font-weight: 500;
  }

  .panel {
    position: absolute;
    top: calc(100% + var(--space-2));
    left: 0;
    z-index: var(--z-popover);
    padding: var(--space-3);
  }
</style>
