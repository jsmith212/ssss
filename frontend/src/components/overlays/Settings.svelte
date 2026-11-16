<!-- Cmd+,: the settings sheet. Every change applies at once; there is no Save. -->
<script lang="ts">
  import X from "@lucide/svelte/icons/x";
  import { api } from "../../lib/api";
  import { app, closeOverlay, libraryUsable, openOverlay, pushToast, settings, sidebar, updateSettings } from "../../lib/state.svelte";
  import type { Density, LibraryStatus, Theme } from "../../lib/types";
  import { normaliseTag } from "./format";
  import Surface from "./Surface.svelte";
  import { attempt, report } from "./targets.svelte";

  const THEMES: { value: Theme; label: string }[] = [
    { value: "system", label: "System" },
    { value: "light", label: "Light" },
    { value: "dark", label: "Dark" },
  ];
  const DENSITIES: { value: Density; label: string }[] = [
    { value: "regular", label: "Regular" },
    { value: "compact", label: "Compact" },
  ];
  const STATUS: Record<LibraryStatus, string> = {
    ok: "",
    none: "No library is open.",
    unavailable: "The folder can’t be reached right now.",
    readonly: "Open read-only.",
  };
  const SLOTS = 9;

  const lib = $derived(app.state);
  const pinned = $derived(Array.from({ length: SLOTS }, (_, i) => settings().pinnedTags[i] ?? ""));

  let spliceDetected = $state("");
  api
    .detectSplice()
    .then((info) => (spliceDetected = info.found ? info.folder : ""))
    .catch(() => {
      // The field just shows no detected folder.
    });

  const close = () => closeOverlay("settings");

  async function locate(): Promise<void> {
    try {
      const folder = await api.chooseFolder("Locate your library folder");
      if (!folder) return;
      await api.openLibrary(folder);
      pushToast(`Library opened from ${folder}`);
    } catch (err) {
      report(err);
    }
  }

  async function chooseSplice(): Promise<void> {
    try {
      const folder = await api.chooseFolder("Choose your Splice folder");
      if (folder) await updateSettings({ spliceFolder: folder });
    } catch (err) {
      report(err);
    }
  }

  function pin(slot: number, value: string): void {
    const next = [...pinned];
    next[slot] = normaliseTag(value);
    // Slots are positional; only trailing empties are dropped.
    while (next.length > 0 && next[next.length - 1] === "") next.pop();
    void updateSettings({ pinnedTags: next });
  }

  async function rescan(): Promise<void> {
    if (await attempt(() => api.rescan())) close();
  }

  async function rebuild(): Promise<void> {
    if (await attempt(() => api.rebuildCache())) pushToast("Rebuilding the cache from the library folder");
  }
</script>

<Surface label="Settings" placement="center" modal onclose={close} width={580}>
  <header class="head">
    <h2>Settings</h2>
    <button class="close" aria-label="Close settings" onclick={close}><X size={16} strokeWidth={1.5} aria-hidden="true" /></button>
  </header>

  <!-- Focus starts on the sheet itself, so no control is ringed before the user reaches for one. -->
  <div class="body" tabindex="-1" data-autofocus>
    <section aria-labelledby="set-library">
      <h3 class="section-label" id="set-library">Library</h3>
      <div class="row">
        <div class="grow">
          <p class="value selectable truncate" title={lib?.libraryPath}>{lib?.libraryPath || "Not set"}</p>
          <p class="help">
            {#if lib && STATUS[lib.status]}<span class="warn">{lib.message || STATUS[lib.status]}</span>{:else}{lib?.libraryName}{/if}
          </p>
        </div>
        <button class="btn" onclick={locate}>Locate…</button>
      </div>
      <p class="help">
        To move the library, quit ssss, move the whole folder with your file manager, then use Locate to point at its
        new place. Tags, collections and packs travel inside the folder.
      </p>
    </section>

    <section aria-labelledby="set-splice">
      <h3 class="section-label" id="set-splice">Splice</h3>
      <div class="row">
        <div class="grow">
          <p class="value selectable truncate" title={settings().spliceFolder || spliceDetected}>
            {settings().spliceFolder || spliceDetected || "No Splice folder found"}
          </p>
          <p class="help">{settings().spliceFolder ? "Chosen by you" : spliceDetected ? "Detected automatically" : " "}</p>
        </div>
        <button class="btn" onclick={chooseSplice}>Choose…</button>
        <button
          class="btn"
          disabled={!libraryUsable()}
          onclick={() => {
            close();
            openOverlay("splice");
          }}
        >
          Import…
        </button>
      </div>
    </section>

    <section aria-labelledby="set-look">
      <h3 class="section-label" id="set-look">Appearance</h3>
      <div class="row">
        <span class="grow" id="set-theme">Theme</span>
        <div class="segments" role="radiogroup" aria-labelledby="set-theme">
          {#each THEMES as option (option.value)}
            <label class:on={settings().theme === option.value}>
              <input
                class="visually-hidden"
                type="radio"
                name="theme"
                value={option.value}
                checked={settings().theme === option.value}
                onchange={() => void updateSettings({ theme: option.value })}
              />
              {option.label}
            </label>
          {/each}
        </div>
      </div>
      <div class="row">
        <span class="grow" id="set-density">Row density</span>
        <div class="segments" role="radiogroup" aria-labelledby="set-density">
          {#each DENSITIES as option (option.value)}
            <label class:on={settings().density === option.value}>
              <input
                class="visually-hidden"
                type="radio"
                name="density"
                value={option.value}
                checked={settings().density === option.value}
                onchange={() => void updateSettings({ density: option.value })}
              />
              {option.label}
            </label>
          {/each}
        </div>
      </div>
    </section>

    <section aria-labelledby="set-play">
      <h3 class="section-label" id="set-play">Playback</h3>
      <label class="row">
        <span class="grow">
          Play on select
          <span class="help block">Samples play as you move through the list.</span>
        </span>
        <input
          class="switch"
          type="checkbox"
          role="switch"
          checked={settings().playOnSelect}
          onchange={(e) => void updateSettings({ playOnSelect: e.currentTarget.checked })}
        />
      </label>
    </section>

    <section aria-labelledby="set-pins">
      <h3 class="section-label" id="set-pins">Pinned tags</h3>
      <p class="help">Keys 1–9 toggle these tags on the selection.</p>
      <datalist id="set-tags">
        {#each sidebar.data?.tags ?? [] as tag (tag.tag)}<option value={tag.tag}></option>{/each}
      </datalist>
      <div class="pins">
        {#each pinned as tag, slot (slot)}
          <label class="pin">
            <kbd class="ov-kbd">{slot + 1}</kbd>
            <input
              class="ov-field"
              type="text"
              list="set-tags"
              autocomplete="off"
              spellcheck="false"
              placeholder="–"
              aria-label="Pinned tag {slot + 1}"
              value={tag}
              onchange={(e) => pin(slot, e.currentTarget.value)}
            />
          </label>
        {/each}
      </div>
    </section>

    <section aria-labelledby="set-maint">
      <h3 class="section-label" id="set-maint">Maintenance</h3>
      <div class="row">
        <span class="grow">
          Rescan library
          <span class="help block">Picks up files added, moved or removed outside ssss.</span>
        </span>
        <button class="btn" disabled={!libraryUsable()} onclick={rescan}>Rescan</button>
      </div>
      <div class="row">
        <span class="grow">
          Rebuild cache
          <span class="help block">Rebuilds this machine’s search index from the library folder. Nothing in the library changes.</span>
        </span>
        <button class="btn" disabled={lib?.status === "none"} onclick={rebuild}>Rebuild</button>
      </div>
    </section>
  </div>
</Surface>

<style>
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--space-4) var(--space-4) var(--space-4) var(--space-6);
    border-bottom: 1px solid var(--border);
  }

  h2 {
    font-size: var(--text-omnibar);
  }

  .close {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    border-radius: var(--radius-control);
    color: var(--text-2);
  }

  .close:hover {
    background: var(--bg-3);
    color: var(--text-1);
  }

  .body {
    display: flex;
    flex-direction: column;
    gap: var(--space-6);
    min-height: 0;
    padding: var(--space-5) var(--space-6) var(--space-6);
    overflow-y: auto;
  }

  .body:focus-visible {
    outline: none;
  }

  section {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .row {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    min-height: 32px;
  }

  .grow {
    flex: 1;
    min-width: 0;
  }

  .help {
    color: var(--text-2);
    font-size: var(--text-meta);
    line-height: 1.45;
  }

  .help.block {
    display: block;
  }

  .warn {
    color: var(--warning);
  }

  .row .btn,
  .segments {
    flex: none;
  }

  .row .btn {
    background: var(--bg-3);
  }

  .segments {
    display: flex;
    padding: 2px;
    border-radius: var(--radius-control);
    background: var(--bg-0);
  }

  .segments label {
    position: relative;
    display: grid;
    place-items: center;
    min-width: 72px;
    height: 24px;
    padding: 0 var(--space-4);
    border-radius: var(--radius-chip);
    color: var(--text-2);
    transition: background var(--dur-fast) var(--ease);
  }

  .segments label.on {
    background: var(--bg-3);
    color: var(--text-1);
    font-weight: 500;
  }

  .segments label:has(:focus-visible) {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }

  .switch {
    position: relative;
    flex: none;
    width: 32px;
    height: 18px;
    margin: 0;
    border-radius: 9px;
    background: var(--text-3);
    appearance: none;
    transition: background var(--dur-fast) var(--ease);
  }

  .switch::after {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    background: var(--bg-0);
    content: "";
    transition: translate var(--dur-fast) var(--ease);
  }

  .switch:checked {
    background: var(--text-1);
  }

  .switch:checked::after {
    translate: 14px 0;
  }

  .switch:focus-visible {
    outline-offset: 2px;
    border-radius: 9px;
  }

  .pins {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: var(--space-3);
  }

  .pin {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }

  .pin input {
    flex: 1;
    min-width: 0;
  }
</style>
