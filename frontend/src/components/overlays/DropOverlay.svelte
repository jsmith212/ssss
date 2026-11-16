<!--
  Feedback while an OS file drag is over the window: an accent ring around
  the window and a line saying where the files will go.

  Wails gives the page no drag event. On every platform it only toggles the
  class `file-drop-target-active` on the hovered `[data-file-drop-target]`
  element, so this watches for that class. The body is made a drop target
  here so that the whole window accepts files; sidebar items are nearer
  targets and win. The drop itself is handled in Go and announced by the
  "drop" event.

  Dragging samples out of the list lights up the same class, because the
  pointer starts over our own window. Such a drag imports nothing: on a tag
  or a collection it assigns the samples, anywhere else it does nothing, and
  the feedback says so.
-->
<script lang="ts">
  import Download from "@lucide/svelte/icons/download";
  import { onMount } from "svelte";
  import { api } from "../../lib/api";
  import { list } from "../../lib/list/controller.svelte";
  import { announceUndoable, drop, libraryUsable, sidebar } from "../../lib/state.svelte";
  import type { DropEvent } from "../../lib/types";
  import { plural } from "./format";
  import { installMockDrop } from "./mockDrop";

  const ACTIVE = "file-drop-target-active";

  let target = $state.raw<HTMLElement | null>(null);
  /**
   * The drag over the window is our own drag-out. Latched until the hover
   * ends: Wails may clear the class a moment after the drag has finished.
   */
  let own = $state(false);

  $effect(() => {
    // Lets app.css drop the target tint from sidebar items that ignore a self-drop.
    document.documentElement.toggleAttribute("data-self-drag", own);
  });

  function collectionName(id: string): string {
    return sidebar.data?.collections.find((c) => c.id === id)?.name ?? "collection";
  }

  const message = $derived.by(() => {
    if (!target) return "";
    const id = target.dataset.dropId ?? "";
    if (own) {
      if (!libraryUsable()) return "";
      if (target.dataset.dropKind === "tag") return `Tag with ${id}`;
      if (target.dataset.dropKind === "collection") return `Add to ${collectionName(id)}`;
      return "";
    }
    if (!libraryUsable()) return "Files can’t be added while the library is unavailable";
    switch (target.dataset.dropKind) {
      case "pack":
        return `Add to ${sidebar.data?.packs.find((p) => p.id === id)?.name ?? "pack"}`;
      case "tag":
        return `Add to library · tag ${id}`;
      case "collection":
        return `Add to library · collection ${collectionName(id)}`;
      default:
        return "Add to library";
    }
  });

  function announce(event: DropEvent): void {
    // An import shows up as a job; only an assignment needs saying.
    if (event.kind !== "assign") return;
    const what = plural(event.count, "sample");
    if (event.target.tag) announceUndoable(`Tagged ${what} ${event.target.tag}`);
    else if (event.target.collectionId) announceUndoable(`Added ${what} to ${collectionName(event.target.collectionId)}`);
  }

  onMount(() => {
    const body = document.body;
    body.setAttribute("data-file-drop-target", "");
    body.dataset.dropKind = "list";

    const observer = new MutationObserver((records) => {
      let next = target;
      for (const record of records) {
        const el = record.target as HTMLElement;
        if (el.classList.contains(ACTIVE)) next = el;
        else if (el === next) next = null;
      }
      own = next !== null && (own || list.dragging);
      target = next;
      drop.active = next !== null && !own;
    });
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["class"], subtree: true });

    const offDrop = api.on("drop", announce);
    const uninstallMock = api.isMock ? installMockDrop() : undefined;

    return () => {
      observer.disconnect();
      offDrop();
      uninstallMock?.();
      body.removeAttribute("data-file-drop-target");
      delete body.dataset.dropKind;
      drop.active = false;
      document.documentElement.removeAttribute("data-self-drag");
    };
  });
</script>

{#if target && !own}
  <div class="ring" class:blocked={!libraryUsable()}></div>
{/if}
<div class="label-host" role="status" aria-live="polite">
  {#if message !== ""}
    <div class="popover label">
      {#if !own && libraryUsable()}<Download size={16} strokeWidth={1.5} aria-hidden="true" />{/if}
      <span>{message}</span>
    </div>
  {/if}
</div>

<style>
  /* The window-level targets get the ring below instead of the generic
     drop-target tint from app.css. */
  :global(body.file-drop-target-active) {
    outline: none;
    background-color: var(--bg-0);
  }

  .ring {
    position: fixed;
    inset: 0;
    z-index: var(--z-toast);
    box-shadow: inset 0 0 0 2px var(--accent);
    pointer-events: none;
    animation: ring-in var(--dur-fast) var(--ease);
  }

  .ring.blocked {
    box-shadow: inset 0 0 0 2px var(--text-3);
  }

  .label-host {
    position: fixed;
    bottom: calc(var(--player-height) + var(--space-5));
    left: 50%;
    z-index: var(--z-toast);
    translate: -50% 0;
    pointer-events: none;
  }

  .label {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    height: 36px;
    padding: 0 var(--space-5);
    font-weight: 500;
    white-space: nowrap;
    animation: ring-in var(--dur-fast) var(--ease);
  }

  .label :global(svg) {
    color: var(--accent);
  }

  @keyframes ring-in {
    from {
      opacity: 0;
    }
  }
</style>
