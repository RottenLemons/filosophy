<script>
  import "carbon-components-svelte/css/all.css";
  import { Content } from "carbon-components-svelte";
</script>

<Content>
  <slot />
</Content>

<style>
  /* Global reset to ensure the layout fills the screen */
  :global(html, body, #svelte) {
    height: 100%;
    margin: 0;
    padding: 0;
  }
</style>