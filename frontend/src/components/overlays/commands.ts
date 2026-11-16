// Palette commands for things only the overlays offer.

import { api } from "../../lib/api";
import { registerAll, type Command } from "../../lib/commands";
import { app, pushToast, updateSettings } from "../../lib/state.svelte";
import type { Theme } from "../../lib/types";
import { attempt } from "./targets.svelte";

// The first-run screen owns the window until a library exists.
const ready = () => app.state != null && app.state.status !== "none";

function theme(value: Theme, title: string): Command {
  return {
    id: `theme.${value}`,
    title,
    section: "View",
    when: ready,
    run: () => updateSettings({ theme: value }),
  };
}

/**
 * Registers the commands this directory adds. The shortcuts that open the
 * overlays (T, C, Cmd+I, Cmd+K, Cmd+, and ?) are registered with the rest of
 * the keyboard map in lib/list/commands.ts.
 */
export function registerOverlayCommands(): () => void {
  return registerAll([
    {
      id: "library.rebuildCache",
      title: "Rebuild cache",
      section: "Library",
      when: ready,
      run: async () => {
        if (await attempt(() => api.rebuildCache())) pushToast("Rebuilding the cache from the library folder");
      },
    },
    theme("system", "Theme: follow system"),
    theme("light", "Theme: light"),
    theme("dark", "Theme: dark"),
  ]);
}
