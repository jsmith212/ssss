<!--
  Navigation by rewriting the query: a click replaces it with the item's
  token, Cmd/Ctrl-click adds (or removes) the token. The highlighted items
  are whatever tokens the query holds, however they got there.
-->
<script lang="ts">
  import ChevronRight from "@lucide/svelte/icons/chevron-right";
  import CircleQuestionMark from "@lucide/svelte/icons/circle-question-mark";
  import Plus from "@lucide/svelte/icons/plus";
  import SettingsIcon from "@lucide/svelte/icons/settings";
  import { api } from "../../lib/api";
  import { formatKeys, isComposing, isMac, run as runCommand } from "../../lib/commands";
  import {
    sidebarUi,
    startCreateCollection,
    startRenameItem,
    stopEditing,
    toggleSection,
    toggleTag,
    expandTags,
    type SidebarSection,
  } from "../../lib/list/sidebar.svelte";
  import { ancestors, buildTagTree, visibleTags } from "../../lib/list/tagtree";
  import {
    addToken,
    describeQuery,
    formatToken,
    refMatcher,
    toggleRef,
    toggleToken,
    tokenize,
    tokenMatcher,
    tokensOf,
    type Named,
    type RefKind,
    type TokenSpec,
  } from "../../lib/query";
  import {
    errorMessage,
    libraryUsable,
    previewSettings,
    pushToast,
    setQuery,
    settings,
    sidebar,
    updateSettings,
    view,
  } from "../../lib/state.svelte";
  import { openSidebarMenu } from "../overlays/menus";

  const MIN_WIDTH = 180;
  const MAX_WIDTH = 320;
  const DEFAULT_WIDTH = 220;

  const data = $derived(sidebar.data);
  const has = $derived(tokenMatcher(view.query));
  // Packs and collections are written by id, but a name typed in the omnibar
  // selects them too, so these look at what each token selects.
  const showsPack = $derived(refMatcher(view.query, "pack"));
  const showsCollection = $derived(refMatcher(view.query, "in"));
  const empty = $derived(view.query.trim() === "");

  const fixed = $derived<{ label: string; spec: TokenSpec | null; count: number }[]>([
    { label: "Library", spec: null, count: data?.all ?? 0 },
    { label: "Favourites", spec: { kind: "is", value: "fav" }, count: data?.favorites ?? 0 },
    { label: "Untagged", spec: { kind: "is", value: "untagged" }, count: data?.untagged ?? 0 },
    { label: "Recent", spec: { kind: "is", value: "recent" }, count: data?.recent ?? 0 },
  ]);

  const tagTree = $derived(buildTagTree(data?.tags ?? []));
  const tagRows = $derived(visibleTags(tagTree, sidebarUi.expandedTags));

  // A tag filter set elsewhere (omnibar, facets) should be visible in the tree.
  $effect(() => {
    expandTags(tokensOf(view.query, "tag").flatMap((t) => ancestors(t.value)));
  });

  function additive(e: MouseEvent): boolean {
    return isMac() ? e.metaKey : e.ctrlKey;
  }

  function focusList(): void {
    document.querySelector<HTMLElement>('[role="listbox"][aria-multiselectable="true"]')?.focus();
  }

  function go(e: MouseEvent, spec: TokenSpec | null): void {
    if (spec === null) setQuery("");
    else if (additive(e)) setQuery(toggleToken(view.query, spec));
    else setQuery(formatToken(spec));
    focusList();
  }

  /** A pack or a collection: the token holds its id, since names can repeat or contain anything. */
  function goRef(e: MouseEvent, kind: RefKind, item: Named): void {
    if (additive(e)) setQuery(toggleRef(view.query, kind, item));
    else setQuery(formatToken({ kind, value: item.id }));
    focusList();
  }

  function goView(e: MouseEvent, query: string): void {
    if (additive(e)) setQuery(tokenize(query).reduce((q, token) => addToken(q, token), view.query));
    else setQuery(query);
    focusList();
  }

  function viewActive(query: string): boolean {
    const saved = tokenize(query);
    return saved.length > 0 && saved.length === tokenize(view.query).length && saved.every(has);
  }

  // Clicking an item must not take keyboard focus away from the list, or
  // Space and the arrow keys would act on the sidebar button instead.
  function keepFocus(e: MouseEvent): void {
    if (!(e.target as HTMLElement).closest("input")) e.preventDefault();
  }

  // --- inline create / rename -----------------------------------------------------

  async function attempt(action: () => Promise<unknown>): Promise<void> {
    try {
      await action();
    } catch (err) {
      pushToast(errorMessage(err), { level: "error" });
    }
  }

  function edit(input: HTMLInputElement, commit: (value: string) => void) {
    input.focus();
    input.select();
    let done = false;
    const finish = (save: boolean) => {
      if (done) return;
      done = true;
      const value = input.value.trim();
      stopEditing();
      if (save && value !== "") commit(value);
    };
    const onkeydown = (e: KeyboardEvent) => {
      if (isComposing(e)) return;
      if (e.key === "Enter") finish(true);
      else if (e.key === "Escape") finish(false);
      else return;
      e.preventDefault();
      e.stopPropagation();
    };
    const onblur = () => finish(true);
    input.addEventListener("keydown", onkeydown);
    input.addEventListener("blur", onblur);
    return {
      destroy() {
        input.removeEventListener("keydown", onkeydown);
        input.removeEventListener("blur", onblur);
      },
    };
  }

  function renaming(kind: "collection" | "pack", id: string): boolean {
    return sidebarUi.renaming?.kind === kind && sidebarUi.renaming.id === id;
  }

  function rename(kind: "collection" | "pack", id: string, from: string, to: string): void {
    if (to === from) return;
    void attempt(() => (kind === "collection" ? api.renameCollection(id, to) : api.renamePack(id, to)));
  }

  function beginRename(kind: "collection" | "pack", id: string): void {
    if (libraryUsable()) startRenameItem(kind, id);
  }

  // --- resize ---------------------------------------------------------------------

  let resizeFrom: { x: number; width: number } | null = null;

  function onResizeDown(e: PointerEvent): void {
    if (e.button !== 0) return;
    e.preventDefault();
    resizeFrom = { x: e.clientX, width: settings().sidebarWidth };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    document.documentElement.dataset.sidebarResizing = "";
  }

  function onResizeMove(e: PointerEvent): void {
    if (!resizeFrom) return;
    const width = Math.round(Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, resizeFrom.width + e.clientX - resizeFrom.x)));
    if (width !== settings().sidebarWidth) previewSettings({ sidebarWidth: width });
  }

  function onResizeEnd(): void {
    if (!resizeFrom) return;
    const changed = resizeFrom.width !== settings().sidebarWidth;
    resizeFrom = null;
    delete document.documentElement.dataset.sidebarResizing;
    if (changed) void updateSettings({ sidebarWidth: settings().sidebarWidth });
  }
</script>

{#snippet count(n: number)}
  <span class="num count">{n.toLocaleString()}</span>
{/snippet}

{#snippet heading(section: SidebarSection, label: string)}
  <div class="heading">
    <button
      class="section-label toggle"
      type="button"
      aria-expanded={!sidebarUi.collapsed.has(section)}
      onclick={() => toggleSection(section)}
    >
      {label}
      <span class="chevron" class:open={!sidebarUi.collapsed.has(section)}>
        <ChevronRight size={12} strokeWidth={1.5} />
      </span>
    </button>
    {#if section === "collections" && libraryUsable()}
      <button class="add" type="button" aria-label="New collection" title="New collection" onclick={startCreateCollection}>
        <Plus size={14} strokeWidth={1.5} />
      </button>
    {/if}
  </div>
{/snippet}

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<nav class="sidebar" aria-label="Library" onmousedown={keepFocus}>
  <div class="scroll">
    <ul>
      {#each fixed as item (item.label)}
        <li>
          <button
            class="item"
            class:active={item.spec ? has(item.spec) : empty}
            type="button"
            onclick={(e) => go(e, item.spec)}
          >
            <span class="label truncate">{item.label}</span>
            {@render count(item.count)}
          </button>
        </li>
      {/each}
    </ul>

    {#if data && data.views.length > 0}
      {@render heading("views", "Views")}
      {#if !sidebarUi.collapsed.has("views")}
        <ul>
          {#each data.views as saved (saved.id)}
            <li>
              <button
                class="item"
                class:active={viewActive(saved.query)}
                type="button"
                title="{saved.name}\n{describeQuery(saved.query, data ?? undefined)}"
                onclick={(e) => goView(e, saved.query)}
                oncontextmenu={(e) => openSidebarMenu(e, { kind: "view", ...saved })}
              >
                <span class="label truncate">{saved.name}</span>
              </button>
            </li>
          {/each}
        </ul>
      {/if}
    {/if}

    {@render heading("collections", "Collections")}
    {#if !sidebarUi.collapsed.has("collections")}
      <ul>
        {#if sidebarUi.creatingCollection}
          <li>
            <input
              class="edit"
              type="text"
              placeholder="Collection name"
              aria-label="New collection name"
              spellcheck="false"
              autocomplete="off"
              use:edit={(name) => void attempt(() => api.createCollection(name))}
            />
          </li>
        {/if}
        {#each data?.collections ?? [] as collection (collection.id)}
          <li>
            {#if renaming("collection", collection.id)}
              <input
                class="edit"
                type="text"
                aria-label="Collection name"
                spellcheck="false"
                autocomplete="off"
                value={collection.name}
                use:edit={(name) => rename("collection", collection.id, collection.name, name)}
              />
            {:else}
              <button
                class="item"
                class:active={showsCollection(collection)}
                type="button"
                data-file-drop-target
                data-drop-kind="collection"
                data-drop-id={collection.id}
                onclick={(e) => goRef(e, "in", collection)}
                ondblclick={() => beginRename("collection", collection.id)}
                oncontextmenu={(e) => openSidebarMenu(e, { kind: "collection", id: collection.id, name: collection.name })}
              >
                <span class="label truncate">{collection.name}</span>
                {@render count(collection.count)}
              </button>
            {/if}
          </li>
        {:else}
          {#if !sidebarUi.creatingCollection}
            <li class="hint">No collections yet</li>
          {/if}
        {/each}
      </ul>
    {/if}

    {@render heading("packs", "Packs")}
    {#if !sidebarUi.collapsed.has("packs")}
      <ul>
        {#each data?.packs ?? [] as pack (pack.id)}
          <li>
            {#if renaming("pack", pack.id)}
              <input
                class="edit"
                type="text"
                aria-label="Pack name"
                spellcheck="false"
                autocomplete="off"
                value={pack.name}
                use:edit={(name) => rename("pack", pack.id, pack.name, name)}
              />
            {:else}
              <button
                class="item"
                class:active={showsPack(pack)}
                type="button"
                data-file-drop-target
                data-drop-kind="pack"
                data-drop-id={pack.id}
                onclick={(e) => goRef(e, "pack", pack)}
                ondblclick={() => beginRename("pack", pack.id)}
                oncontextmenu={(e) => openSidebarMenu(e, { kind: "pack", id: pack.id, name: pack.name })}
              >
                <span class="label truncate">{pack.name}</span>
                {@render count(pack.count)}
              </button>
            {/if}
          </li>
        {:else}
          <li class="hint">No packs yet</li>
        {/each}
      </ul>
    {/if}

    {@render heading("tags", "Tags")}
    {#if !sidebarUi.collapsed.has("tags")}
      <ul>
        {#each tagRows as node (node.tag)}
          <li class="tag" style:--depth={node.depth}>
            {#if node.children.length > 0}
              <button
                class="twist"
                class:open={sidebarUi.expandedTags.has(node.tag)}
                type="button"
                tabindex="-1"
                aria-label="{sidebarUi.expandedTags.has(node.tag) ? 'Collapse' : 'Expand'} {node.name}"
                onclick={() => toggleTag(node.tag)}
              >
                <ChevronRight size={12} strokeWidth={1.5} />
              </button>
            {/if}
            <button
              class="item"
              class:active={has({ kind: "tag", value: node.tag })}
              type="button"
              title={node.tag}
              aria-expanded={node.children.length > 0 ? sidebarUi.expandedTags.has(node.tag) : undefined}
              data-file-drop-target
              data-drop-kind="tag"
              data-drop-id={node.tag}
              onclick={(e) => go(e, { kind: "tag", value: node.tag })}
              oncontextmenu={(e) => openSidebarMenu(e, { kind: "tag", tag: node.tag })}
            >
              <span class="label truncate">{node.name}</span>
              {#if node.color}<span class="swatch" style:background={node.color}></span>{/if}
              {@render count(node.count)}
            </button>
          </li>
        {:else}
          <li class="hint">No tags yet</li>
        {/each}
      </ul>
    {/if}
  </div>

  <!-- The mouse's way to what otherwise needs a shortcut; each button runs
       its command, so the commands' own availability rules apply. -->
  <div class="footer">
    {#if libraryUsable()}
      <button class="foot" type="button" aria-label="Add files" title="Add files…" onclick={() => runCommand("library.addFiles")}>
        <Plus size={14} strokeWidth={1.5} />
      </button>
    {/if}
    <span class="gap"></span>
    <button
      class="foot"
      type="button"
      aria-label="Settings"
      title="Settings ({formatKeys('Mod+,')})"
      onclick={() => runCommand("overlay.settings")}
    >
      <SettingsIcon size={14} strokeWidth={1.5} />
    </button>
    <button
      class="foot"
      type="button"
      aria-label="Keyboard shortcuts"
      title="Keyboard shortcuts (?)"
      onclick={() => runCommand("overlay.shortcuts")}
    >
      <CircleQuestionMark size={14} strokeWidth={1.5} />
    </button>
  </div>

  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="resizer"
    role="separator"
    aria-orientation="vertical"
    aria-label="Resize sidebar"
    onpointerdown={onResizeDown}
    onpointermove={onResizeMove}
    onpointerup={onResizeEnd}
    onpointercancel={onResizeEnd}
    ondblclick={() => updateSettings({ sidebarWidth: DEFAULT_WIDTH })}
  ></div>
</nav>

<style>
  .sidebar {
    position: relative;
    display: flex;
    flex-direction: column;
    /* Keep the content at its width while the column animates closed. */
    width: var(--sidebar-width);
    height: 100%;
  }

  .scroll {
    flex: 1;
    min-height: 0;
    overflow-x: hidden;
    overflow-y: auto;
    /* Room at the bottom for the job pill, which floats over this corner. */
    padding: var(--space-3) var(--space-3) calc(28px + var(--space-6));
  }

  .footer {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    flex: none;
    height: var(--sidebar-footer-height);
    padding: 0 var(--space-3);
    border-top: 1px solid var(--border);
  }

  .gap {
    flex: 1;
  }

  .foot {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    border-radius: var(--radius-control);
    color: var(--text-3);
    transition:
      background var(--dur-fast) var(--ease),
      color var(--dur-fast) var(--ease);
  }

  .foot:hover {
    background: var(--bg-2);
    color: var(--text-1);
  }

  .heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: 24px;
    margin-top: var(--space-4);
    padding: 0 var(--space-2) 0 var(--space-3);
  }

  .toggle {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    height: 100%;
  }

  .toggle:hover {
    color: var(--text-2);
  }

  .chevron {
    display: grid;
    opacity: 0;
    transition: transform var(--dur-fast) var(--ease);
  }

  .chevron.open {
    transform: rotate(90deg);
  }

  .heading:hover .chevron,
  .toggle[aria-expanded="false"] .chevron,
  .toggle:focus-visible .chevron {
    opacity: 1;
  }

  .add {
    display: grid;
    place-items: center;
    width: 20px;
    height: 20px;
    border-radius: var(--radius-chip);
    color: var(--text-3);
    opacity: 0;
  }

  .heading:hover .add,
  .add:focus-visible {
    opacity: 1;
  }

  .add:hover {
    background: var(--bg-2);
    color: var(--text-1);
  }

  li {
    position: relative;
  }

  .item {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    width: 100%;
    height: 28px;
    padding: 0 var(--space-3);
    border-radius: var(--radius-control);
    color: var(--text-2);
    text-align: left;
  }

  .item:hover {
    background: var(--bg-2);
  }

  .item.active {
    background: var(--bg-3);
    color: var(--text-1);
    font-weight: 500;
  }

  .label {
    flex: 1;
    min-width: 0;
  }

  .count {
    flex: none;
    color: var(--text-3);
    font-size: var(--text-meta);
    font-weight: 400;
  }

  .tag .item {
    padding-left: calc(var(--space-3) + 14px + var(--depth) * 12px);
  }

  .twist {
    position: absolute;
    top: 6px;
    left: calc(var(--space-2) + var(--depth) * 12px);
    z-index: 1;
    display: grid;
    place-items: center;
    width: 16px;
    height: 16px;
    border-radius: var(--radius-chip);
    color: var(--text-3);
    transition: transform var(--dur-fast) var(--ease);
  }

  .twist:hover {
    color: var(--text-1);
  }

  .twist.open {
    transform: rotate(90deg);
  }

  .swatch {
    flex: none;
    width: 6px;
    height: 6px;
    border-radius: 50%;
  }

  .edit {
    width: 100%;
    height: 28px;
    padding: 0 7px;
    border: 1px solid var(--accent);
    border-radius: var(--radius-control);
    background: var(--bg-0);
  }

  .hint {
    padding: var(--space-2) var(--space-3);
    color: var(--text-3);
    font-size: var(--text-meta);
  }

  .resizer {
    position: absolute;
    top: 0;
    right: 0;
    bottom: 0;
    width: 5px;
    cursor: col-resize;
    touch-action: none;
  }

  .resizer:hover,
  :global(html[data-sidebar-resizing]) .resizer {
    background: linear-gradient(to left, var(--border) 2px, transparent 2px);
  }

  /* The column follows the pointer directly while it is being dragged. */
  :global(html[data-sidebar-resizing] .app) {
    transition: none;
  }

  :global(html[data-sidebar-resizing]) {
    cursor: col-resize;
  }
</style>
