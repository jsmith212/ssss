// Canvas drawing needs resolved colours, and those change with the theme.
// `theme.epoch` moves whenever they may have, so drawing effects rerun.

export const theme = $state({ epoch: 0 });

const cache = new Map<string, string>();

/** The current value of a colour token, e.g. cssColor("--text-3"). */
export function cssColor(token: string): string {
  void theme.epoch;
  let value = cache.get(token);
  if (value === undefined) {
    value = getComputedStyle(document.documentElement).getPropertyValue(token).trim();
    cache.set(token, value);
  }
  return value;
}

/** Starts following theme changes (system scheme and the manual override). */
export function watchTheme(): () => void {
  const bump = () => {
    cache.clear();
    theme.epoch++;
  };
  const media = window.matchMedia("(prefers-color-scheme: light)");
  media.addEventListener("change", bump);
  const observer = new MutationObserver(bump);
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  return () => {
    media.removeEventListener("change", bump);
    observer.disconnect();
  };
}
