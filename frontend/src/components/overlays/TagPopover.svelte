<!-- T: add and remove tags on the selection. -->
<script lang="ts">
  import { api } from "../../lib/api";
  import { closeOverlay, overlay, sidebar } from "../../lib/state.svelte";
  import { normaliseTag, plural } from "./format";
  import Picker, { type PickerItem } from "./Picker.svelte";
  import { pickerRank, recent, rememberRecent, targetIds, useChecks, useDetails } from "./targets.svelte";

  const opened = overlay("tags");
  // Fixed while the popover is open, so a toggle never lands on a different
  // set than the one the check marks describe.
  const ids = (opened?.props?.ids as string[] | undefined) ?? targetIds();
  const details = useDetails(() => ids);

  const checks = useChecks(details, (d) => d.tags);

  const recents = recent("tags");

  const items = $derived.by((): PickerItem[] => {
    const counts = new Map((sidebar.data?.tags ?? []).map((t) => [t.tag, t]));
    const known = new Set([...counts.keys(), ...checks.keys()]);
    const order = (tag: string) => pickerRank(tag, checks.appliedAtOpen(tag), recents);
    return [...known]
      .sort((a, b) => order(a) - order(b) || a.localeCompare(b))
      .map((tag) => ({
        id: tag,
        label: tag,
        hint: counts.get(tag)?.count.toLocaleString(),
        state: checks.stateOf(tag),
        color: counts.get(tag)?.color,
      }));
  });

  const ghosts = $derived.by(() => {
    const seen = new Set<string>();
    for (const d of details.list) for (const t of d.suggestedTags) if (checks.stateOf(t) !== "on") seen.add(t);
    return [...seen].slice(0, 6);
  });

  async function set(tag: string, on: boolean): Promise<void> {
    const ok = await checks.apply(tag, on, () => (on ? api.addTags(ids, [tag]) : api.removeTags(ids, [tag])));
    if (ok && on) rememberRecent("tags", tag);
  }
</script>

<Picker
  label="Tags"
  placeholder="Tag…"
  subject={plural(ids.length, "sample")}
  anchor={opened?.anchor}
  {items}
  paths
  {ghosts}
  creatable={normaliseTag}
  emptyText="No tags yet. Type a name to create one; use a/b to nest."
  ontoggle={(item) => set(item.id, item.state !== "on")}
  oncreate={(name) => set(name, true)}
  onghost={(tag) => set(tag, true)}
  onclose={() => closeOverlay("tags")}
/>
