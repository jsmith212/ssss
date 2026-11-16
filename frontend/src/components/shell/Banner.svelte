<!-- One line under the omnibar while the library is offline or read-only. -->
<script lang="ts">
  import { app, errorMessage, locateLibrary, pushToast, retryLibrary } from "../../lib/state.svelte";

  const status = $derived(app.state?.status);
  let busy = $state(false);

  async function attempt(action: () => Promise<void>): Promise<void> {
    if (busy) return;
    busy = true;
    try {
      await action();
    } catch (err) {
      pushToast(errorMessage(err), { level: "error" });
    } finally {
      busy = false;
    }
  }
</script>

{#if status === "unavailable"}
  <div class="banner" role="status">
    <span class="text truncate" title={app.state?.message || undefined}>
      Library folder unavailable
      <span class="detail">· browsing from cache</span>
    </span>
    <button class="action" type="button" disabled={busy} onclick={() => attempt(retryLibrary)}>Retry</button>
    <button class="action" type="button" disabled={busy} onclick={() => attempt(locateLibrary)}>Locate…</button>
  </div>
{:else if status === "readonly"}
  <div class="banner" role="status">
    <span class="text truncate" title={app.state?.message || undefined}>
      Library is read-only
      {#if app.state?.message}<span class="detail">· {app.state.message}</span>{/if}
    </span>
    <button class="action" type="button" disabled={busy} onclick={() => attempt(retryLibrary)}>Retry</button>
  </div>
{/if}

<style>
  .banner {
    display: flex;
    align-items: center;
    gap: var(--space-4);
    flex: none;
    height: 28px;
    padding: 0 var(--space-5);
    border-bottom: 1px solid var(--border);
    background: var(--bg-1);
    color: var(--warning);
    font-size: var(--text-meta);
  }

  .text {
    flex: 0 1 auto;
    min-width: 0;
    font-weight: 500;
  }

  .detail {
    color: var(--text-2);
    font-weight: 400;
  }

  .action {
    flex: none;
    color: var(--text-1);
    text-decoration: underline;
    text-decoration-color: var(--text-3);
    text-underline-offset: 3px;
  }

  .action:hover {
    text-decoration-color: currentColor;
  }

  .action:disabled {
    color: var(--text-3);
  }
</style>
