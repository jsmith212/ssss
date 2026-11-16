// Minimal Chrome DevTools Protocol client for the smoke test and the audio
// end-to-end checks: launches headless Chromium and drives one page over the
// built-in WebSocket, so no browser-automation package is needed.

import { spawn } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

/**
 * Starts headless Chromium (binary from $CHROMIUM, default "chromium") with
 * audio allowed to start without a user gesture.
 */
export async function launch({ width = 1280, height = 800, args = [] } = {}) {
  const profile = await mkdtemp(join(tmpdir(), "ssss-e2e-"));
  const child = spawn(
    process.env.CHROMIUM ?? "chromium",
    [
      "--headless=new",
      "--no-sandbox",
      "--disable-gpu",
      "--hide-scrollbars",
      "--mute-audio",
      "--autoplay-policy=no-user-gesture-required",
      "--remote-debugging-port=0",
      `--user-data-dir=${profile}`,
      `--window-size=${width},${height}`,
      ...args,
      "about:blank",
    ],
    { stdio: ["ignore", "ignore", "pipe"] },
  );

  const endpoint = await new Promise((resolve, reject) => {
    let stderr = "";
    const timer = setTimeout(() => reject(new Error(`Chromium did not start:\n${stderr}`)), 30_000);
    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (chunk) => {
      stderr += chunk;
      const m = /DevTools listening on (ws:\/\/\S+)/.exec(stderr);
      if (m) {
        clearTimeout(timer);
        resolve(m[1]);
      }
    });
    child.once("error", (err) => {
      clearTimeout(timer);
      reject(err);
    });
    child.once("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`Chromium exited with ${code}:\n${stderr}`));
    });
  });

  const connection = await Connection.open(endpoint);
  return {
    async newPage() {
      const { targetId } = await connection.send("Target.createTarget", { url: "about:blank" });
      const { sessionId } = await connection.send("Target.attachToTarget", { targetId, flatten: true });
      const page = new Page(connection, sessionId);
      await page.send("Page.enable");
      await page.send("Runtime.enable");
      await page.send("Emulation.setDeviceMetricsOverride", {
        width,
        height,
        deviceScaleFactor: 1,
        mobile: false,
      });
      return page;
    },
    async close() {
      connection.close();
      child.kill();
      await new Promise((resolve) => child.once("exit", resolve));
      await rm(profile, { recursive: true, force: true, maxRetries: 3 });
    },
  };
}

const MODIFIERS = { alt: 1, ctrl: 2, mod: 2, meta: 4, shift: 8 };

const NAMED_KEYS = {
  Enter: { key: "Enter", code: "Enter", vk: 13, text: "\r" },
  Escape: { key: "Escape", code: "Escape", vk: 27 },
  Tab: { key: "Tab", code: "Tab", vk: 9 },
  Space: { key: " ", code: "Space", vk: 32, text: " " },
  Backspace: { key: "Backspace", code: "Backspace", vk: 8 },
  Delete: { key: "Delete", code: "Delete", vk: 46 },
  ArrowUp: { key: "ArrowUp", code: "ArrowUp", vk: 38 },
  ArrowDown: { key: "ArrowDown", code: "ArrowDown", vk: 40 },
  ArrowLeft: { key: "ArrowLeft", code: "ArrowLeft", vk: 37 },
  ArrowRight: { key: "ArrowRight", code: "ArrowRight", vk: 39 },
  PageUp: { key: "PageUp", code: "PageUp", vk: 33 },
  PageDown: { key: "PageDown", code: "PageDown", vk: 34 },
  Home: { key: "Home", code: "Home", vk: 36 },
  End: { key: "End", code: "End", vk: 35 },
  F2: { key: "F2", code: "F2", vk: 113 },
  ",": { key: ",", code: "Comma", vk: 188 },
  "/": { key: "/", code: "Slash", vk: 191 },
  "?": { key: "?", code: "Slash", vk: 191 },
  "\\": { key: "\\", code: "Backslash", vk: 220 },
  "[": { key: "[", code: "BracketLeft", vk: 219 },
  "]": { key: "]", code: "BracketRight", vk: 221 },
  "-": { key: "-", code: "Minus", vk: 189 },
  "=": { key: "=", code: "Equal", vk: 187 },
};

class Connection {
  static open(url) {
    return new Promise((resolve, reject) => {
      const socket = new WebSocket(url);
      socket.addEventListener("open", () => resolve(new Connection(socket)));
      socket.addEventListener("error", () => reject(new Error(`cannot connect to ${url}`)));
    });
  }

  constructor(socket) {
    this.socket = socket;
    this.nextId = 1;
    this.pending = new Map();
    this.listeners = new Set();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(event.data);
      if (message.id === undefined) {
        this.listeners.forEach((fn) => fn(message));
        return;
      }
      const waiter = this.pending.get(message.id);
      if (!waiter) return;
      this.pending.delete(message.id);
      if (message.error) waiter.reject(new Error(`${waiter.method}: ${message.error.message}`));
      else waiter.resolve(message.result);
    });
  }

  send(method, params = {}, sessionId) {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject, method });
      this.socket.send(JSON.stringify({ id, method, params, sessionId }));
    });
  }

  close() {
    this.socket.close();
  }
}

class Page {
  constructor(connection, sessionId) {
    this.connection = connection;
    this.sessionId = sessionId;
    /** Console errors and uncaught exceptions seen so far. */
    this.errors = [];
    connection.listeners.add((message) => {
      if (message.sessionId !== sessionId) return;
      if (message.method === "Runtime.exceptionThrown") {
        const d = message.params.exceptionDetails;
        this.errors.push(d.exception?.description ?? d.text);
      } else if (message.method === "Runtime.consoleAPICalled" && message.params.type === "error") {
        this.errors.push(message.params.args.map((a) => a.value ?? a.description).join(" "));
      }
    });
  }

  send(method, params) {
    return this.connection.send(method, params, this.sessionId);
  }

  async goto(url) {
    const loaded = this.once("Page.loadEventFired");
    await this.send("Page.navigate", { url });
    await loaded;
  }

  once(method) {
    return new Promise((resolve) => {
      const listener = (message) => {
        if (message.sessionId !== this.sessionId || message.method !== method) return;
        this.connection.listeners.delete(listener);
        resolve(message.params);
      };
      this.connection.listeners.add(listener);
    });
  }

  /** Evaluates an expression (promises are awaited) and returns its JSON value. */
  async evaluate(expression) {
    const { result, exceptionDetails } = await this.send("Runtime.evaluate", {
      expression,
      awaitPromise: true,
      returnByValue: true,
      userGesture: true,
    });
    if (exceptionDetails) {
      throw new Error(exceptionDetails.exception?.description ?? exceptionDetails.text);
    }
    return result.value;
  }

  /**
   * Like evaluate, but starts over if the page reloads underneath it, as a
   * dev server does once when it discovers a new dependency.
   */
  async evaluateStable(expression, { attempts = 5 } = {}) {
    for (let attempt = 1; ; attempt++) {
      try {
        return await this.evaluate(expression);
      } catch (err) {
        const reloaded = /navigated or closed|context was destroyed|Cannot find context/i.test(String(err));
        if (!reloaded || attempt >= attempts) throw err;
        await new Promise((resolve) => setTimeout(resolve, 500));
      }
    }
  }

  /** Polls until the expression is truthy; returns its value. */
  async waitFor(expression, { timeout = 15_000, interval = 50 } = {}) {
    const deadline = Date.now() + timeout;
    for (;;) {
      const value = await this.evaluateStable(expression);
      if (value) return value;
      if (Date.now() > deadline) throw new Error(`timed out waiting for: ${expression}`);
      await new Promise((resolve) => setTimeout(resolve, interval));
    }
  }

  setColorScheme(scheme) {
    return this.send("Emulation.setEmulatedMedia", {
      features: [{ name: "prefers-color-scheme", value: scheme }],
    });
  }

  setDeviceScale(width, height, deviceScaleFactor) {
    return this.send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor, mobile: false });
  }

  async key(key, { code = key, modifiers = 0 } = {}) {
    const text = key.length === 1 ? key : undefined;
    await this.send("Input.dispatchKeyEvent", { type: "keyDown", key, code, text, modifiers });
    await this.send("Input.dispatchKeyEvent", { type: "keyUp", key, code, modifiers });
  }

  /**
   * Presses a shortcut written like the app's own bindings: "Ctrl+Shift+Z",
   * "ArrowDown", "Space", "?", "t". Printable keys without Ctrl/Meta also
   * type their character, as a real keyboard would.
   */
  async press(combo) {
    const parts = combo === "+" ? ["+"] : combo.split("+");
    const name = parts.pop() || "+";
    let modifiers = 0;
    for (const part of parts) modifiers |= MODIFIERS[part.toLowerCase()] ?? 0;
    const named = NAMED_KEYS[name];
    const key = named?.key ?? name;
    const code = named?.code ?? (/^[a-z]$/i.test(key) ? `Key${key.toUpperCase()}` : /^\d$/.test(key) ? `Digit${key}` : "");
    const vk = named?.vk ?? (key.length === 1 ? key.toUpperCase().charCodeAt(0) : 0);
    const printable = key.length === 1 && (modifiers & (MODIFIERS.ctrl | MODIFIERS.meta)) === 0;
    const text = printable ? key : named?.text;
    const event = { key, code, modifiers, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk };
    await this.send("Input.dispatchKeyEvent", { type: text ? "keyDown" : "rawKeyDown", text, ...event });
    await this.send("Input.dispatchKeyEvent", { type: "keyUp", ...event });
  }

  /** Types text into the focused element. */
  type(text) {
    return this.send("Input.insertText", { text });
  }

  async mouse(type, x, y, buttons = 0) {
    await this.send("Input.dispatchMouseEvent", {
      type,
      x,
      y,
      button: type === "mouseMoved" && buttons === 0 ? "none" : "left",
      buttons,
      clickCount: type === "mouseMoved" ? 0 : 1,
    });
  }

  /** Centre of the first element matching `selector`; throws when there is none. */
  async centre(selector) {
    const point = await this.evaluate(`(() => {
      const el = document.querySelector(${JSON.stringify(selector)});
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
    })()`);
    if (!point) throw new Error(`no element matches ${selector}`);
    return point;
  }

  /** Clicks the first element matching `selector`. Options: button, modifiers ("Ctrl", "Shift"). */
  async click(selector, { button = "left", modifiers = [] } = {}) {
    const { x, y } = await this.centre(selector);
    let mask = 0;
    for (const m of modifiers) mask |= MODIFIERS[m.toLowerCase()] ?? 0;
    const buttons = button === "right" ? 2 : 1;
    const base = { x, y, modifiers: mask };
    await this.send("Input.dispatchMouseEvent", { type: "mouseMoved", ...base });
    await this.send("Input.dispatchMouseEvent", { type: "mousePressed", button, buttons, clickCount: 1, ...base });
    await this.send("Input.dispatchMouseEvent", { type: "mouseReleased", button, buttons: 0, clickCount: 1, ...base });
  }

  wheel(x, y, deltaY) {
    return this.send("Input.dispatchMouseEvent", { type: "mouseWheel", x, y, deltaX: 0, deltaY });
  }

  /** Saves a PNG of the viewport, or of `clip` ({x, y, width, height}). */
  async screenshot(path, clip) {
    const { data } = await this.send("Page.captureScreenshot", {
      format: "png",
      clip: clip ? { ...clip, scale: 1 } : undefined,
    });
    await writeFile(path, Buffer.from(data, "base64"));
  }
}
