<!--
  The omnibar: free text plus filter tokens shown as chips, with completion
  for tags, packs, collections and keys. Its string is the one filtering
  model; the sidebar and the facet strip only rewrite it.
-->
<script lang="ts">
  import ArrowDown from "@lucide/svelte/icons/arrow-down";
  import ArrowUp from "@lucide/svelte/icons/arrow-up";
  import Ellipsis from "@lucide/svelte/icons/ellipsis";
  import Search from "@lucide/svelte/icons/search";
  import SlidersHorizontal from "@lucide/svelte/icons/sliders-horizontal";
  import X from "@lucide/svelte/icons/x";
  import { formatKeys, isComposing, run as runCommand } from "../../lib/commands";
  import { chooseSort, OMNIBAR_INPUT_ID, SORT_LABELS, sortChoices } from "../../lib/list/commands";
  import { list, navigate } from "../../lib/list/controller.svelte";
  import { dismissable } from "../../lib/list/dismiss";
  import {
    addToken,
    chipLabel,
    isFilterWord,
    joinQuery,
    splitQuery,
    suggest,
    tokenize,
    wordAt,
    type Suggestion,
    type Token,
  } from "../../lib/query";
  import { selection, setQuery, sidebar, sidebarVisible, view } from "../../lib/state.svelte";

  const TYPING_DEBOUNCE_MS = 60;
  /** Chips with a longer label than this give way when the bar is full; shorter ones stay whole. */
  const LONG_CHIP = 12;

  let input = $state<HTMLInputElement>();
  /** Text in the input: free-text terms and any token still being typed. */
  let draft = $state("");
  let caret = $state(0);
  let focused = $state(false);
  /** Escape hid the suggestions; typing brings them back. */
  let dismissed = $state(false);
  let active = $state(-1);
  let sortOpen = $state(false);

  /** The query this component last wrote; anything else came from outside. */
  let written = "";
  let timer: ReturnType<typeof setTimeout> | undefined;

  const chips = $derived(splitQuery(view.query).chips);
  /** Pack and collection chips hold ids; they read as names. */
  const label = (chip: Token) => chipLabel(chip, sidebar.data ?? undefined);

  let chipStrip = $state<HTMLElement>();

  // More chips than fit scroll sideways; keep the newest, next to the input, in view.
  $effect(() => {
    void chips;
    if (chipStrip) chipStrip.scrollLeft = chipStrip.scrollWidth;
  });

  $effect(() => {
    const query = view.query;
    if (query === written) return;
    // Sidebar, facets, Escape or a saved view replaced the query.
    clearTimeout(timer);
    written = query;
    setDraft(splitQuery(query).text);
    dismissed = true;
  });

  const current = $derived(wordAt(draft, caret));
  const completing = $derived(isFilterWord(current.word));
  const suggestions = $derived<Suggestion[]>(
    focused && !dismissed && sidebar.data ? suggest(current.word, sidebar.data) : [],
  );

  $effect(() => {
    // A token being typed preselects its best completion; plain text does not,
    // so Enter keeps meaning "search for what I typed".
    active = suggestions.length > 0 && completing ? 0 : -1;
  });

  /** The draft minus tokens: what counts as free text right now. */
  function liveText(): string {
    const rest = completing ? draft.slice(0, current.start) + draft.slice(current.end) : draft;
    return splitQuery(rest).text;
  }

  function write(query: string): void {
    clearTimeout(timer);
    written = query;
    setQuery(query);
  }

  function schedule(): void {
    clearTimeout(timer);
    const query = joinQuery(chips, liveText());
    timer = setTimeout(() => write(query), TYPING_DEBOUNCE_MS);
  }

  /**
   * Replaces the input text. The element is written directly as well: when
   * the draft ends up equal to what it was before the keystroke, Svelte sees
   * no change and would leave the typed characters in place.
   */
  function setDraft(text: string): void {
    draft = text;
    caret = text.length;
    if (input && input.value !== text) input.value = text;
  }

  /** Turns complete tokens in the draft into chips. Returns whether any were. */
  function promote(): boolean {
    const typed = splitQuery(draft);
    if (typed.chips.length === 0) return false;
    let query = joinQuery(chips, typed.text);
    for (const chip of typed.chips) query = addToken(query, chip);
    setDraft(typed.text === "" ? "" : typed.text + " ");
    write(query);
    return true;
  }

  function accept(s: Suggestion): void {
    const rest = (draft.slice(0, current.start) + draft.slice(current.end)).replace(/\s+/g, " ").trimStart();
    const [token] = tokenize(s.token);
    setDraft(rest);
    write(addToken(joinQuery(chips, splitQuery(rest).text), token));
    input?.focus();
  }

  function removeChip(index: number): void {
    write(joinQuery(chips.filter((_, i) => i !== index), liveText()));
    input?.focus();
  }

  function syncCaret(): void {
    caret = input?.selectionStart ?? draft.length;
  }

  function oninput(e: Event & { currentTarget: HTMLInputElement }): void {
    draft = e.currentTarget.value;
    syncCaret();
    dismissed = false;
    // A space or Enter inside an IME composition picks a candidate; the text
    // is not final until the composition ends, which fires another input.
    if (e instanceof InputEvent && e.isComposing) return;
    const typedSpace = /\s$/.test(draft.slice(0, caret));
    const pasted = e instanceof InputEvent && e.inputType === "insertFromPaste";
    if ((typedSpace || pasted) && promote()) return;
    schedule();
  }

  function toList(): void {
    input?.blur();
    document.querySelector<HTMLElement>('[role="listbox"]')?.focus();
    if (selection.focusIndex < 0 && list.total > 0) navigate("down");
  }

  function onkeydown(e: KeyboardEvent): void {
    // Enter, Tab and the arrows belong to the IME while it is composing.
    if (isComposing(e)) return;
    const open = suggestions.length > 0;
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        if (open) active = (active + 1) % suggestions.length;
        else toList();
        return;
      case "ArrowUp":
        if (!open) return;
        e.preventDefault();
        active = active <= 0 ? suggestions.length - 1 : active - 1;
        return;
      case "Tab":
        if (!open) return;
        e.preventDefault();
        accept(suggestions[Math.max(0, active)]);
        return;
      case "Enter":
        e.preventDefault();
        if (open && active >= 0) {
          accept(suggestions[active]);
        } else if (!promote()) {
          write(joinQuery(chips, liveText()));
          toList();
        }
        return;
      case "Escape":
        if (!open) return;
        // Only close the suggestions; the next Escape clears the query.
        e.preventDefault();
        e.stopPropagation();
        dismissed = true;
        return;
      case "Backspace":
        if (chips.length > 0 && input?.selectionStart === 0 && input.selectionEnd === 0) {
          e.preventDefault();
          removeChip(chips.length - 1);
        }
        return;
    }
  }

  function onblur(): void {
    focused = false;
    if (!promote()) write(joinQuery(chips, liveText()));
  }

  function chipTitle(chip: Token): string {
    return chip.negated ? `Excluding ${label({ ...chip, negated: false })}` : label(chip);
  }
</script>

<header class="omnibar">
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="field" onclick={(e) => e.target === e.currentTarget && input?.focus()}>
    <Search class="icon" />
    {#if chips.length > 0}
      <div class="chips" bind:this={chipStrip}>
        {#each chips as chip, index (index + chip.raw)}
          <span
            class="token"
            class:negated={chip.negated}
            class:long={label(chip).length > LONG_CHIP}
            title={chipTitle(chip)}
          >
            <span class="truncate">{label(chip)}</span>
            <button
              type="button"
              tabindex="-1"
              aria-label="Remove filter {label(chip)}"
              onmousedown={(e) => e.preventDefault()}
              onclick={() => removeChip(index)}
            >
              <X size={12} strokeWidth={1.5} />
            </button>
          </span>
        {/each}
      </div>
    {/if}
    <input
      id={OMNIBAR_INPUT_ID}
      bind:this={input}
      type="text"
      placeholder={chips.length === 0 ? "Search or filter…" : ""}
      spellcheck="false"
      autocomplete="off"
      autocapitalize="off"
      aria-label="Search or filter"
      role="combobox"
      aria-expanded={suggestions.length > 0}
      aria-controls="omnibar-suggestions"
      aria-autocomplete="list"
      value={draft}
      {oninput}
      {onkeydown}
      {onblur}
      onfocus={() => {
        focused = true;
        dismissed = false;
        syncCaret();
      }}
      onkeyup={syncCaret}
      onclick={syncCaret}
    />
    {#if suggestions.length > 0}
      <ul class="suggestions popover" id="omnibar-suggestions" role="listbox">
        {#each suggestions as s, index (s.token)}
          <!-- svelte-ignore a11y_click_events_have_key_events -->
          <li
            role="option"
            aria-selected={index === active}
            class:active={index === active}
            onmousedown={(e) => e.preventDefault()}
            onclick={() => accept(s)}
            onpointermove={() => (active = index)}
          >
            <span class="truncate">{s.label}</span>
            {#if s.count !== undefined}
              <span class="num count">{s.count.toLocaleString()}</span>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  {#if view.error}
    <span class="error truncate" role="status" title={view.error}>{view.error}</span>
  {/if}

  <span class="num total" aria-label="{view.total} samples">{view.total.toLocaleString()}</span>

  <button
    class="tool"
    class:on={view.facetsOpen}
    type="button"
    aria-label="Filters"
    aria-pressed={view.facetsOpen}
    title="Filters ({formatKeys('Mod+Shift+F')})"
    onmousedown={(e) => e.preventDefault()}
    onclick={() => (view.facetsOpen = !view.facetsOpen)}
  >
    <SlidersHorizontal size={16} strokeWidth={1.5} />
  </button>

  <div class="sort" use:dismissable={{ open: () => sortOpen, close: () => (sortOpen = false) }}>
    <button
      class="tool label"
      type="button"
      aria-haspopup="menu"
      aria-expanded={sortOpen}
      title="Sort"
      onmousedown={(e) => e.preventDefault()}
      onclick={() => (sortOpen = !sortOpen)}
    >
      {SORT_LABELS[view.sort]}
      {#if view.desc}<ArrowDown size={12} strokeWidth={1.5} />{:else}<ArrowUp size={12} strokeWidth={1.5} />{/if}
    </button>
    {#if sortOpen}
      <ul class="menu popover" role="menu">
        {#each sortChoices() as sort (sort)}
          <li>
            <button
              type="button"
              role="menuitemradio"
              aria-checked={sort === view.sort}
              onclick={() => {
                chooseSort(sort);
                sortOpen = false;
              }}
            >
              <span>{SORT_LABELS[sort]}</span>
              {#if sort === view.sort}
                {#if view.desc}<ArrowDown size={12} strokeWidth={1.5} />{:else}<ArrowUp size={12} strokeWidth={1.5} />{/if}
              {/if}
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>

  {#if !sidebarVisible()}
    <!-- With the sidebar (and its footer buttons) out of sight, this is the
         mouse's way to settings, shortcuts, adding files and the rest. -->
    <button
      class="tool"
      type="button"
      aria-label="Commands"
      title="Commands ({formatKeys('Mod+K')})"
      onmousedown={(e) => e.preventDefault()}
      onclick={() => runCommand("overlay.palette")}
    >
      <Ellipsis size={16} strokeWidth={1.5} />
    </button>
  {/if}
</header>

<style>
  .omnibar {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    flex: none;
    height: var(--omnibar-height);
    padding: 0 var(--space-4) 0 var(--space-5);
    border-bottom: 1px solid var(--border);
  }

  .field {
    position: relative;
    display: flex;
    align-items: center;
    gap: var(--space-2);
    flex: 1;
    min-width: 0;
    height: 100%;
    cursor: text;
  }

  .field :global(.icon) {
    margin-right: var(--space-2);
    color: var(--text-3);
  }

  /* Chips keep their text: only long ones shrink, down to a readable stub,
     and what still does not fit scrolls sideways instead of spilling over
     the controls to the right. */
  .chips {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    flex: 0 1 auto;
    min-width: 0;
    overflow-x: auto;
    scrollbar-width: none;
  }

  .chips::-webkit-scrollbar {
    display: none;
  }

  .token {
    display: inline-flex;
    align-items: center;
    flex: none;
    height: 22px;
    padding-left: 7px;
    border-radius: var(--radius-chip);
    background: var(--bg-2);
    color: var(--text-1);
    font-size: var(--text-meta);
    cursor: default;
  }

  .token.long {
    flex: 0 1 auto;
    min-width: 84px;
  }

  .token.negated {
    color: var(--text-2);
    text-decoration: line-through;
  }

  .token button {
    display: grid;
    place-items: center;
    flex: none;
    width: 20px;
    height: 22px;
    border-radius: var(--radius-chip);
    color: var(--text-3);
  }

  .token button:hover {
    color: var(--text-1);
  }

  input {
    flex: 1 0 64px;
    min-width: 0;
    height: 100%;
    padding: 0;
    border: 0;
    outline: none;
    background: none;
    font-size: var(--text-omnibar);
  }

  .suggestions {
    position: absolute;
    top: calc(100% - 2px);
    left: 20px;
    z-index: var(--z-popover);
    width: min(340px, 100%);
    padding: var(--space-2);
    cursor: default;
  }

  .suggestions li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-4);
    height: 28px;
    padding: 0 var(--space-3);
    border-radius: var(--radius-control);
    color: var(--text-2);
  }

  .suggestions li.active {
    background: var(--bg-3);
    color: var(--text-1);
  }

  .count {
    flex: none;
    color: var(--text-3);
    font-size: var(--text-meta);
  }

  .error {
    flex: 0 1 auto;
    min-width: 0;
    max-width: 40%;
    color: var(--danger);
    font-size: var(--text-meta);
  }

  .total {
    flex: none;
    padding: 0 var(--space-2);
    color: var(--text-3);
    font-size: var(--text-meta);
  }

  .tool {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: var(--space-2);
    flex: none;
    min-width: 28px;
    height: 28px;
    border-radius: var(--radius-control);
    color: var(--text-2);
    transition: background var(--dur-fast) var(--ease);
  }

  .tool:hover,
  .tool[aria-expanded="true"] {
    background: var(--bg-2);
    color: var(--text-1);
  }

  .tool.on {
    background: var(--bg-3);
    color: var(--text-1);
  }

  .tool.label {
    padding: 0 var(--space-3);
  }

  .sort {
    position: relative;
    flex: none;
  }

  .menu {
    position: absolute;
    top: calc(100% + var(--space-2));
    right: 0;
    z-index: var(--z-popover);
    min-width: 148px;
    padding: var(--space-2);
  }

  .menu button {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-4);
    width: 100%;
    height: 28px;
    padding: 0 var(--space-3);
    border-radius: var(--radius-control);
    color: var(--text-2);
    text-align: left;
  }

  .menu button:hover,
  .menu button[aria-checked="true"] {
    color: var(--text-1);
  }

  .menu button:hover {
    background: var(--bg-3);
  }
</style>
