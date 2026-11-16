// What an overlay acts on, and the sample details it needs to show state.

import { untrack } from "svelte";
import { api } from "../../lib/api";
import { errorMessage, pushToast, selectedIds, selection, view } from "../../lib/state.svelte";
import type { SampleDetail } from "../../lib/types";

/** The selection, or the focus row when nothing is selected. */
export function targetIds(): string[] {
  const ids = selectedIds();
  if (ids.length > 0) return ids;
  return selection.focusSample ? [selection.focusSample.id] : [];
}

/** The single sample an inspector should describe: the focus row if it is part of the target. */
export function primaryTargetId(): string | null {
  const focus = selection.focusSample?.id;
  const ids = selectedIds();
  if (focus && (ids.length === 0 || ids.includes(focus))) return focus;
  return ids[0] ?? null;
}

/** Reports a failed backend call the way every overlay does: one error toast. */
export function report(err: unknown): void {
  pushToast(errorMessage(err), { level: "error" });
}

/** Runs a backend call and reports its failure; true when it succeeded. */
export async function attempt(call: () => Promise<unknown>): Promise<boolean> {
  try {
    await call();
    return true;
  } catch (err) {
    report(err);
    return false;
  }
}

/**
 * Tri-state pickers look at every target to tell "all" from "some". Past
 * this many the first ones stand in for the rest; toggling still applies to
 * the whole selection.
 */
export const DETAIL_LIMIT = 2000;

/**
 * Details of `ids`, refetched whenever the backend reports a change. Call
 * during component initialisation.
 */
export function useDetails(ids: () => string[]): { readonly list: SampleDetail[]; readonly loaded: boolean } {
  let list = $state.raw<SampleDetail[]>([]);
  let loaded = $state(false);

  $effect(() => {
    void view.revision;
    const wanted = ids().slice(0, DETAIL_LIMIT);
    let stale = false;
    api
      .getSamples(wanted)
      .then((details) => {
        if (stale) return;
        list = details;
        loaded = true;
      })
      .catch(report);
    return () => {
      stale = true;
    };
  });

  return {
    get list() {
      return list;
    },
    get loaded() {
      return loaded;
    },
  };
}

export type CheckState = "on" | "off" | "mixed";

/** How many of `total` targets carry each value, as a tri-state per value. */
export function checkStates(values: Iterable<string>[], total: number): Map<string, CheckState> {
  const counts = new Map<string, number>();
  for (const set of values) for (const v of set) counts.set(v, (counts.get(v) ?? 0) + 1);
  const out = new Map<string, CheckState>();
  for (const [value, n] of counts) out.set(value, n >= total ? "on" : "mixed");
  return out;
}

/**
 * Tri-state bookkeeping for a picker: the state the backend reports, the
 * optimistic state of toggles still in flight, and what was applied when the
 * picker opened (rows keep that order instead of jumping as they are toggled).
 */
export function useChecks(
  details: { readonly list: SampleDetail[]; readonly loaded: boolean },
  pick: (detail: SampleDetail) => string[],
) {
  const states = $derived(checkStates(details.list.map(pick), details.list.length));
  let pending = $state<Record<string, CheckState>>({});
  let atOpen = $state.raw<Set<string> | null>(null);

  $effect(() => {
    const real = states;
    untrack(() => {
      for (const key of Object.keys(pending)) {
        if ((real.get(key) ?? "off") === pending[key]) delete pending[key];
      }
      if (details.loaded && !atOpen) atOpen = new Set(real.keys());
    });
  });

  return {
    stateOf: (key: string): CheckState => pending[key] ?? states.get(key) ?? "off",
    appliedAtOpen: (key: string): boolean => atOpen?.has(key) ?? false,
    /** Every value some target carries, or is about to. */
    keys: (): string[] => [...new Set([...states.keys(), ...Object.keys(pending)])],
    /** Shows the new state at once and reverts it if the call fails. */
    async apply(key: string, on: boolean, call: () => Promise<unknown>): Promise<boolean> {
      pending[key] = on ? "on" : "off";
      const ok = await attempt(call);
      if (!ok) delete pending[key];
      return ok;
    },
  };
}

/** Sort rank for picker rows: applied at open, then recently used, then the rest. */
export function pickerRank(key: string, applied: boolean, recents: string[]): number {
  if (applied) return 0;
  const r = recents.indexOf(key);
  return r >= 0 ? 1 + r : 1 + recents.length;
}

const RECENT_LIMIT = 12;

/** Most recently used values (tags, collection ids) per kind, newest first; kept per machine. */
export function recent(kind: string): string[] {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(`ssss.recent.${kind}`) ?? "[]");
    return Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === "string") : [];
  } catch {
    return [];
  }
}

export function rememberRecent(kind: string, value: string): void {
  const next = [value, ...recent(kind).filter((v) => v !== value)].slice(0, RECENT_LIMIT);
  try {
    localStorage.setItem(`ssss.recent.${kind}`, JSON.stringify(next));
  } catch {
    // Storage can be unavailable (private mode); recents are a nicety.
  }
}
