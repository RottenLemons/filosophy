<script lang="ts">
  import { goto } from '$app/navigation';
  import SettingsModal from '$lib/components/SettingsModal.svelte';

  let showSettings = true;

  function closeSettings() {
    showSettings = false;
    goto('/');
  }
</script>

<svelte:head>
  <title>Settings - Filosophy</title>
</svelte:head>

<div class="f-page settings-page">
  <p class="eyebrow">Workspace</p>
  <h1>Settings</h1>
  <p class="settings-summary">Indexing, model connections, and ranking preferences.</p>
</div>

<SettingsModal bind:show={showSettings} on:close={closeSettings} />

<style>
  .settings-page { max-width: 760px; }
  h1 { margin: 0; font-size: 23px; line-height: 1.2; font-weight: 650; }
  .eyebrow { margin: 0 0 4px; color: var(--f-text-3); font-size: 10px; font-weight: 700; text-transform: uppercase; }
  .settings-summary { margin: 6px 0; color: var(--f-text-2); font-size: 13px; }
</style>
