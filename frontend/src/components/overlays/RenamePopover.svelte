<!--
  One-field popover that asks for a name: renaming a tag, collection, pack or
  sample from a menu, or naming something new (a saved view, a collection).
-->
<script lang="ts">
  import { closeOverlay, overlay } from "../../lib/state.svelte";
  import Surface from "./Surface.svelte";
  import { report } from "./targets.svelte";

  const opened = overlay("rename");
  const title = (opened?.props?.title as string | undefined) ?? "Rename";
  const original = (opened?.props?.value as string | undefined) ?? "";
  const commit = opened?.props?.commit as ((value: string) => Promise<void>) | undefined;
  /** The value is a suggested name for something new, so it commits even when left as it is. */
  const fresh = opened?.props?.fresh === true;

  let value = $state(original);
  let busy = $state(false);

  const close = () => closeOverlay("rename");

  async function submit(event: SubmitEvent): Promise<void> {
    event.preventDefault();
    const next = value.trim();
    if (busy) return;
    if (next === "" || (next === original && !fresh) || !commit) return close();
    busy = true;
    try {
      await commit(next);
      close();
    } catch (err) {
      // Stay open so the name can be corrected.
      report(err);
    } finally {
      busy = false;
    }
  }

  function selectAll(node: HTMLInputElement): void {
    node.select();
  }
</script>

<Surface label={title} anchor={opened?.anchor} onclose={close} width={280}>
  <form onsubmit={submit}>
    <label class="section-label" for="rename-field">{title}</label>
    <input
      id="rename-field"
      class="ov-field"
      type="text"
      data-autofocus
      autocomplete="off"
      spellcheck="false"
      bind:value
      use:selectAll
    />
  </form>
</Surface>

<style>
  form {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding: var(--space-4);
  }
</style>
