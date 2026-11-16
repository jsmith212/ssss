<!--
  Progress of imports, analysis and rescans: a pill at the bottom left, with
  a details popover for cancel and per-file errors. Announces the result of
  an import with a toast.
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { api } from "../../lib/api";
  import { jobs, pushToast, setQuery, settings, sidebarVisible } from "../../lib/state.svelte";
  import type { JobKind, Progress } from "../../lib/types";
  import { plural } from "./format";
  import Surface from "./Surface.svelte";
  import { attempt } from "./targets.svelte";
  import { jobsUi } from "./ui.svelte";

  const VERBS: Record<JobKind, string> = {
    import: "Importing",
    splice: "Importing from Splice",
    analysis: "Analysing",
    rescan: "Scanning library",
  };
  const DONE: Record<JobKind, string> = {
    import: "Import",
    splice: "Splice import",
    analysis: "Analysis",
    rescan: "Rescan",
  };

  // Finished jobs with failures stay here until dismissed; state drops
  // every finished job after a few seconds, which is too soon to read errors.
  let failed = $state<Progress[]>([]);
  let pill = $state<HTMLElement>();

  const running = $derived(jobs.list.filter((j) => !j.finished));
  // Copying files matters more to the user than the analysis trailing it.
  const lead = $derived(running.find((j) => j.kind !== "analysis") ?? running[0]);
  const shown = $derived([...failed, ...running]);
  const failures = $derived(failed.reduce((n, j) => n + j.failed, 0));
  const sidebarShown = $derived(sidebarVisible());

  const count = (n: number) => n.toLocaleString();
  const fraction = (job: Progress) => (job.total > 0 ? Math.min(1, job.done / job.total) : 0);

  function summary(job: Progress): string {
    const parts = [`${count(job.added)} added`];
    if (job.duplicates > 0) parts.push(`${count(job.duplicates)} already in library`);
    if (job.failed > 0) parts.push(`${count(job.failed)} failed`);
    return parts.join(" · ");
  }

  function finished(job: Progress): void {
    if (job.failed > 0 || job.errors.length > 0) {
      failed = [...failed.filter((j) => j.jobId !== job.jobId), job];
    }
    if (job.kind === "analysis") return;
    const show = { label: "Show", run: () => setQuery("is:recent") };
    if (job.kind === "rescan") {
      pushToast(job.cancelled ? "Rescan cancelled" : job.added > 0 ? `Rescan found ${plural(job.added, "new sample")}` : "Rescan finished");
      return;
    }
    const text = job.cancelled ? `${DONE[job.kind]} cancelled · ${summary(job)}` : summary(job);
    pushToast(text, { action: job.added > 0 ? show : undefined, timeout: 8000 });
  }

  onMount(() => {
    const onProgress = (job: Progress) => {
      if (job.finished) finished(job);
    };
    const off = [api.on("ingest:progress", onProgress), api.on("analysis:progress", onProgress)];
    return () => off.forEach((fn) => fn());
  });

  $effect(() => {
    if (shown.length === 0) jobsUi.open = false;
  });

  function dismiss(job: Progress): void {
    failed = failed.filter((j) => j.jobId !== job.jobId);
  }
</script>

{#if shown.length > 0}
  <button
    bind:this={pill}
    class="popover pill num"
    class:raised={!sidebarShown}
    class:warn={!lead}
    style:max-width="{sidebarShown ? settings().sidebarWidth - 24 : 260}px"
    data-surface-keep
    aria-haspopup="dialog"
    aria-expanded={jobsUi.open}
    onclick={() => (jobsUi.open = !jobsUi.open)}
  >
    {#if lead}
      <span class="truncate">{VERBS[lead.kind]}</span>
      <span class="progress-text">{count(lead.done)} / {count(lead.total)}</span>
      {#if running.length > 1}<span class="more">+{running.length - 1}</span>{/if}
      <span class="track" aria-hidden="true"><span class="fill" style:width="{fraction(lead) * 100}%"></span></span>
    {:else}
      <span class="truncate">{plural(failures, "file")} failed</span>
    {/if}
    <span class="visually-hidden">. Show details</span>
  </button>
{/if}

{#if jobsUi.open && shown.length > 0}
  <Surface label="Activity" anchorTo={pill} above onclose={() => (jobsUi.open = false)} width={340}>
    <div class="jobs">
      {#each shown as job (job.jobId)}
        <section class="job">
          <header>
            <h3 class="truncate">{job.finished ? `${DONE[job.kind]} ${job.cancelled ? "cancelled" : "finished"}` : VERBS[job.kind]}</h3>
            <span class="num meta">{count(job.done)} / {count(job.total)}</span>
          </header>
          {#if !job.finished}
            <div
              class="track"
              role="progressbar"
              aria-label={VERBS[job.kind]}
              aria-valuemin="0"
              aria-valuemax={job.total}
              aria-valuenow={job.done}
            >
              <span class="fill" style:width="{fraction(job) * 100}%"></span>
            </div>
            <p class="current truncate" title={job.current}>{job.current || " "}</p>
          {/if}
          {#if job.kind !== "analysis"}
            <p class="num meta">{summary(job)}</p>
          {/if}
          {#if job.errors.length > 0}
            <ul class="errors selectable" aria-label="Files that failed">
              {#each job.errors as error, i (i)}
                <li>{error}</li>
              {/each}
            </ul>
          {/if}
          <div class="actions">
            {#if job.finished}
              <button class="btn" onclick={() => dismiss(job)}>Dismiss</button>
            {:else}
              <button class="btn" onclick={() => void attempt(() => api.cancelJob(job.jobId))}>Cancel</button>
            {/if}
          </div>
        </section>
      {/each}
    </div>
  </Surface>
{/if}

<style>
  .pill {
    position: fixed;
    /* Over the bottom of the sidebar's list, clear of its footer buttons. */
    bottom: calc(var(--sidebar-footer-height) + var(--space-3));
    left: var(--space-4);
    z-index: var(--z-popover);
    display: flex;
    align-items: center;
    gap: var(--space-3);
    height: 28px;
    padding: 0 var(--space-4);
    overflow: hidden;
    border-radius: 14px;
    color: var(--text-2);
    font-size: var(--text-meta);
    transition: color var(--dur-fast) var(--ease);
  }

  /* Without the sidebar the bottom-left corner belongs to the player bar. */
  .pill.raised {
    bottom: calc(var(--player-height) + var(--space-4));
  }

  .pill:hover {
    color: var(--text-1);
  }

  .pill.warn {
    color: var(--warning);
  }

  .progress-text {
    flex: none;
    color: var(--text-1);
  }

  .more {
    flex: none;
    color: var(--text-3);
  }

  .track {
    display: block;
    height: 3px;
    overflow: hidden;
    border-radius: 2px;
    background: var(--bg-3);
  }

  .pill .track {
    position: absolute;
    right: 0;
    bottom: 0;
    left: 0;
    height: 2px;
    border-radius: 0;
    background: none;
  }

  .fill {
    display: block;
    height: 100%;
    background: var(--text-2);
  }

  .jobs {
    max-height: min(420px, 70vh);
    overflow-y: auto;
  }

  .job {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding: var(--space-4);
  }

  .job + .job {
    border-top: 1px solid var(--border);
  }

  header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-4);
  }

  .meta,
  .current {
    color: var(--text-2);
    font-size: var(--text-meta);
  }

  .current {
    color: var(--text-3);
  }

  .errors {
    max-height: 132px;
    overflow-y: auto;
    padding: var(--space-3);
    border-radius: var(--radius-control);
    background: var(--bg-0);
    color: var(--text-2);
    font-size: var(--text-meta);
  }

  .errors li {
    overflow-wrap: anywhere;
  }

  .errors li + li {
    margin-top: var(--space-2);
  }

  .actions {
    display: flex;
    justify-content: flex-end;
  }

  .actions .btn {
    background: var(--bg-3);
  }
</style>
