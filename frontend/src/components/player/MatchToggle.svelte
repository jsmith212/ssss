<!--
  One of the two match switches. The caption names it; the value shows what
  the sample sounds like ("124", "+2 st"). Accent caption = switched on;
  accent value = it is changing the sample that is shown.
-->
<script lang="ts">
  interface Props {
    caption: string;
    value: string;
    /** The setting is switched on. */
    on: boolean;
    /** The setting applies to the shown sample. */
    applies: boolean;
    title: string;
    onclick: () => void;
  }

  let { caption, value, on, applies, title, onclick }: Props = $props();
</script>

<button class="toggle" class:on class:applies={on && applies} aria-pressed={on} {title} {onclick}>
  <span class="caption">{caption}</span>
  <span class="value num truncate">{value}</span>
</button>

<style>
  .toggle {
    display: flex;
    flex: none;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 1px;
    width: 52px;
    height: 40px;
    border-radius: var(--radius-control);
    transition: background var(--dur-fast) var(--ease);
  }

  .toggle:hover {
    background: var(--bg-2);
  }

  .caption {
    color: var(--text-3);
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.04em;
    line-height: 1.2;
    text-transform: uppercase;
  }

  .value {
    max-width: 100%;
    color: var(--text-2);
    font-size: var(--text-meta);
    line-height: 1.3;
  }

  .on .caption {
    color: var(--accent-text);
  }

  .applies {
    background: var(--accent-subtle);
  }

  .applies:hover {
    background: color-mix(in srgb, var(--accent-subtle) 85%, var(--accent));
  }

  .applies .value {
    color: var(--accent-text);
    font-weight: 500;
  }
</style>
