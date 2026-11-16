// Checks of the player inside the real app, running on the mock backend:
// the player bar, keyboard shortcuts, scrubbing and screenshots.

import { mkdir } from "node:fs/promises";
import { join } from "node:path";

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

/** Page-side helpers, installed once as window.__t. */
const HELPERS = `(async () => {
  const audio = await import("/src/lib/audio/index.ts");
  const playerModule = await import("/src/lib/audio/player.svelte.ts");
  const state = await import("/src/lib/state.svelte.ts");
  const { api } = await import("/src/lib/api.ts");
  const engine = playerModule.audioEngine();
  const analyser = engine.context.createAnalyser();
  analyser.fftSize = 8192;
  engine.output.connect(analyser);
  const loops = (await api.search({ query: "type:loop", sort: "name", desc: false, offset: 0, limit: 200 })).rows;
  const shots = (await api.search({ query: "type:oneshot", sort: "name", desc: false, offset: 0, limit: 200 })).rows;
  window.__t = {
    audio, state, api, engine,
    loop: loops.find((s) => s.bpm > 0 && s.key !== "" && s.dur > 3 && !s.missing),
    oneshot: shots.find((s) => !s.missing && s.key !== ""),
    many: [...loops, ...shots].filter((s) => !s.missing).slice(0, 50),
    level() {
      const data = new Float32Array(analyser.fftSize);
      analyser.getFloatTimeDomainData(data);
      return Math.sqrt(data.reduce((sum, v) => sum + v * v, 0) / data.length);
    },
    snapshot() {
      const p = audio.player;
      const s = state.settings();
      return {
        id: p.id, playing: p.playing, loading: p.loading, position: p.position, duration: p.duration,
        loop: p.loop, matched: p.matched, rate: p.match.rate, semitones: p.match.semitones,
        effectiveBpm: p.match.effectiveBpm, effectiveKey: p.match.effectiveKey, error: p.error,
        voices: engine.activeVoices(), enginePosition: engine.position(), contextTime: engine.context.currentTime,
        playingId: state.playback.playingId, rowsMatched: state.playback.matched,
        projectBpm: s.projectBpm, projectKey: s.projectKey, matchTempo: s.matchTempo, matchKey: s.matchKey,
        volume: s.volume, editing: audio.editingProject(),
      };
    },
    rect(selector) {
      const r = document.querySelector(selector)?.getBoundingClientRect();
      return r ? { x: r.x, y: r.y, width: r.width, height: r.height } : null;
    },
  };
  return true;
})()`;

/** Runs the checks, handing each result to `emit` as it is known. */
export async function appChecks(browser, base, shots, emit) {
  const check = (name, ok, detail) => emit({ name, ok: Boolean(ok), detail });
  const page = await browser.newPage();
  await page.setColorScheme("dark");
  await page.goto(`${base}/?mock`);
  await page.waitFor("document.querySelector('footer.player') !== null");
  await page.evaluateStable(HELPERS);
  const snap = () => page.evaluate("window.__t.snapshot()");
  const run = (js) => page.evaluate(`(async () => { const t = window.__t; ${js} })()`);

  // --- the list drives the player ------------------------------------------------
  await page.waitFor("document.querySelector('[data-index]') !== null").catch(() => {});
  const row = await page.evaluate("window.__t.rect('[data-index]')");
  if (row) {
    await page.mouse("mousePressed", row.x + 60, row.y + row.height / 2, 1);
    await page.mouse("mouseReleased", row.x + 60, row.y + row.height / 2, 0);
    await sleep(400);
    const s = await snap();
    const name = await page.evaluate("document.querySelector('footer.player .name')?.textContent ?? ''");
    check("clicking a row auditions it", s.id !== null && (s.playing || s.duration < 0.4) && name !== "", {
      id: s.id,
      playing: s.playing,
      name,
    });
  } else {
    check("clicking a row auditions it", false, { reason: "no rows rendered" });
  }

  // --- audition a loop, plain ------------------------------------------------------
  await run("t.state.updateSettings({ matchTempo: false, matchKey: false, projectBpm: 120, projectKey: '', volume: 0.8 }); t.state.playback.loopOverride = null;");
  await run("t.state.setFocus(t.loop, -1); t.audio.audition(t.loop);");
  await sleep(600);
  let a = await snap();
  const level = await page.evaluate("window.__t.level()");
  await sleep(400);
  let b = await snap();
  const sample = await page.evaluate("({ id: __t.loop.id, bpm: __t.loop.bpm, key: __t.loop.key, dur: __t.loop.dur, name: __t.loop.name })");
  check(
    "a loop from the mock backend plays and the bar follows",
    a.playing && a.id === sample.id && a.loop && !a.matched && level > 0.01 && b.position !== a.position && a.playingId === sample.id && a.voices === 1,
    { sample, level: Number(level.toFixed(4)), positions: [a.position, b.position], duration: a.duration, voices: a.voices },
  );

  // --- keyboard ---------------------------------------------------------------------
  await page.key(" ", { code: "Space" });
  await sleep(100);
  a = await snap();
  await page.key(" ", { code: "Space" });
  await sleep(200);
  b = await snap();
  check("Space stops and plays", !a.playing && a.voices === 0 && b.playing && b.voices === 1, {
    afterFirst: { playing: a.playing, voices: a.voices },
    afterSecond: { playing: b.playing, voices: b.voices },
  });

  // Clicking the play button, then Space: the focused button must not fire twice.
  const play = await page.evaluate("window.__t.rect('footer.player .play')");
  await page.mouse("mousePressed", play.x + 16, play.y + 16, 1);
  await page.mouse("mouseReleased", play.x + 16, play.y + 16, 0);
  await sleep(100);
  a = await snap();
  await page.key(" ", { code: "Space" });
  await sleep(200);
  b = await snap();
  check("play button stops; Space with the button focused plays once", !a.playing && b.playing && b.voices === 1, {
    afterClick: a.playing,
    afterSpace: b.playing,
    voices: b.voices,
  });

  await page.key("l", { code: "KeyL" });
  a = await snap();
  await page.key("l", { code: "KeyL" });
  b = await snap();
  check("L toggles loop", a.loop === false && b.loop === true && b.playing, { afterFirst: a.loop, afterSecond: b.loop });

  const before = await snap();
  await page.key("ArrowRight");
  await sleep(50);
  a = await snap();
  const jump = (((a.enginePosition - before.enginePosition) % a.duration) + a.duration) % a.duration;
  check("→ seeks forward 5%", Math.abs(jump - 0.05 * a.duration) < 0.12 && a.playing && a.voices === 1, {
    jump: Number(jump.toFixed(3)),
    expected: Number((0.05 * a.duration).toFixed(3)),
  });

  await page.key("-", { code: "Minus" });
  a = await snap();
  await page.key("=", { code: "Equal" });
  b = await snap();
  check("- and = change the volume", Math.abs(a.volume - 0.75) < 1e-9 && Math.abs(b.volume - 0.8) < 1e-9, {
    down: a.volume,
    up: b.volume,
  });

  // --- project pill -------------------------------------------------------------------
  await page.key("b", { code: "KeyB" });
  await sleep(100);
  a = await snap();
  const focused = await page.evaluate("document.activeElement?.classList.contains('field') ?? false");
  for (const ch of "999") await page.key(ch);
  await page.key("Enter");
  await sleep(50);
  const invalid = await page.evaluate("({ editing: __t.audio.editingProject(), hint: document.querySelector('.project .hint')?.textContent.trim(), bpm: __t.state.settings().projectBpm })");
  if (shots) {
    await mkdir(shots, { recursive: true });
    await page.screenshot(join(shots, "project-invalid-dark.png"), await barClip(page));
  }
  await page.evaluate("(() => { const i = document.querySelector('.project .field'); i.select(); })()");
  for (const ch of "128 am") await page.key(ch, { code: ch === " " ? "Space" : ch });
  await page.key("Enter");
  await sleep(50);
  b = await snap();
  check(
    "B edits the project; bad input is refused, “128 am” is applied",
    a.editing && focused && invalid.editing && invalid.bpm === 120 && /20–400/.test(invalid.hint) && !b.editing && b.projectBpm === 128 && b.projectKey === "Am",
    { openedWithFocus: focused, invalid, after: { editing: b.editing, bpm: b.projectBpm, key: b.projectKey } },
  );

  await page.key("]", { code: "BracketRight" });
  a = await snap();
  await page.key("[", { code: "BracketLeft" });
  await page.key("[", { code: "BracketLeft" });
  b = await snap();
  check("[ and ] nudge the project tempo", a.projectBpm === 129 && b.projectBpm === 127, { up: a.projectBpm, down: b.projectBpm });
  await page.key("]", { code: "BracketRight" });

  // --- matched preview ------------------------------------------------------------------
  await sleep(300);
  const plain = await snap();
  await page.key("m", { code: "KeyM" });
  await sleep(700);
  a = await snap();
  await sleep(1000);
  b = await snap();
  const matchedLevel = await page.evaluate("window.__t.level()");
  const speed = (((b.enginePosition - a.enginePosition) % a.duration) + a.duration) % a.duration / (b.contextTime - a.contextTime);
  const toggles = await page.evaluate("[...document.querySelectorAll('footer.player .toggle')].map((el) => ({ text: el.innerText.replace(/\\s+/g, ' '), pressed: el.getAttribute('aria-pressed'), applies: el.classList.contains('applies') }))");
  check(
    "M switches the playing loop to matched preview without stopping it",
    plain.playing && a.playing && a.matched && a.matchTempo && a.matchKey && a.rowsMatched && a.voices === 1 &&
      Math.abs(speed - a.rate) < 0.03 && matchedLevel > 0.01 && Math.abs(a.effectiveBpm - sample.bpm * a.rate) < 1e-6,
    { rate: a.rate, semitones: a.semitones, effectiveBpm: a.effectiveBpm, effectiveKey: a.effectiveKey, playheadSpeed: Number(speed.toFixed(3)), level: Number(matchedLevel.toFixed(4)), toggles },
  );

  if (shots) {
    await page.screenshot(join(shots, "player-dark.png"), await barClip(page));
    await page.screenshot(join(shots, "app-dark.png"));
    await page.setDeviceScale(1280, 800, 2);
    await sleep(300);
    await page.screenshot(join(shots, "player-dark@2x.png"), await barClip(page));
    await page.setDeviceScale(1280, 800, 1);
    await page.setColorScheme("light");
    await sleep(300);
    await page.screenshot(join(shots, "player-light.png"), await barClip(page));
    await page.screenshot(join(shots, "app-light.png"));
    // Volume popover.
    const mute = await page.evaluate("window.__t.rect('footer.player .mute')");
    await page.mouse("mouseMoved", mute.x + 14, mute.y + 14);
    await sleep(300);
    await page.screenshot(join(shots, "volume-light.png"), await barClip(page, 130));
    await page.setColorScheme("dark");
    await sleep(300);
    await page.screenshot(join(shots, "volume-dark.png"), await barClip(page, 130));
    await page.mouse("mouseMoved", 400, 300);
  }

  // --- scrubbing ---------------------------------------------------------------------------
  const wave = await page.evaluate("window.__t.rect('footer.player .waveform')");
  const y = wave.y + wave.height / 2;
  await page.mouse("mousePressed", wave.x + wave.width * 0.5, y, 1);
  await sleep(120);
  a = await snap();
  await page.mouse("mouseMoved", wave.x + wave.width * 0.6, y, 1);
  await page.mouse("mouseMoved", wave.x + wave.width * 0.75, y, 1);
  await sleep(120);
  b = await snap();
  await page.mouse("mouseReleased", wave.x + wave.width * 0.75, y, 0);
  check(
    "click and drag on the waveform scrub",
    Math.abs(a.enginePosition / a.duration - 0.5) < 0.06 && Math.abs(b.enginePosition / b.duration - 0.75) < 0.06 && b.playing && b.voices === 1,
    { afterClick: Number((a.enginePosition / a.duration).toFixed(3)), afterDrag: Number((b.enginePosition / b.duration).toFixed(3)) },
  );

  await page.key("m", { code: "KeyM" });
  await sleep(300);
  a = await snap();
  check("M again returns to plain playback", a.playing && !a.matched && !a.matchTempo && !a.matchKey && a.voices === 1, {
    playing: a.playing,
    matched: a.matched,
    voices: a.voices,
  });

  // --- a one-shot: key match only, ends by itself ---------------------------------------------
  await run("t.state.updateSettings({ matchTempo: true, matchKey: true, projectKey: 'F#m' }); t.state.setFocus(t.oneshot, -1); t.audio.audition(t.oneshot);");
  await sleep(150);
  a = await snap();
  if (shots) await page.screenshot(join(shots, "oneshot-dark.png"), await barClip(page));
  await page.waitFor("!window.__t.audio.player.playing", { timeout: 25_000 });
  b = await snap();
  check("a one-shot is key matched only and ends by itself", a.rate === 1 && !a.loop && b.voices === 0 && b.playingId === null && b.position === 0, {
    rate: a.rate,
    semitones: a.semitones,
    loop: a.loop,
    endedVoices: b.voices,
  });

  // --- rapid retriggers through the public API ---------------------------------------------------
  await run("t.audio.prefetch(t.many.slice(0, 4)); for (const s of t.many) { t.state.setFocus(s, -1); t.audio.audition(s); await new Promise((r) => setTimeout(r, 3)); }");
  await sleep(600);
  a = await snap();
  const last = await page.evaluate("__t.many.at(-1).id");
  const inFlight = await page.evaluate("__t.engine.loader.inFlight");
  check("50 rapid auditions leave one voice, the last sample", a.id === last && a.playingId === last && a.voices === 1 && inFlight.length === 0, {
    voices: a.voices,
    playing: a.playing,
    inFlight,
    cached: await page.evaluate("__t.engine.loader.cachedIds.length"),
  });

  await run("t.audio.stop(); t.state.updateSettings({ matchTempo: false, matchKey: false, projectBpm: 120, projectKey: '' });");
  await sleep(100);
  a = await snap();
  check("stop() silences", !a.playing && a.voices === 0 && a.playingId === null, { voices: a.voices });

  check("no page errors", page.errors.length === 0, { errors: page.errors });
}

/** The player bar's rectangle, extended upwards by `above` pixels. */
async function barClip(page, above = 44) {
  const r = await page.evaluate("window.__t.rect('footer.player')");
  return { x: r.x, y: r.y - above, width: r.width, height: r.height + above };
}
