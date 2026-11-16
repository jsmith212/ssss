<!--
  Toast stack, newest at the bottom. Info toasts dismiss themselves (the
  timers live in state); errors stay until dismissed.
-->
<script lang="ts">
  import X from "@lucide/svelte/icons/x";
  import { dismissToast, toasts, type Toast } from "../../lib/state.svelte";
  import { report } from "./targets.svelte";

  async function act(toast: Toast): Promise<void> {
    dismissToast(toast.id);
    try {
      await toast.action?.run();
    } catch (err) {
      report(err);
    }
  }
</script>

<!-- data-surface-keep: using a toast does not count as clicking outside a popover. -->
<div class="toasts" data-surface-keep>
  {#each toasts.list as toast (toast.id)}
    <div
      class="popover toast"
      class:error={toast.level === "error"}
      role={toast.level === "error" ? "alert" : "status"}
      aria-live={toast.level === "error" ? "assertive" : "polite"}
    >
      <span class="message selectable">{toast.message}</span>
      {#if toast.action}
        <button class="action" onclick={() => void act(toast)}>{toast.action.label}</button>
      {/if}
      {#if toast.level === "error" || !toast.action}
        <button class="dismiss" aria-label="Dismiss" onclick={() => dismissToast(toast.id)}>
          <X size={14} strokeWidth={1.5} aria-hidden="true" />
        </button>
      {/if}
    </div>
  {/each}
</div>

<style>
  .toasts {
    position: fixed;
    right: var(--space-5);
    bottom: calc(var(--player-height) + var(--space-4));
    z-index: var(--z-toast);
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: var(--space-3);
    pointer-events: none;
  }

  .toast {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    max-width: min(440px, calc(100vw - 32px));
    min-height: 36px;
    padding: var(--space-2) var(--space-2) var(--space-2) var(--space-4);
    pointer-events: auto;
    animation: toast-in var(--dur-fast) var(--ease);
  }

  .toast.error {
    box-shadow:
      var(--shadow-popover),
      inset 2px 0 0 var(--danger);
  }

  .message {
    padding: var(--space-2) var(--space-3) var(--space-2) 0;
    overflow-wrap: anywhere;
  }

  .action,
  .dismiss {
    display: grid;
    flex: none;
    place-items: center;
    height: 24px;
    border-radius: var(--radius-control);
    transition: background var(--dur-fast) var(--ease);
  }

  .action {
    padding: 0 var(--space-3);
    font-weight: 500;
  }

  .dismiss {
    width: 24px;
    color: var(--text-3);
  }

  .action:hover,
  .dismiss:hover {
    background: var(--bg-3);
    color: var(--text-1);
  }

  @keyframes toast-in {
    from {
      opacity: 0;
      transform: translateY(6px);
    }
  }
</style>
