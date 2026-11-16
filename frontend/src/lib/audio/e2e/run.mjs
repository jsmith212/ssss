// End-to-end checks of the audio engine and player in headless Chromium.
//
//   node frontend/src/lib/audio/e2e/run.mjs [--shots <dir>]
//
// Needs Chromium: `chromium` on PATH, or its path in $CHROMIUM.
//
// Starts a Vite dev server, runs harness.html (engine against a synthetic
// backend) and then the real app against the mock backend. Exits non-zero
// if any check fails. With --shots, saves player bar screenshots there.

import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import { launch } from "../../../../scripts/cdp.mjs";
import { appChecks } from "./app.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../../../..");
const shotsAt = process.argv.indexOf("--shots");
const shots = shotsAt > 0 ? resolve(process.argv[shotsAt + 1]) : null;
const only = process.argv.includes("--engine") ? "engine" : process.argv.includes("--app") ? "app" : "all";

const server = await createServer({ root, logLevel: "error", server: { port: 5199, strictPort: false } });
await server.listen();
const base = server.resolvedUrls.local[0].replace(/\/$/, "");
const browser = await launch();
let failed = 0;

function report(c) {
  console.log(`${c.ok ? "ok  " : "FAIL"} ${c.name}`);
  console.log(`     ${JSON.stringify(c.detail)}`);
  if (!c.ok) failed++;
}

try {
  if (only !== "app") {
    const page = await browser.newPage();
    await page.goto(`${base}/src/lib/audio/e2e/harness.html`);
    const result = await page.evaluateStable(
      "new Promise((r) => { const poll = () => (window.__audioChecks ? r(window.__audioChecks) : setTimeout(poll, 50)); poll(); })",
    );
    result.checks.forEach(report);
    report({ name: "no page errors", ok: page.errors.length === 0, detail: { errors: page.errors } });
  }
  if (only !== "engine") {
    try {
      await appChecks(browser, base, shots, report);
    } catch (err) {
      report({ name: "app checks ran to the end", ok: false, detail: { threw: String(err) } });
    }
  }
} finally {
  await browser.close();
  await server.close();
}

console.log(failed ? `\n${failed} check(s) failed` : "\nall checks passed");
process.exit(failed ? 1 : 0);
