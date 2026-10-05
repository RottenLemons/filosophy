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
  <header class="settings-heading">
    <div>
      <p class="eyebrow">Workspace</p>
      <h1>Settings</h1>
    </div>
    <p class="settings-summary">Manage indexed folders, performance, ranking, and integrations.</p>
  </header>
  <SettingsModal bind:show={showSettings} embedded={true} on:close={closeSettings} />
</div>

<style>
  .settings-page { display: flex; min-height: calc(100dvh - 68px); max-width: 1120px; flex-direction: column; gap: 18px; }
  .settings-heading { display: flex; align-items: end; justify-content: space-between; gap: 20px; }
  h1 { margin: 0; font-size: 23px; line-height: 1.2; font-weight: 650; }
  .eyebrow { margin: 0 0 4px; color: var(--f-text-3); font-size: 10px; font-weight: 700; text-transform: uppercase; }
  .settings-summary { margin: 0 0 2px; color: var(--f-text-2); font-size: 12px; }
  @media (max-width: 640px) {
    .settings-page { min-height: calc(100dvh - 120px); }
    .settings-heading { align-items: start; flex-direction: column; gap: 5px; }
  }
</style>
