<script lang="ts">
  import WhatsAppTab from '$lib/components/WhatsAppTab.svelte';

  let showWhatsApp = true;

  const connections = [
    { name: 'Local files', status: 'Enabled', detail: 'Indexed folders are managed in Settings.' },
    { name: 'WhatsApp', status: 'Available', detail: 'Connect and search message history locally.' },
    { name: 'Local LLM', status: 'Optional', detail: 'Configure an Ollama, LM Studio, or OpenAI-compatible endpoint in Settings.' },
    { name: 'Local API', status: 'Optional', detail: 'Expose search to scripts and tools from Settings.' }
  ];
</script>

<svelte:head>
  <title>Connections - Filosophy</title>
</svelte:head>

<div class="f-page connections-page">
  <div class="page-head">
    <h1>Connections</h1>
    <p>Manage the places Filosophy can search. Everything is designed to stay local unless you configure an external model.</p>
  </div>

  <div class="connection-grid">
    {#each connections as item}
      <div class="f-panel connection-card">
        <div>
          <strong>{item.name}</strong>
          <span>{item.status}</span>
        </div>
        <p>{item.detail}</p>
      </div>
    {/each}
  </div>

  <section class="f-panel whatsapp-panel">
    <div class="panel-head">
      <div>
        <h2>WhatsApp</h2>
        <p>Connect, browse, and search messages from the desktop app.</p>
      </div>
      <button class="f-button" type="button" on:click={() => showWhatsApp = !showWhatsApp}>
        {showWhatsApp ? 'Hide' : 'Show'}
      </button>
    </div>
    {#if showWhatsApp}
      <div class="wa-wrap">
        <WhatsAppTab />
      </div>
    {/if}
  </section>
</div>

<style>
  .connections-page {
    display: flex;
    flex-direction: column;
    gap: 18px;
    max-width: 1120px;
  }

  h1,
  h2,
  p {
    margin: 0;
  }

  h1 {
    font-size: 24px;
    font-weight: 650;
    margin-bottom: 6px;
  }

  h2 {
    font-size: 17px;
    font-weight: 650;
  }

  .page-head p,
  .panel-head p,
  .connection-card p {
    color: var(--f-text-2);
    line-height: 1.5;
  }

  .connection-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: 10px;
  }

  .connection-card {
    padding: 14px;
  }

  .connection-card div {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 8px;
  }

  .connection-card span {
    color: var(--f-success);
    font-size: 13px;
  }

  .whatsapp-panel {
    overflow: hidden;
  }

  .panel-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 16px;
    border-bottom: 1px solid var(--f-border);
  }

  .wa-wrap {
    height: 620px;
    overflow: hidden;
  }
</style>
