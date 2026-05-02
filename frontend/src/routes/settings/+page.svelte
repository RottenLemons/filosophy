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
  <div class="f-panel settings-card">
    <h1>Settings</h1>
    <p>Folder indexing, hardware, model connections, and local API settings are available in the settings window.</p>
    <button class="f-button primary" type="button" on:click={() => showSettings = true}>Open settings</button>
  </div>
</div>

<SettingsModal bind:show={showSettings} on:close={closeSettings} />

<style>
  .settings-page {
    max-width: 760px;
  }

  .settings-card {
    padding: 18px;
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 12px;
  }

  h1,
  p {
    margin: 0;
  }

  h1 {
    font-size: 24px;
    font-weight: 650;
  }

  p {
    color: var(--f-text-2);
    line-height: 1.5;
  }
</style>
