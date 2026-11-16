// Smoke test: walks the core flows of the app in headless Chromium against
// the in-browser mock backend, in dark and light, and fails on any broken
// step or page error.
//
//   npm run smoke -- [options]
//
// Needs Chromium: `chromium` on PATH, or its path in $CHROMIUM.
//
//   --shots <dir>     save a screenshot of every step under <dir>/<scheme>/
//   --scheme dark     run one colour scheme only (default: dark, then light)
//   --n 20000         number of mock samples
//   --only <text>     run only the steps whose name contains <text>
//
// Chromium is taken from $CHROMIUM, else `chromium` on PATH.

import { mkdir } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import { launch } from "./cdp.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");

function option(name, fallback) {
  const at = process.argv.indexOf(`--${name}`);
  return at > 0 ? process.argv[at + 1] : fallback;
}

const shotsDir = option("shots", null);
const schemes = option("scheme", null) ? [option("scheme")] : ["dark", "light"];
const sampleCount = Number(option("n", 20000));
const only = option("only", "");

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/** Page-side handles on the app's own modules, installed once as window.__s. */
const HANDLES = `(async () => {
  const state = await import("/src/lib/state.svelte.ts");
  const { api } = await import("/src/lib/api.ts");
  const controller = await import("/src/lib/list/controller.svelte.ts");
  const audio = await import("/src/lib/audio/index.ts");
  const raf = () => new Promise((r) => requestAnimationFrame(r));
  window.__s = {
    state, api, controller, audio, raf,
    viewport: () => document.querySelector('[role="listbox"][aria-multiselectable="true"]'),
    rows: () => [...document.querySelectorAll('[role="option"][data-index]')],
    row: (i) => document.querySelector('[role="option"][data-index="' + i + '"]'),
    selected: () => [...state.selection.ids],
    details: (ids) => api.getSamples(ids),
    surface: () => document.querySelector("[data-surface]"),
    text: (sel) => document.querySelector(sel)?.textContent?.replace(/\\s+/g, " ").trim() ?? null,
    /** Scrolls the list by \`step\` px per frame for \`frames\` frames; returns the frame times. */
    async fling(step, frames) {
      const el = this.viewport();
      const times = [];
      let last = performance.now();
      for (let i = 0; i < frames; i++) {
        el.scrollTop += step;
        await raf();
        const now = performance.now();
        times.push(now - last);
        last = now;
      }
      return times;
    },
    /** Milliseconds from changing the query to the frame that shows its result. */
    async queryToPaint(query) {
      const before = state.view.revision;
      const seen = controller.list.version;
      const t0 = performance.now();
      state.setQuery(query);
      while (controller.list.version === seen || controller.list.slow) await raf();
      await raf();
      void before;
      return performance.now() - t0;
    },
  };
  return true;
})()`;

const server = await createServer({ root, logLevel: "error", server: { port: 5197, strictPort: false } });
await server.listen();
const base = server.resolvedUrls.local[0].replace(/\/$/, "");
const browser = await launch({ width: 1280, height: 800 });

let failed = 0;
const metrics = {};

function report(name, ok, detail) {
  console.log(`${ok ? "ok  " : "FAIL"} ${name}${detail === undefined ? "" : `  ${JSON.stringify(detail)}`}`);
  if (!ok) failed++;
}

function stats(times) {
  const sorted = [...times].sort((a, b) => a - b);
  const at = (q) => sorted[Math.min(sorted.length - 1, Math.floor(q * sorted.length))];
  const round = (v) => Math.round(v * 10) / 10;
  return { median: round(at(0.5)), p95: round(at(0.95)), max: round(sorted.at(-1)) };
}

async function open(page, query) {
  await page.goto(`${base}/?mock&${query}`);
  await page.waitFor("document.querySelector('#app')?.childElementCount > 0");
  await page.evaluateStable(HANDLES);
}

async function runScheme(scheme) {
  const page = await browser.newPage();
  await page.setColorScheme(scheme);
  const dir = shotsDir ? join(resolve(shotsDir), scheme) : null;
  if (dir) await mkdir(dir, { recursive: true });
  let shotNo = 0;

  const shot = async (name) => {
    if (!dir) return;
    // Let hover/popover transitions (120ms) settle.
    await sleep(200);
    await page.screenshot(join(dir, `${String(++shotNo).padStart(2, "0")}-${name}.png`));
  };
  const js = (expression) => page.evaluate(expression);
  const until = (expression, timeout = 8000) => page.waitFor(expression, { timeout });
  const check = (name, ok, detail) => report(`[${scheme}] ${name}`, Boolean(ok), detail);

  /** Runs one step; a throw fails the step and the walk carries on from a clean state. */
  const step = async (name, fn) => {
    if (only && !name.includes(only)) return;
    try {
      await fn();
    } catch (err) {
      check(name, false, { threw: String(err.message ?? err) });
      await shot(`FAILED-${name.replace(/\W+/g, "-")}`);
    }
    for (let i = 0; i < 3; i++) await page.press("Escape");
    await js("window.__s && (__s.state.setQuery(''), __s.state.overlays.stack.length = 0, true)");
  };

  const clickRow = (index, options) => page.click(`[role="option"][data-index="${index}"] .title`, options);
  const focusId = () => js("__s.state.selection.focusSample?.id ?? null");

  // --- first run ------------------------------------------------------------------

  await step("first run -> create library", async () => {
    await open(page, "state=none");
    await until("__s.text('[data-surface] h1') !== null");
    check("first run shows the library question", (await js("__s.text('[data-surface] h1')")).includes("samples live"));
    check("first run offers a default folder", (await js("document.querySelector('#first-run-path').value")) !== "");
    await shot("first-run");
    await page.click("[data-surface] button[type=submit]");
    await until("__s.state.app.state.status === 'ok'");
    check("create library leaves first run", await js("document.querySelector('#first-run-path') === null"));
    await until("__s.controller.list.ready && __s.state.sidebar.data.all === 0");
    check("a new library starts empty", (await js("__s.controller.list.total")) === 0);
    await until("__s.state.isOverlayOpen('splice') && document.querySelectorAll('[data-surface] [role=option]').length > 0");
    await shot("first-run-splice-offer");
    await page.press("Escape");
    check("escape closes the splice review", !(await js("__s.state.isOverlayOpen('splice')")));
    check("the empty library invites a drop", (await js("__s.text('.empty .lead')")) === "Drop samples or folders here");
    await js("window.__ssssMock.drop(['/home/you/Downloads/First Pack'])");
    await until("__s.controller.list.total > 0 && __s.row(0)?.querySelector('.name') != null", 30000);
    check("the first import fills the empty list", true);
    await until("__s.state.jobs.list.every((j) => j.finished)", 60000);
    await shot("first-import");
  });

  // --- empty library ----------------------------------------------------------------

  await step("empty state", async () => {
    await open(page, "state=empty");
    await until("__s.controller.list.ready");
    check("empty library invites a drop", (await js("__s.text('.empty .lead')")) === "Drop samples or folders here");
    check("empty library offers both imports", (await js("document.querySelectorAll('.empty .btn').length")) === 2);
    await shot("empty-library");
  });

  // --- the main walk ------------------------------------------------------------------

  await open(page, `n=${sampleCount}`);
  await until("__s.controller.list.ready && __s.rows().length > 0 && __s.row(0)?.querySelector('.name')");
  const total = await js("__s.controller.list.total");

  await step("list renders", async () => {
    check("list holds every sample", total === sampleCount, { total });
    const mounted = await js("__s.rows().length");
    check("only a window of rows is mounted", mounted > 10 && mounted < 80, { mounted });
    check("sidebar lists packs and tags", await js("document.querySelectorAll('nav [data-drop-kind=pack]').length > 0 && document.querySelectorAll('nav [data-drop-kind=tag]').length > 0"));
    check("no horizontal overflow", await js("document.documentElement.scrollWidth <= window.innerWidth"));
    await shot("list");
  });

  await step("scroll to the end and back", async () => {
    const height = await js("__s.controller.rowHeight()");
    const down = await js(`__s.fling(${height * 9}, 240)`);
    metrics[`${scheme} fling frame ms`] = stats(down.slice(5));
    await js("__s.viewport().scrollTop = __s.viewport().scrollHeight");
    await until(`__s.row(${total - 1})?.querySelector('.name') != null`);
    check("last row is reachable", true);
    await shot("list-end");
    await js(`__s.viewport().scrollTop = ${Math.floor(total / 2) * height}`);
    const t0 = Date.now();
    await until(`__s.row(${Math.floor(total / 2) + 3})?.querySelector('.name') != null`);
    metrics[`${scheme} jump-to-middle fill ms`] = Date.now() - t0;
    await js("__s.viewport().scrollTop = 0");
    await until("__s.row(0)?.querySelector('.name') != null");
    check("first row is back after scrolling home", true);
    const mounted = await js("__s.rows().length");
    check("mounted rows stay bounded after scrolling", mounted < 80, { mounted });
  });

  await step("arrow-key auditioning", async () => {
    await clickRow(0);
    await until("__s.state.selection.focusIndex === 0");
    for (let i = 0; i < 5; i++) await page.press("ArrowDown");
    await until("__s.state.selection.focusIndex === 5");
    check("arrows move the focus and the selection together", (await js("__s.selected().length")) === 1);
    await until("__s.audio.player.id === __s.state.selection.focusSample.id && (__s.audio.player.playing || __s.audio.player.loading)");
    check("the focused sample is auditioned", true);
    check("player bar names the focused sample", (await js("__s.text('footer.player .name')")).startsWith(await js("__s.state.selection.focusSample.name")));
    await until("__s.audio.player.playing || !__s.audio.player.loading");
    await shot("auditioning");
    await page.press("Shift+ArrowDown");
    await page.press("Shift+ArrowDown");
    await until("__s.selected().length === 3");
    check("shift+arrows extend the selection", true);
    await page.press("Space");
    await until("!__s.audio.player.playing");
    check("space stops playback", true);
    await page.press("End");
    await until(`__s.state.selection.focusIndex === ${total - 1}`);
    await page.press("Home");
    await until("__s.state.selection.focusIndex === 0");
    check("home/end jump across unloaded pages", true);
    await page.press("Space");
  });

  await step("search", async () => {
    await page.press("/");
    check("slash focuses the omnibar", await js("document.activeElement?.id === 'omnibar-input'"));
    await page.type("kick");
    await until(`__s.state.view.query === 'kick' && __s.controller.list.total < ${total} && __s.controller.list.total > 0`);
    check("free text narrows the list", true, { total: await js("__s.controller.list.total") });
    check("free text sorts by relevance", (await js("__s.state.view.sort")) === "relevance");
    // Plain letters must type, not trigger shortcuts.
    await page.type(" l");
    check("letters typed in the omnibar are not shortcuts", (await js("__s.state.playback.loopOverride")) === null);
    await shot("search-text");
    await page.press("Backspace");
    await page.type("#sn");
    await until("document.querySelectorAll('#omnibar-suggestions li').length > 0");
    await shot("search-suggestions");
    await page.press("Enter");
    await until("/tag:|#/.test(__s.state.view.query)");
    check("a completed tag becomes a chip", (await js("document.querySelectorAll('.omnibar .token').length")) === 1);
    await shot("search-chip");
    await page.press("Escape");
    await until("__s.state.view.query === ''");
    check("escape clears the query", true);
    await js("__s.state.setQuery('zzzzqqqq')");
    await until("__s.text('.empty .lead')?.startsWith('Nothing matches')");
    await shot("no-results");
    await page.click(".empty .link");
    await until(`__s.controller.list.total === ${total}`);
    check("clear filters restores the list", true);
  });

  await step("facets", async () => {
    await page.click('.omnibar button[aria-label="Filters"]');
    await until("document.querySelector('.facets') !== null");
    const counts = {};
    const expect = async (name, token) => {
      await until(`__s.state.view.query.includes(${JSON.stringify(token)})`);
      await until("!__s.controller.list.slow");
      counts[name] = await js("__s.controller.list.total");
      check(`facet ${name} writes ${token}`, counts[name] < total, { total: counts[name] });
    };
    await page.click('.facets [aria-label="Type"] button:first-child');
    await expect("type", "type:loop");
    await page.click('.facets input[aria-label="Minimum BPM"]');
    await page.type("120");
    await page.press("Tab");
    await page.type("130");
    await page.press("Enter");
    await expect("bpm", "bpm:120-130");
    await page.click(".facets [data-facet='Key'] > button");
    await until("document.querySelector('.facets .keycell') !== null");
    await shot("facet-key-menu");
    await page.click(".facets .keyrow:nth-child(2) .keycell:nth-child(10)");
    await expect("key", "key:");
    await page.press("Escape");
    await shot("facets");
    await page.click(".facets .clear");
    await until("__s.state.view.query === ''");
    await page.click(".facets [data-facet='Tags'] > button");
    await until("document.querySelector('.facets [data-facet=Tags] [role=option]') !== null");
    await shot("facet-tags-menu");
    await page.click(".facets [data-facet='Tags'] [role=option]");
    await expect("tags", "tag:");
    await page.press("Escape");
    await page.click(".facets .clear");
    await page.click(".facets [data-facet='Pack'] > button");
    await until("document.querySelector('.facets [data-facet=Pack] [role=option]') !== null");
    await page.click(".facets [data-facet='Pack'] [role=option]");
    await expect("pack", "pack:");
    await page.click(".facets .clear");
    await page.click('.facets [aria-label="Length"] button:first-child');
    await expect("length", "len:");
    await page.click(".facets .clear");
    await until("__s.state.view.query === ''");
    await page.click('.omnibar button[aria-label="Filters"]');
    check("filter strip closes again", await js("document.querySelector('.facets') === null"));
  });

  await step("tag one and many", async () => {
    await clickRow(2);
    await page.press("Space");
    const one = await focusId();
    await page.press("t");
    await until("__s.state.isOverlayOpen('tags') && document.activeElement?.closest('[data-surface]') !== null");
    await page.type("smoke/one");
    await shot("tag-popover-create");
    await page.press("Enter");
    await until(`__s.details([${JSON.stringify(one)}]).then((d) => d[0].tags.includes('smoke/one'))`);
    check("T tags the focused sample", true);
    check("tag popover stays open for the next tag", await js("__s.state.isOverlayOpen('tags')"));
    await page.press("Escape");
    check("escape closes the tag popover only", !(await js("__s.state.isOverlayOpen('tags')")) && (await focusId()) === one);
    check("focus returns to the list", await js("document.activeElement === __s.viewport()"));

    await clickRow(4);
    await clickRow(9, { modifiers: ["Shift"] });
    await until("__s.selected().length === 6");
    const many = await js("__s.selected()");
    await page.press("t");
    await until("__s.state.isOverlayOpen('tags')");
    await page.type("smoke/many");
    await page.press("Enter");
    await until(`__s.details(${JSON.stringify(many)}).then((d) => d.every((s) => s.tags.includes('smoke/many')))`);
    check("T tags the whole selection", true);
    await shot("tag-popover-many");
    await page.press("Escape");
    await until("[...document.querySelectorAll('nav [data-drop-kind=tag]')].some((el) => el.dataset.dropId === 'smoke')");
    check("new tag appears in the sidebar", true);
    await page.press("Mod+z");
    await until(`__s.details(${JSON.stringify(many)}).then((d) => d.every((s) => !s.tags.includes('smoke/many')))`);
    check("undo removes the tag from all of them", true);
  });

  await step("collections", async () => {
    await clickRow(1);
    await clickRow(3, { modifiers: ["Shift"] });
    await until("__s.selected().length === 3");
    await page.press("c");
    await until("__s.state.isOverlayOpen('collections')");
    await page.type("Smoke set");
    await page.press("Enter");
    await until("__s.state.sidebar.data.collections.some((c) => c.name === 'Smoke set' && c.count === 3)");
    check("C creates a collection holding the selection", true);
    await shot("collection-popover");
    await page.press("Escape");
    const id = await js("__s.state.sidebar.data.collections.find((c) => c.name === 'Smoke set').id");
    await page.click(`nav [data-drop-kind="collection"][data-drop-id="${id}"]`);
    await until("__s.controller.list.total === 3");
    check("sidebar collection filters the list", (await js("__s.state.view.query")).startsWith("in:"));
    await shot("collection-view");
    await page.click("nav .item");
    await until(`__s.controller.list.total === ${total}`);
    check("Library clears the filter", (await js("__s.state.view.query")) === "");
  });

  await step("favourites", async () => {
    await clickRow(6);
    await page.press("Space");
    const id = await focusId();
    const before = await js("__s.state.sidebar.data.favorites");
    const was = await js("__s.state.selection.focusSample.fav");
    await page.press("f");
    await until(`__s.state.selection.focusSample.fav === ${!was}`);
    await until(`__s.state.sidebar.data.favorites === ${before + (was ? -1 : 1)}`);
    check("F toggles the favourite and the sidebar count", true);
    check("the row shows its heart", (await js("__s.row(6).querySelector('.fav').classList.contains('on')")) === !was);
    await page.press("f");
    await until(`__s.state.selection.focusSample.fav === ${was}`);
    check("F toggles back", (await focusId()) === id);
  });

  await step("rename", async () => {
    await clickRow(3);
    await page.press("Space");
    const id = await focusId();
    await page.press("Enter");
    await until("document.activeElement?.classList.contains('rename')");
    await shot("rename");
    await page.press("Mod+a");
    await page.type("Smoke Renamed");
    await page.press("Enter");
    await until(`__s.details([${JSON.stringify(id)}]).then((d) => d[0].name === 'Smoke Renamed')`);
    await until("__s.text('[data-index=\"3\"] .name')?.startsWith('Smoke Renamed')");
    check("Enter renames inline", true);
    check("focus is back on the list after rename", await js("document.activeElement === __s.viewport()"));
    await page.press("Mod+z");
    await until(`__s.details([${JSON.stringify(id)}]).then((d) => d[0].name !== 'Smoke Renamed')`);
    check("undo restores the name", true);
  });

  await step("remove and undo", async () => {
    await js("__s.state.setQuery('type:loop')");
    await until(`__s.controller.list.total < ${total} && __s.row(1)?.querySelector('.name') != null`);
    const loops = await js("__s.controller.list.total");
    await clickRow(1);
    const id = await focusId();
    await until(`__s.state.playback.playingId === ${JSON.stringify(id)}`);
    await page.press("Delete");
    await until(`__s.controller.list.total === ${loops - 1}`);
    check("Delete removes the sample", (await focusId()) !== id);
    check("a removed sample stops sounding", (await js("__s.state.playback.playingId")) !== id);
    await js("__s.state.setQuery('')");
    await until(`__s.controller.list.total === ${total - 1}`);
    await clickRow(5);
    await page.press("Space");
    await page.press("Delete");
    await until(`__s.controller.list.total === ${total - 2}`);
    await until("[...document.querySelectorAll('.toast .action')].some((b) => b.textContent.trim() === 'Undo')");
    check("edits share one toast", (await js("document.querySelectorAll('.toast').length")) === 1);
    await shot("removed-toast");
    await page.click(".toast .action");
    await until(`__s.controller.list.total === ${total - 1}`);
    check("the toast's Undo brings it back", true);
    await page.press("Mod+Shift+z");
    await until(`__s.controller.list.total === ${total - 2}`);
    await page.press("Mod+z");
    await page.press("Mod+z");
    await until(`__s.controller.list.total === ${total}`);
    check("redo and undo shortcuts work", true);
  });

  await step("context menu", async () => {
    await clickRow(2, { button: "right" });
    await until("__s.state.isOverlayOpen('context')");
    check("right-click selects the row", (await js("__s.state.selection.focusIndex")) === 2);
    await shot("context-menu");
    await page.press("Escape");
    check("escape closes the menu", !(await js("__s.state.isOverlayOpen('context')")));
  });

  await step("command palette", async () => {
    await clickRow(0);
    await page.press("Space");
    await page.press("Mod+k");
    await until("__s.state.isOverlayOpen('palette')");
    await shot("palette");
    await page.type("settings");
    await until("__s.text('[data-surface] [role=option][aria-selected=true] .title') === 'Settings'");
    await shot("palette-filtered");
    await page.press("Enter");
    await until("__s.state.isOverlayOpen('settings') && !__s.state.isOverlayOpen('palette')");
    check("palette runs a command", true);
  });

  await step("settings and theme", async () => {
    await page.press("Mod+,");
    await until("__s.state.isOverlayOpen('settings')");
    await shot("settings");
    const other = scheme === "dark" ? "light" : "dark";
    await page.click(`[data-surface] input[name=theme][value=${other}] + *, [data-surface] label:has(input[name=theme][value=${other}])`);
    await until(`document.documentElement.dataset.theme === '${other}'`);
    const bg = await js("getComputedStyle(document.body).backgroundColor");
    check("theme switch repaints", other === "light" ? bg === "rgb(255, 255, 255)" : bg === "rgb(17, 17, 19)", { bg });
    await shot(`settings-${other}`);
    await page.click("[data-surface] label:has(input[name=density][value=compact])");
    await until("document.documentElement.dataset.density === 'compact'");
    await page.press("Escape");
    check("escape closes settings", !(await js("__s.state.isOverlayOpen('settings')")));
    check("compact rows are 28px", (await js("__s.row(0).getBoundingClientRect().height")) === 28);
    await shot(`compact-${other}`);
    await js("__s.state.updateSettings({ theme: 'system', density: 'regular' })");
    await until("document.documentElement.dataset.theme === undefined");
  });

  await step("import progress pill", async () => {
    await js("window.__ssssMock.drop(['/home/you/Downloads/Dusty Breaks A', '/home/you/Downloads/Dusty Breaks B', '/home/you/Downloads/Dusty Breaks C'])");
    await until("document.querySelector('.pill[aria-haspopup]') !== null");
    check("an import shows the progress pill", true);
    await shot("import-pill");
    await page.click(".pill[aria-haspopup]");
    await until("__s.surface() !== null");
    await shot("import-details");
    await page.press("Escape");
    await until("__s.state.jobs.list.every((j) => j.finished)", 60000);
    await until("[...document.querySelectorAll('.toast')].some((t) => /added/.test(t.textContent))");
    check("the import ends with a summary toast", true);
    await until(`__s.controller.list.total > ${total}`);
    check("imported samples appear in the list", true, { total: await js("__s.controller.list.total") });
    await shot("import-done");
    await until("document.querySelector('.pill[aria-haspopup]') === null", 30000);
    check("the pill goes away", true);
  });

  await step("file drop feedback", async () => {
    // Wails marks the hovered drop target with this class; so does the test.
    await js("document.body.classList.add('file-drop-target-active')");
    await until("__s.text('.label-host .label') === 'Add to library'");
    await shot("drop-window");
    await js("document.body.classList.remove('file-drop-target-active')");
    await js("document.querySelector('nav [data-drop-kind=tag]').classList.add('file-drop-target-active')");
    await until("__s.text('.label-host .label')?.includes('tag ')");
    check("a sidebar target names itself while files hover it", true);
    await shot("drop-tag");
    await js("document.querySelector('nav [data-drop-kind=tag]').classList.remove('file-drop-target-active')");
    await until("__s.text('.label-host .label') === null");
    const id = await js("__s.state.sidebar.data.collections[0].id");
    const before = await js("__s.state.sidebar.data.collections[0].count");
    await js(`window.__ssssMock.drop(['/home/you/Downloads/Drop_Kick_01.wav'], 'collection', ${JSON.stringify(id)})`);
    await until(`__s.state.sidebar.data.collections[0].count === ${before + 1}`, 30000);
    check("a drop on a collection imports into it", true);
    await until("__s.state.jobs.list.every((j) => j.finished)", 30000);
  });

  await step("rename survives rows moving", async () => {
    await clickRow(5);
    const id = await focusId();
    await page.press("Enter");
    await until("document.querySelector('input.rename') !== null");
    await page.type("My_New_");
    // An import puts rows above the one being renamed, several times over.
    await js("window.__ssssMock.drop(['/home/you/Downloads/Rename Shift A', '/home/you/Downloads/Rename Shift B'])");
    await until("__s.state.selection.focusIndex > 5 && document.activeElement?.classList.contains('rename')", 30000);
    check("the rename follows its sample", (await focusId()) === id && (await js("__s.controller.list.renamingId")) === id);
    check("the typed text is kept", (await js("document.querySelector('input.rename').value")) === "My_New_");
    await until("__s.state.jobs.list.every((j) => j.finished)", 60000);
    await until("document.activeElement?.classList.contains('rename')");
    await page.type("Name");
    await page.press("Enter");
    await until(`__s.details([${JSON.stringify(id)}]).then((d) => d[0].name === 'My_New_Name')`);
    check("Enter applies what was typed before and after", true);
    await page.press("Mod+z");

    // Scrolled far out of the mounted window, the field stays and keeps the keyboard.
    // (Following the field as its row moved has scrolled the list.)
    await js("__s.viewport().scrollTop = 0");
    await until("__s.row(2)?.querySelector('.name') != null");
    await clickRow(2);
    await page.press("Enter");
    await until("document.querySelector('input.rename') !== null");
    await js("__s.viewport().scrollTop = 40000");
    await until("__s.row(2) !== null && __s.rows().length > 20 && __s.controller.list.scrollTop === 40000");
    check("the renaming row stays mounted when scrolled away", await js("document.activeElement === document.querySelector('input.rename')"));
    await page.press("Escape");
    await until("__s.controller.list.renamingId === null");
    await page.press("ArrowDown");
    check("the list has the keyboard back", (await js("__s.state.selection.focusIndex")) === 3);
    await js("__s.viewport().scrollTop = 0");
  });

  await step("pack and collection filters", async () => {
    const pack = await js("(({ id, name, count }) => ({ id, name, count }))(__s.state.sidebar.data.packs[1])");
    await js(`__s.api.renamePack(${JSON.stringify(pack.id)}, '12" Vinyl Breaks')`);
    await until(`__s.state.sidebar.data.packs.some((p) => p.name === '12" Vinyl Breaks')`);
    await page.click(`nav [data-drop-kind=pack][data-drop-id="${pack.id}"]`);
    await until(`__s.controller.list.total === ${pack.count}`);
    check("a pack with a quote in its name filters to its samples", true);
    check("the sidebar highlights it", await js(`document.querySelector('nav [data-drop-kind=pack][data-drop-id="${pack.id}"]').classList.contains('active')`));
    check("the chip shows its name", (await js("__s.text('.omnibar .token .truncate')")) === '@12" Vinyl Breaks');
    await shot("pack-quote");
    // Typed by name it is the same filter, and the sidebar still knows it.
    await js(`__s.state.setQuery('pack:"12\\\\" Vinyl Breaks"')`);
    await until(`__s.controller.list.total === ${pack.count}`);
    check("the typed name selects the same pack", await js(`document.querySelector('nav [data-drop-kind=pack][data-drop-id="${pack.id}"]').classList.contains('active')`));
    await js("__s.state.setQuery('')");
    await page.press("Mod+z");
    await until(`__s.state.sidebar.data.packs.some((p) => p.name === ${JSON.stringify(pack.name)})`);
  });

  await step("removing in a collection view", async () => {
    const col = await js("(({ id, name, count }) => ({ id, name, count }))(__s.state.sidebar.data.collections[0])");
    await page.click(`nav [data-drop-kind=collection][data-drop-id="${col.id}"]`);
    await until(`__s.controller.list.total === ${col.count} && __s.row(0)?.querySelector('.name') != null`);
    const library = await js("__s.state.sidebar.data.all");
    await clickRow(0, { button: "right" });
    await until("__s.state.isOverlayOpen('context')");
    const labels = await js("[...document.querySelectorAll('[data-surface] [role=menuitem] .truncate')].map((e) => e.textContent)");
    check("the row menu names both removals", labels.includes(`Remove from “${col.name}”`) && labels.includes("Remove from library"), labels);
    await shot("collection-menu");
    await js(`[...document.querySelectorAll('[data-surface] [role=menuitem]')].find((e) => e.textContent.includes('Remove from “')).click()`);
    await until(`__s.state.sidebar.data.collections[0].count === ${col.count - 1}`);
    check("removing from the collection keeps the sample in the library", (await js("__s.state.sidebar.data.all")) === library);
    check("the toast says where it was removed from", await js(`[...document.querySelectorAll('.toast .message')].some((m) => m.textContent.trim().endsWith('from “${col.name}”'))`));
    await page.press("Mod+z");
    await until(`__s.state.sidebar.data.collections[0].count === ${col.count}`);
    await clickRow(0);
    await page.press("Space");
    await page.press("Delete");
    await until(`__s.state.sidebar.data.all === ${library - 1}`);
    check("Delete says it removes from the library", await js("[...document.querySelectorAll('.toast .message')].some((m) => m.textContent.trim().endsWith('from library'))"));
    await page.press("Mod+z");
    await until(`__s.state.sidebar.data.all === ${library}`);
  });

  await step("own drag-out is not an import", async () => {
    await js("__s.controller.list.dragging = true");
    await js("document.body.classList.add('file-drop-target-active')");
    await sleep(100);
    check("no import ring or label over the list", (await js("document.querySelector('.ring') === null && __s.text('.label-host .label') === null")) === true);
    check("it is not reported as an incoming drop", (await js("__s.state.drop.active")) === false);
    await js("document.body.classList.remove('file-drop-target-active')");
    await js("document.querySelector('nav [data-drop-kind=pack]').classList.add('file-drop-target-active')");
    await sleep(100);
    check("a pack shows no drop target", await js("getComputedStyle(document.querySelector('nav [data-drop-kind=pack]')).outlineStyle === 'none' && __s.text('.label-host .label') === null"));
    await js("document.querySelector('nav [data-drop-kind=pack]').classList.remove('file-drop-target-active')");
    await js("document.querySelector('nav [data-drop-kind=tag]').classList.add('file-drop-target-active')");
    await until("__s.text('.label-host .label')?.startsWith('Tag with ')");
    check("a tag says what the drop does", true);
    await shot("self-drag-tag");
    // The class may outlive the drag by a moment; the wording must not flip to "Add to library".
    await js("__s.controller.list.dragging = false");
    await sleep(100);
    check("the label holds until the hover ends", (await js("__s.text('.label-host .label')")).startsWith("Tag with "));
    await js("document.querySelector('nav [data-drop-kind=tag]').classList.remove('file-drop-target-active')");
    await until("__s.text('.label-host .label') === null");
  });

  await step("save view", async () => {
    await js("__s.state.setQuery('type:loop bpm:120-130')");
    await page.press("Mod+s");
    await until("__s.state.isOverlayOpen('rename') && document.querySelector('#rename-field') !== null");
    check("saving a view offers a readable name", (await js("document.querySelector('#rename-field').value")) === "Loops · 120-130 BPM");
    await shot("save-view");
    await page.press("Enter");
    await until("__s.state.sidebar.data.views.some((v) => v.name === 'Loops · 120-130 BPM' && v.query === 'type:loop bpm:120-130')");
    check("Enter saves it under that name", true);
    const id = await js("__s.state.sidebar.data.views.find((v) => v.name === 'Loops · 120-130 BPM').id");
    await js(`__s.api.deleteView(${JSON.stringify(id)})`);
  });

  await step("tag colour and long tag paths", async () => {
    const tag = await js("document.querySelector('nav [data-drop-kind=tag]').dataset.dropId");
    await js("document.querySelector('nav [data-drop-kind=tag]').scrollIntoView({ block: 'center' })");
    await page.click(`nav [data-drop-kind=tag][data-drop-id="${tag}"]`, { button: "right" });
    await until("__s.state.isOverlayOpen('context') && document.querySelector('[data-surface] .swatches') !== null");
    await shot("tag-menu");
    const colour = `__s.state.sidebar.data.tags.find((t) => t.tag === ${JSON.stringify(tag)}).color`;
    const before = await js(colour);
    await page.click('[data-surface] .swatch[aria-label="Blue"]');
    await until(`${colour} === '#3e83dd'`);
    check("a tag colour can be picked from its menu", await js(`document.querySelector('nav [data-drop-kind=tag][data-drop-id="${tag}"] .swatch') !== null`));
    await page.press("Mod+z");
    await until(`${colour} === ${JSON.stringify(before)}`);

    // Two tags that differ only in their last segment, far past the popover's width.
    const stem = "an-extremely-long-tag-name/with-a-nested-child-that-keeps-going";
    await clickRow(0);
    await page.press("Space");
    const id = await focusId();
    await js(`__s.api.addTags([${JSON.stringify(id)}], ['${stem}/first', '${stem}/second'])`);
    await until(`__s.state.sidebar.data.tags.some((t) => t.tag === '${stem}/second')`);
    await page.press("t");
    await until("__s.state.isOverlayOpen('tags') && document.querySelector('[data-surface] [role=option]') !== null");
    await page.type("extremely");
    await until("document.querySelectorAll('[data-surface] [role=option] .leaf').length >= 2");
    const leaves = await js("[...document.querySelectorAll('[data-surface] [role=option] .leaf')].map((e) => ({ text: e.textContent, cut: (() => { const r = document.createRange(); r.selectNodeContents(e); return r.getBoundingClientRect().width > e.getBoundingClientRect().width + 0.01; })() }))");
    check("sibling tags stay distinguishable in the tag picker", leaves.some((l) => l.text === "/first" && !l.cut) && leaves.some((l) => l.text === "/second" && !l.cut), leaves);
    await shot("tag-popover-long");
    await page.press("Escape");
    await page.press("Mod+z");
  });

  await step("mouse entry points", async () => {
    await page.click('nav .footer button[aria-label="Settings"]');
    await until("__s.state.isOverlayOpen('settings')");
    check("the sidebar footer opens settings", true);
    await page.press("Escape");
    await page.click('nav .footer button[aria-label="Keyboard shortcuts"]');
    await until("__s.state.isOverlayOpen('shortcuts')");
    check("the sidebar footer opens the shortcut sheet", true);
    await shot("sidebar-footer");
    await page.press("Escape");
    check("no commands button while the sidebar shows", await js("document.querySelector('.omnibar button[aria-label=Commands]') === null"));
  });

  await step("splice import", async () => {
    await page.press("Mod+k");
    await until("__s.state.isOverlayOpen('palette')");
    await page.type("splice");
    await page.press("Enter");
    await until("__s.state.isOverlayOpen('splice') && document.querySelectorAll('[data-surface] [role=option]').length > 0");
    await shot("splice-import");
    await page.press("Space");
    await page.press("ArrowDown");
    check("splice list is keyboard driven", await js("document.querySelector('[data-surface] [role=option]:nth-child(2)').dataset.active === 'true'"));
    await page.press("Escape");
    check("escape closes the splice review", !(await js("__s.state.isOverlayOpen('splice')")));
  });

  await step("info popover", async () => {
    await clickRow(1);
    await page.press("Space");
    await page.press("Mod+i");
    await until("__s.state.isOverlayOpen('info') && document.querySelector('#info-bpm') !== null");
    await shot("info");
    const id = await focusId();
    await page.click("#info-bpm");
    await page.press("Mod+a");
    await page.type("133");
    await page.press("Enter");
    await until(`__s.details([${JSON.stringify(id)}]).then((d) => d[0].bpm === 133 && d[0].bpmSrc === 'user')`);
    check("info edits the BPM as a user value", true);
    await page.press("Escape");
    check("escape closes info", !(await js("__s.state.isOverlayOpen('info')")));
    await page.press("Mod+z");
  });

  await step("shortcut sheet", async () => {
    await clickRow(0);
    await page.press("Space");
    await page.press("?");
    await until("__s.state.isOverlayOpen('shortcuts')");
    await shot("shortcuts");
    await page.press("Escape");
    check("escape closes the shortcut sheet", !(await js("__s.state.isOverlayOpen('shortcuts')")));
  });

  await step("playback keys", async () => {
    await clickRow(0);
    await page.press("m");
    await until("__s.state.settings().matchTempo && __s.state.settings().matchKey");
    await page.press("b");
    await until("__s.state.isOverlayOpen('project')");
    await page.press("Mod+a");
    await page.type("128 dm");
    await page.press("Enter");
    await until("__s.state.settings().projectBpm === 128 && __s.state.settings().projectKey === 'Dm'");
    check("B edits the project tempo and key", true);
    await page.press("]");
    await until("__s.state.settings().projectBpm === 129");
    await page.press("l");
    check("L overrides the loop default", (await js("__s.state.playback.loopOverride")) !== null);
    await page.press("l");
    await shot("player-matched");
    await page.click('footer.player button[aria-label="Loop"]');
    check("clicking a player button leaves the keyboard on the list", await js("document.activeElement === __s.viewport()"));
    await page.click('footer.player button[aria-label="Loop"]');
    await page.press("m");
    await page.press("Space");
  });

  await step("query to paint", async () => {
    const times = [];
    for (const q of ["type:loop", "snare", "bpm:120-128 key:Am", "", "is:fav", "#drums", ""]) {
      times.push(await js(`__s.queryToPaint(${JSON.stringify(q)})`));
    }
    metrics[`${scheme} query-to-paint ms`] = stats(times);
    check("queries paint quickly", Math.max(...times) < 400, stats(times));
  });

  await step("narrow window", async () => {
    await page.setDeviceScale(800, 520, 1);
    await until("__s.state.app.narrow");
    await sleep(250);
    check("sidebar hides below 900px", await js("document.querySelector('aside.sidebar').getBoundingClientRect().width === 0"));
    check("no horizontal overflow when narrow", await js("document.documentElement.scrollWidth <= window.innerWidth"));
    await clickRow(1);
    await shot("narrow");
    await js(`__s.state.setQuery('kick #drums bpm:120-130 key:Am -#bass pack:"Afro House Drums" len:<2')`);
    await until("document.querySelectorAll('.omnibar .token').length === 6");
    await sleep(100);
    const chips = await js("[...document.querySelectorAll('.omnibar .token')].map((t) => ({ text: t.textContent.trim(), long: t.classList.contains('long'), cut: t.firstElementChild.scrollWidth > t.firstElementChild.clientWidth }))");
    check("short filter chips stay readable in a narrow window", chips.filter((c) => !c.long).every((c) => !c.cut), chips);
    check("the omnibar still fits", await js("document.querySelector('.omnibar').scrollWidth <= document.querySelector('.omnibar').clientWidth && document.querySelector('.omnibar input').getBoundingClientRect().width >= 60"));
    await shot("narrow-chips");
    await js("__s.state.setQuery('')");
    await page.click('.omnibar button[aria-label="Commands"]');
    await until("__s.state.isOverlayOpen('palette')");
    check("with the sidebar hidden the omnibar offers the commands", true);
    await page.press("Escape");
    await page.press("Mod+\\");
    await until("[...document.querySelectorAll('.toast')].some((t) => /sidebar is hidden/.test(t.textContent))");
    check("toggling the hidden sidebar leaves the setting alone", await js("__s.state.settings().sidebarOpen"));
    await page.click('.omnibar button[aria-label="Filters"]');
    await until("document.querySelector('.facets') !== null");
    check("facet strip fits the narrow window", await js("document.querySelector('.facets').scrollWidth <= document.querySelector('.facets').clientWidth + 1"));
    await shot("narrow-facets");
    await page.click('.omnibar button[aria-label="Filters"]');
    await page.press("Mod+,");
    await until("__s.state.isOverlayOpen('settings')");
    await shot("narrow-settings");
    await page.press("Escape");
    await page.press("Mod+k");
    await until("__s.state.isOverlayOpen('palette')");
    await shot("narrow-palette");
    await page.press("Escape");
    await page.press("Space");
    await page.setDeviceScale(1280, 800, 1);
  });

  await step("offline and read-only", async () => {
    await open(page, "state=unavailable");
    await until("__s.controller.list.ready && document.querySelector('.banner') !== null");
    check("offline banner offers Retry and Locate", (await js("document.querySelectorAll('.banner .action').length")) === 2);
    check("play is disabled offline", await js("document.querySelector('footer.player .play').disabled"));
    await shot("offline");
    await open(page, "state=readonly");
    await until("__s.controller.list.ready && document.querySelector('.banner') !== null");
    await shot("readonly");
  });

  check("no page errors", page.errors.length === 0, page.errors.length ? { errors: page.errors.slice(0, 8) } : undefined);
}

try {
  for (const scheme of schemes) await runScheme(scheme);
} catch (err) {
  report("smoke run reached the end", false, { threw: String(err?.stack ?? err) });
} finally {
  await browser.close();
  await server.close();
}

console.log("\nmeasurements");
for (const [name, value] of Object.entries(metrics)) console.log(`  ${name}: ${JSON.stringify(value)}`);
console.log(failed ? `\n${failed} check(s) failed` : "\nall checks passed");
process.exit(failed ? 1 : 0);
