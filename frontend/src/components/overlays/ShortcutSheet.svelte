<!-- ?: every shortcut, straight from the command registry so it cannot drift. -->
<script lang="ts">
  import { all, formatKeys } from "../../lib/commands";
  import { closeOverlay } from "../../lib/state.svelte";
  import Surface from "./Surface.svelte";

  interface Line {
    title: string;
    keys: string[];
  }

  const FALLBACK_SECTION = "General";

  /**
   * Folds a numbered run ("Toggle pinned tag 1" on 1 ... "9" on 9) into one
   * line, "Toggle pinned tag 1–9".
   */
  function foldRuns(lines: Line[]): Line[] {
    const out: (Line & { stem?: string; last?: string })[] = [];
    for (const line of lines) {
      const m = /^(.*\D)(\d)$/.exec(line.title);
      const numbered = m && line.keys.length === 1 && line.keys[0] === m[2];
      const previous = out.at(-1);
      if (numbered && previous?.stem === m[1] && Number(previous.last) + 1 === Number(m[2])) {
        const first = previous.keys[0].split("–")[0];
        previous.title = `${m[1]}${first}–${m[2]}`;
        previous.keys = [`${first}–${m[2]}`];
        previous.last = m[2];
      } else {
        out.push(numbered ? { ...line, stem: m[1], last: m[2] } : line);
      }
    }
    return out;
  }

  const sections = (() => {
    const groups = new Map<string, Line[]>();
    for (const command of all()) {
      if (!command.keys || command.sheet === false) continue;
      const keys =
        typeof command.sheet === "string"
          ? [command.sheet]
          : (Array.isArray(command.keys) ? command.keys : [command.keys]).map((k) => formatKeys(k));
      const name = command.section ?? FALLBACK_SECTION;
      const lines = groups.get(name) ?? [];
      lines.push({ title: command.title, keys });
      groups.set(name, lines);
    }
    return [...groups].map(([name, lines]) => ({ name, lines: foldRuns(lines) }));
  })();

  const close = () => closeOverlay("shortcuts");
</script>

<Surface label="Keyboard shortcuts" placement="center" onclose={close} width={680}>
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div class="sheet" tabindex="0" data-autofocus>
    <h2>Keyboard shortcuts</h2>
    <div class="columns">
      {#each sections as section (section.name)}
        <section>
          <h3 class="section-label">{section.name}</h3>
          <dl>
            {#each section.lines as line, i (i)}
              <div class="line">
                <dt class="truncate">{line.title}</dt>
                <dd>
                  {#each line.keys as key, k (k)}
                    {#if k > 0}<span class="or">or</span>{/if}
                    <kbd class="ov-kbd">{key}</kbd>
                  {/each}
                </dd>
              </div>
            {/each}
          </dl>
        </section>
      {/each}
    </div>
  </div>
</Surface>

<style>
  .sheet {
    padding: var(--space-5) var(--space-6) var(--space-6);
    overflow-y: auto;
  }

  .sheet:focus-visible {
    outline: none;
  }

  h2 {
    margin-bottom: var(--space-4);
    font-size: var(--text-omnibar);
  }

  .columns {
    columns: 2;
    column-gap: var(--space-7);
  }

  section {
    margin-bottom: var(--space-5);
    break-inside: avoid;
  }

  h3 {
    margin-bottom: var(--space-2);
  }

  dl {
    margin: 0;
  }

  .line {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-4);
    min-height: 26px;
  }

  dt {
    min-width: 0;
    color: var(--text-2);
  }

  dd {
    display: flex;
    flex: none;
    align-items: center;
    gap: var(--space-2);
    margin: 0;
  }

  .or {
    color: var(--text-3);
    font-size: var(--text-label);
  }

  @media (max-width: 620px) {
    .columns {
      columns: 1;
    }
  }
</style>
