<!-- Filterable option list inside a facet popover (tags, packs). -->
<script lang="ts">
  import { isComposing } from "../../lib/commands";

  interface Option {
    value: string;
    label: string;
    count: number;
  }

  interface Props {
    options: readonly Option[];
    selected: readonly string[];
    placeholder: string;
    onpick: (value: string) => void;
  }

  let { options, selected, placeholder, onpick }: Props = $props();

  const MAX_SHOWN = 60;
  let filter = $state("");
  let active = $state(0);

  const chosen = $derived(new Set(selected.map((s) => s.toLowerCase())));
  const shown = $derived.by(() => {
    const needle = filter.trim().toLowerCase();
    const matches = needle === "" ? options : options.filter((o) => o.label.toLowerCase().includes(needle));
    return matches.slice(0, MAX_SHOWN);
  });

  $effect(() => {
    void shown;
    active = 0;
  });

  function onkeydown(e: KeyboardEvent): void {
    if (isComposing(e)) return;
    if (e.key === "ArrowDown") active = Math.min(shown.length - 1, active + 1);
    else if (e.key === "ArrowUp") active = Math.max(0, active - 1);
    else if (e.key === "Enter" && shown[active]) onpick(shown[active].value);
    else return;
    e.preventDefault();
  }

  function focusOnMount(node: HTMLInputElement): void {
    node.focus();
  }
</script>

<div class="picker">
  <input
    type="text"
    {placeholder}
    spellcheck="false"
    autocomplete="off"
    aria-label={placeholder}
    bind:value={filter}
    {onkeydown}
    use:focusOnMount
  />
  <ul role="listbox">
    {#each shown as option, index (option.value)}
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <li
        role="option"
        aria-selected={chosen.has(option.value.toLowerCase())}
        class:active={index === active}
        class:chosen={chosen.has(option.value.toLowerCase())}
        onmousedown={(e) => e.preventDefault()}
        onclick={() => onpick(option.value)}
        onpointermove={() => (active = index)}
      >
        <span class="truncate">{option.label}</span>
        <span class="num count">{option.count.toLocaleString()}</span>
      </li>
    {:else}
      <li class="none">No matches</li>
    {/each}
  </ul>
</div>

<style>
  .picker {
    width: 240px;
  }

  input {
    width: 100%;
    height: 28px;
    padding: 0 var(--space-3);
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    outline: none;
    background: var(--bg-0);
  }

  input:focus {
    border-color: var(--text-3);
  }

  ul {
    max-height: 280px;
    margin-top: var(--space-2);
    overflow-y: auto;
  }

  li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-4);
    height: 26px;
    padding: 0 var(--space-3);
    border-radius: var(--radius-control);
    color: var(--text-2);
  }

  li.active {
    background: var(--bg-3);
  }

  li.chosen {
    color: var(--text-1);
    font-weight: 500;
  }

  .count,
  .none {
    color: var(--text-3);
    font-size: var(--text-meta);
  }

  .count {
    flex: none;
    font-weight: 400;
  }
</style>
