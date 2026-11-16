<script lang="ts">
  import { onMount } from "svelte";
  import Banner from "./components/shell/Banner.svelte";
  import FacetStrip from "./components/shell/FacetStrip.svelte";
  import Omnibar from "./components/shell/Omnibar.svelte";
  import SampleList from "./components/shell/SampleList.svelte";
  import Sidebar from "./components/shell/Sidebar.svelte";
  import PlayerBar from "./components/player/PlayerBar.svelte";
  import Overlays from "./components/overlays/Overlays.svelte";
  import { installKeyboard, isTyping } from "./lib/commands";
  import { app, init, settings, sidebarVisible, view } from "./lib/state.svelte";

  onMount(() => {
    const uninstallKeyboard = installKeyboard();
    let stop = () => {};
    let unmounted = false;
    void init().then((fn) => {
      if (unmounted) fn();
      else stop = fn;
    });
    return () => {
      unmounted = true;
      uninstallKeyboard();
      stop();
    };
  });

  const sidebarShown = $derived(sidebarVisible());

  /**
   * A mouse click on a button must not take the keyboard with it: Space
   * would then press that button again instead of playing, and the arrows
   * would stop moving through the list.
   */
  function keepKeyboardOnList(e: MouseEvent): void {
    const target = e.target as HTMLElement;
    if (!target.closest("button") || target.closest("input, textarea, select, [role='slider']")) return;
    e.preventDefault();
    if (!isTyping(document.activeElement)) {
      document.querySelector<HTMLElement>('[role="listbox"][aria-multiselectable="true"]')?.focus({ preventScroll: true });
    }
  }
</script>

{#if app.state}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="app"
    class:sidebar-hidden={!sidebarShown}
    onmousedown={keepKeyboardOnList}
    style:--sidebar-width="{settings().sidebarWidth}px"
  >
    <aside class="sidebar" inert={!sidebarShown}>
      <Sidebar />
    </aside>
    <main class="main">
      <Omnibar />
      {#if view.facetsOpen}
        <FacetStrip />
      {/if}
      <Banner />
      <div class="list">
        <SampleList />
      </div>
      <PlayerBar />
    </main>
  </div>
  <Overlays />
{/if}

<style>
  .app {
    display: grid;
    grid-template-columns: var(--sidebar-width) minmax(0, 1fr);
    height: 100%;
    transition: grid-template-columns var(--dur-sidebar) var(--ease);
  }

  .app.sidebar-hidden {
    grid-template-columns: 0 minmax(0, 1fr);
  }

  .sidebar {
    min-width: 0;
    overflow: hidden;
    border-right: 1px solid var(--border);
    background: var(--bg-1);
  }

  .sidebar-hidden .sidebar {
    border-right-width: 0;
  }

  .main {
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
  }

  .list {
    flex: 1;
    min-height: 0;
    position: relative;
  }
</style>
