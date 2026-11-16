<!--
  Project tempo and key, the target of matched preview. Click or press B to
  edit: type "124", "am" or "124 am"; "-" clears the key.
-->
<script lang="ts">
  import { tick } from "svelte";
  import { editingProject, formatProject, parseProject, setProject } from "../../lib/audio";
  import { formatKeys } from "../../lib/commands";
  import { closeOverlay, openOverlay, settings } from "../../lib/state.svelte";

  const editing = $derived(editingProject());
  const label = $derived(formatProject(settings().projectBpm, settings().projectKey));

  let input = $state<HTMLInputElement>();
  let text = $state("");
  let error = $state("");

  $effect(() => {
    if (!editing) return;
    text = formatProject(settings().projectBpm, settings().projectKey, " ");
    error = "";
    void tick().then(() => {
      input?.focus();
      input?.select();
    });
  });

  function commit(): boolean {
    const result = parseProject(text);
    if (!result.ok) {
      error = result.error;
      return false;
    }
    setProject(result.patch);
    closeOverlay("project");
    return true;
  }

  function onkeydown(event: KeyboardEvent): void {
    if (event.key === "Enter") {
      event.preventDefault();
      commit();
    }
  }

  function onblur(): void {
    // Leaving the field keeps a valid entry and drops an invalid one.
    if (editing && !commit()) closeOverlay("project");
  }
</script>

<div class="project">
  {#if editing}
    <input
      bind:this={input}
      bind:value={text}
      class="field num"
      class:invalid={error !== ""}
      aria-label="Project tempo and key"
      aria-invalid={error !== ""}
      placeholder="124 am"
      spellcheck="false"
      autocomplete="off"
      oninput={() => (error = "")}
      {onkeydown}
      {onblur}
    />
    <div class="hint popover" class:invalid={error !== ""} role="status">
      {error || "Tempo, key or both · “-” clears the key"}
    </div>
  {:else}
    <button
      class="pill num"
      title="Project tempo and key ({formatKeys('B')} to edit, [ ] to nudge)"
      onclick={() => openOverlay("project")}
    >
      {label}
    </button>
  {/if}
</div>

<style>
  .project {
    position: relative;
    flex: none;
  }

  .pill,
  .field {
    display: block;
    width: 84px;
    height: 28px;
    padding: 0 var(--space-3);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    font-size: var(--text-meta);
    text-align: center;
  }

  .pill {
    overflow: hidden;
    color: var(--text-1);
    white-space: nowrap;
    text-overflow: ellipsis;
    transition: background var(--dur-fast) var(--ease);
  }

  .pill:hover {
    background: var(--bg-2);
  }

  .field {
    border-color: var(--accent);
    background: var(--bg-0);
    outline: none;
  }

  .field.invalid {
    border-color: var(--danger);
  }

  .hint {
    position: absolute;
    right: 0;
    bottom: calc(100% + var(--space-3));
    z-index: var(--z-popover);
    padding: var(--space-2) var(--space-3);
    color: var(--text-2);
    font-size: var(--text-meta);
    white-space: nowrap;
  }

  .hint.invalid {
    color: var(--danger);
  }
</style>
