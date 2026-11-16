import { defineConfig } from "vitest/config";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import wails from "@wailsio/runtime/plugins/vite";

// `wails3 dev` passes its own port and needs it to be exact; a plain
// `npm run dev` (mock backend in a browser) uses Vite's default and may move
// to the next free port so several dev servers can run side by side.
const wailsPort = Number(process.env.WAILS_VITE_PORT) || 0;

export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: wailsPort || 5173,
    strictPort: wailsPort > 0,
  },
  plugins: [svelte(), wails("./bindings")],
  test: {
    include: ["src/**/*.test.ts"],
    environment: "node",
  },
});
