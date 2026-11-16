<!--
  Mounts every overlay. App.svelte renders only this component from the
  overlays directory, so overlays can be added or reworked without touching
  the shell.
-->
<script lang="ts">
  import { onMount, type Component } from "svelte";
  import { api } from "../../lib/api";
  import { app, overlays, type OverlayId } from "../../lib/state.svelte";
  import CollectionPopover from "./CollectionPopover.svelte";
  import CommandPalette from "./CommandPalette.svelte";
  import { registerOverlayCommands } from "./commands";
  import ContextMenu from "./ContextMenu.svelte";
  import DropOverlay from "./DropOverlay.svelte";
  import FirstRun from "./FirstRun.svelte";
  import InfoPopover from "./InfoPopover.svelte";
  import JobPill from "./JobPill.svelte";
  import RenamePopover from "./RenamePopover.svelte";
  import Settings from "./Settings.svelte";
  import ShortcutSheet from "./ShortcutSheet.svelte";
  import SpliceImport from "./SpliceImport.svelte";
  import TagPopover from "./TagPopover.svelte";
  import Toasts from "./Toasts.svelte";

  // "project" (the project BPM/key editor) belongs to the player bar.
  const SURFACES: Partial<Record<OverlayId, Component>> = {
    tags: TagPopover,
    collections: CollectionPopover,
    info: InfoPopover,
    context: ContextMenu,
    rename: RenamePopover,
    splice: SpliceImport,
    settings: Settings,
    shortcuts: ShortcutSheet,
    palette: CommandPalette,
  };

  onMount(() => {
    const unregister = registerOverlayCommands();
    if (import.meta.env.DEV && api.isMock) {
      void import("./dev").then((dev) => dev.runDevDriver());
    }
    return unregister;
  });
</script>

{#if app.state?.status === "none"}
  <FirstRun />
{/if}
<!-- In stack order, so the most recently opened surface is on top. -->
{#each overlays.stack as open (open.id)}
  {@const Surface = SURFACES[open.id]}
  {#if Surface}<Surface />{/if}
{/each}
<DropOverlay />
<JobPill />
<Toasts />

<style>
  /* Shared by the overlay components. */
  :global(.ov-field) {
    height: 28px;
    padding: 0 var(--space-3);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--bg-0);
    transition: border-color var(--dur-fast) var(--ease);
  }

  :global(.ov-field:hover:not(:disabled)) {
    border-color: var(--text-3);
  }

  :global(.ov-field:disabled) {
    color: var(--text-3);
  }

  :global(.ov-field[aria-invalid="true"]) {
    border-color: var(--danger);
  }

  :global(kbd.ov-kbd) {
    display: inline-grid;
    flex: none;
    place-items: center;
    min-width: 20px;
    height: 20px;
    padding: 0 5px;
    border-radius: var(--radius-chip);
    background: var(--bg-3);
    color: var(--text-2);
    font: inherit;
    font-size: var(--text-label);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
</style>
