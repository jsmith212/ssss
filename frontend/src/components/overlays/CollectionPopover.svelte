<!-- C: add the selection to collections, or take it out. -->
<script lang="ts">
  import { api } from "../../lib/api";
  import { closeOverlay, overlay, sidebar } from "../../lib/state.svelte";
  import { plural } from "./format";
  import Picker, { type PickerItem } from "./Picker.svelte";
  import { pickerRank, recent, rememberRecent, report, targetIds, useChecks, useDetails } from "./targets.svelte";

  const opened = overlay("collections");
  const ids = (opened?.props?.ids as string[] | undefined) ?? targetIds();
  const details = useDetails(() => ids);

  const checks = useChecks(details, (d) => d.collections);
  // A collection created here, until the sidebar refresh lists it.
  let created = $state<{ id: string; name: string }[]>([]);

  const recents = recent("collections");

  const items = $derived.by((): PickerItem[] => {
    const listed = sidebar.data?.collections ?? [];
    const all = [
      ...listed.map((c) => ({ id: c.id, name: c.name, hint: c.count.toLocaleString() })),
      ...created.filter((c) => !listed.some((l) => l.id === c.id)).map((c) => ({ ...c, hint: "" })),
    ];
    const order = (id: string) => pickerRank(id, checks.appliedAtOpen(id), recents);
    return all
      .sort((a, b) => order(a.id) - order(b.id) || a.name.localeCompare(b.name))
      .map((c) => ({ id: c.id, label: c.name, hint: c.hint, state: checks.stateOf(c.id) }));
  });

  async function set(id: string, on: boolean): Promise<void> {
    const ok = await checks.apply(id, on, () =>
      on ? api.addToCollection(id, ids) : api.removeFromCollection(id, ids),
    );
    if (ok && on) rememberRecent("collections", id);
  }

  async function create(name: string): Promise<void> {
    try {
      const info = await api.createCollection(name);
      created.push({ id: info.id, name: info.name });
      await set(info.id, true);
    } catch (err) {
      report(err);
    }
  }
</script>

<Picker
  label="Collections"
  placeholder="Add to collection…"
  subject={plural(ids.length, "sample")}
  anchor={opened?.anchor}
  {items}
  creatable={(text) => text.trim()}
  emptyText="No collections yet. Type a name to create one."
  ontoggle={(item) => set(item.id, item.state !== "on")}
  oncreate={create}
  onclose={() => closeOverlay("collections")}
/>
