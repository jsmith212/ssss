<!--
  First run: pick the folder the library lives in. One of the two modal
  surfaces in the app; it cannot be dismissed, only completed.
-->
<script lang="ts">
  import { api } from "../../lib/api";
  import { app, errorMessage, openOverlay } from "../../lib/state.svelte";
  import { baseName } from "./format";
  import Surface from "./Surface.svelte";

  type Mode = "create" | "open";

  let path = $state(app.state?.defaultLibraryPath ?? "");
  // What the primary button does. It starts as "create" and follows what the
  // backend says about the folder: there is no way to look before trying.
  let mode = $state<Mode>("create");
  let hint = $state("");
  let error = $state("");
  let busy = $state(false);
  let spliceFolder = $state("");
  let alsoSplice = $state(true);

  api
    .detectSplice()
    .then((info) => (spliceFolder = info.found ? info.folder : ""))
    .catch(() => {
      // No Splice offer, nothing else lost.
    });

  const labels: Record<Mode, string> = { create: "Create library", open: "Open existing library" };
  const other = $derived<Mode>(mode === "create" ? "open" : "create");

  // The backend's own words for "wrong action for this folder" (library.ErrExists,
  // library.ErrNotLibrary and OpenLibrary's missing-folder error). They are
  // matched whole: any other failure carries the folder's path and the OS's
  // wording, and must be shown as it is rather than guessed at.
  const EXISTS = "folder already contains a library";
  const NOT_A_LIBRARY = "folder does not contain a library";
  const NOT_FOUND = "folder not found: ";

  async function submit(chosen: Mode): Promise<void> {
    const folder = path.trim();
    if (busy) return;
    if (!folder) {
      error = "Choose a folder for the library.";
      return;
    }
    busy = true;
    error = "";
    hint = "";
    try {
      if (chosen === "create") await api.createLibrary(folder, baseName(folder) || "Main");
      else await api.openLibrary(folder);
      // The "state" event flips the status and this screen goes away.
      if (spliceFolder && alsoSplice) openOverlay("splice");
    } catch (err) {
      const message = errorMessage(err);
      // Offer the action that fits what was found there instead of a dead end.
      if (chosen === "create" && message === EXISTS) {
        mode = "open";
        hint = "This folder already holds a library.";
      } else if (chosen === "open" && (message === NOT_A_LIBRARY || message.startsWith(NOT_FOUND))) {
        mode = "create";
        hint = "There is no library in this folder yet.";
      } else {
        error = message;
      }
    } finally {
      busy = false;
    }
  }

  async function choose(): Promise<void> {
    try {
      const folder = await api.chooseFolder("Choose a folder for your sample library");
      if (!folder) return;
      path = folder;
      mode = "create";
      hint = "";
      error = "";
    } catch (err) {
      error = errorMessage(err);
    }
  }
</script>

<Surface label="Set up your library" placement="screen" modal dismissable={false} exclusive onclose={() => {}}>
  <form
    class="card"
    onsubmit={(e) => {
      e.preventDefault();
      void submit(mode);
    }}
  >
    <h1>Where should your samples live?</h1>
    <p class="lead">
      ssss copies samples into one folder and keeps its tags and collections there too. A synced folder works, so the
      library can follow you between machines.
    </p>

    <label class="section-label" for="first-run-path">Library folder</label>
    <div class="path-row">
      <input
        id="first-run-path"
        class="ov-field path"
        type="text"
        data-autofocus
        autocomplete="off"
        spellcheck="false"
        aria-describedby="first-run-status"
        aria-invalid={error ? true : undefined}
        bind:value={path}
        oninput={() => {
          hint = "";
          error = "";
        }}
      />
      <button type="button" class="btn" onclick={choose}>Choose folder…</button>
    </div>
    <p id="first-run-status" class="status" class:error={error !== ""} role={error ? "alert" : "status"}>
      {error || hint || " "}
    </p>

    {#if spliceFolder}
      <label class="check">
        <input type="checkbox" bind:checked={alsoSplice} />
        <span>Also import Splice</span>
        <span class="where truncate" title={spliceFolder}>{spliceFolder}</span>
      </label>
    {/if}

    <div class="actions">
      <button type="submit" class="btn btn-primary" disabled={busy}>{labels[mode]}</button>
      <button type="button" class="btn btn-ghost" disabled={busy} onclick={() => void submit(other)}>
        {labels[other]}
      </button>
    </div>
  </form>
</Surface>

<style>
  .card {
    display: flex;
    flex-direction: column;
    width: min(520px, calc(100vw - 48px));
  }

  h1 {
    font-size: 20px;
    letter-spacing: -0.01em;
  }

  .lead {
    margin: var(--space-3) 0 var(--space-6);
    color: var(--text-2);
    line-height: 1.5;
  }

  .path-row {
    display: flex;
    gap: var(--space-3);
    margin-top: var(--space-3);
  }

  .path {
    flex: 1;
    min-width: 0;
    height: 32px;
  }

  .path-row .btn {
    flex: none;
    height: 32px;
  }

  .status {
    min-height: 18px;
    margin-top: var(--space-3);
    color: var(--text-2);
    font-size: var(--text-meta);
    white-space: pre-wrap;
  }

  .status.error {
    color: var(--danger);
  }

  .check {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    margin-top: var(--space-3);
  }

  .check input {
    accent-color: var(--text-1);
  }

  .where {
    min-width: 0;
    color: var(--text-3);
    font-size: var(--text-meta);
  }

  .actions {
    display: flex;
    gap: var(--space-3);
    margin-top: var(--space-6);
  }

  .actions .btn {
    height: 32px;
    padding: 0 var(--space-5);
  }
</style>
