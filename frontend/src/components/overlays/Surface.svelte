<!--
  The one floating surface every overlay is built on: placement, focus trap,
  Escape, light dismiss, and returning focus to where it came from.
-->
<script lang="ts">
  import { onMount, type Snippet } from "svelte";
  import { focusables, listElement, trapTab, type Point } from "./focus";

  interface Props {
    /** Accessible name. */
    label: string;
    onclose: () => void;
    /** "anchor" needs `anchor`; without one the surface centres itself. */
    placement?: "anchor" | "center" | "top" | "screen";
    anchor?: Point | null;
    /** Anchor to an element instead of a point; follows it when the window resizes. */
    anchorTo?: HTMLElement | null;
    /** Open above the anchor (popovers that hang off something at the bottom edge). */
    above?: boolean;
    width?: number;
    /** Dim the app behind and block interaction with it. */
    modal?: boolean;
    /** Escape and a click outside close the surface. */
    dismissable?: boolean;
    /** Keep every key press inside, shortcuts with Cmd/Ctrl included. */
    exclusive?: boolean;
    role?: "dialog" | "none";
    children: Snippet;
  }

  let {
    label,
    onclose,
    placement = "anchor",
    anchor = null,
    anchorTo = null,
    above = false,
    width,
    modal = false,
    dismissable = true,
    exclusive = false,
    role = "dialog",
    children,
  }: Props = $props();

  const MARGIN = 8;

  let root: HTMLDivElement;
  let position = $state<{ left: number; top: number } | null>(null);
  const GAP = 6;
  const anchored = $derived(placement === "anchor" && (anchor != null || anchorTo != null));

  function anchorPoint(): Point | null {
    if (!anchorTo) return anchor;
    const r = anchorTo.getBoundingClientRect();
    return { x: r.left, y: above ? r.top - GAP : r.bottom + GAP };
  }

  function place(): void {
    const at = anchored ? anchorPoint() : null;
    if (!at) return;
    const { width: w, height: h } = root.getBoundingClientRect();
    const maxLeft = window.innerWidth - w - MARGIN;
    const maxTop = window.innerHeight - h - MARGIN;
    const top = above ? at.y - h : at.y;
    position = {
      left: Math.max(MARGIN, Math.min(at.x, maxLeft)),
      top: Math.max(MARGIN, Math.min(top, maxTop)),
    };
  }

  onMount(() => {
    const previous = document.activeElement as HTMLElement | null;
    (root.querySelector<HTMLElement>("[data-autofocus]") ?? focusables(root)[0] ?? root).focus({
      preventScroll: true,
    });

    place();
    const resize = new ResizeObserver(place);
    resize.observe(root);
    window.addEventListener("resize", place);

    const onPointerDown = (event: PointerEvent) => {
      if (!dismissable || modal) return;
      const target = event.target as Element | null;
      // Other surfaces and the toasts are not "outside": a toast's Undo must
      // not dismiss the popover it belongs to.
      if (target?.closest("[data-surface], [data-surface-keep]")) return;
      onclose();
    };
    window.addEventListener("pointerdown", onPointerDown, true);

    return () => {
      resize.disconnect();
      window.removeEventListener("resize", place);
      window.removeEventListener("pointerdown", onPointerDown, true);
      // Give focus back only if it is still ours to give; a surface opened
      // from this one (palette -> tags) has already taken it.
      const active = document.activeElement;
      if (active && active !== document.body && !root.contains(active)) return;
      const back = previous?.isConnected && previous !== document.body ? previous : listElement();
      back?.focus({ preventScroll: true });
    };
  });

  function onkeydown(event: KeyboardEvent): void {
    if (event.key === "Escape") {
      event.stopPropagation();
      if (dismissable) {
        event.preventDefault();
        onclose();
      }
      return;
    }
    if (event.key === "Tab") trapTab(event, root);
    // Plain keys typed here must not reach the global shortcut map (Space
    // would play, arrows would move the list); Cmd/Ctrl shortcuts still do.
    if (exclusive || !(event.metaKey || event.ctrlKey)) event.stopPropagation();
  }
</script>

{#if modal}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="scrim" class:opaque={placement === "screen"} onpointerdown={() => dismissable && onclose()}></div>
{/if}
<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div
  bind:this={root}
  class="surface {anchored ? 'anchor' : placement === 'anchor' ? 'center' : placement}"
  class:popover={placement !== "screen"}
  class:placed={!anchored || position !== null}
  data-surface
  {role}
  aria-label={label}
  aria-modal={role === "dialog" ? modal : undefined}
  tabindex="-1"
  style:width={width ? `${width}px` : undefined}
  style:left={anchored && position ? `${position.left}px` : undefined}
  style:top={anchored && position ? `${position.top}px` : undefined}
  {onkeydown}
>
  {@render children()}
</div>

<style>
  .scrim {
    position: fixed;
    inset: 0;
    z-index: var(--z-overlay);
    background: rgb(0 0 0 / 0.4);
    animation: fade var(--dur-fast) var(--ease);
  }

  .scrim.opaque {
    background: var(--bg-0);
    animation: none;
  }

  .surface {
    position: fixed;
    z-index: var(--z-overlay);
    display: flex;
    flex-direction: column;
    max-width: calc(100vw - 16px);
    max-height: calc(100vh - 16px);
    color: var(--text-1);
    animation: rise var(--dur-fast) var(--ease);
  }

  .surface:focus-visible {
    outline: none;
  }

  /* Invisible until measured, so an anchored popover never flashes at 0,0.
     Not `visibility: hidden`: the surface must be focusable straight away. */
  .surface:not(.placed) {
    opacity: 0;
  }

  .center {
    top: 50%;
    left: 50%;
    translate: -50% -50%;
  }

  .top {
    top: min(18vh, 140px);
    left: 50%;
    max-height: calc(100vh - min(18vh, 140px) - 24px);
    translate: -50% 0;
  }

  .screen {
    inset: 0;
    max-width: none;
    max-height: none;
    align-items: center;
    justify-content: center;
    animation: none;
  }

  @keyframes rise {
    from {
      opacity: 0;
      transform: translateY(4px);
    }
  }

  @keyframes fade {
    from {
      opacity: 0;
    }
  }
</style>
