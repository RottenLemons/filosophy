<script lang="ts">
  const connections = [
    { name: 'Local files', status: 'Enabled', detail: 'Indexed folders are managed in Settings.' },
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

</div>

<style>
  .connections-page {
    display: flex;
    flex-direction: column;
    gap: 18px;
    max-width: 1120px;
  }

  h1,
  p {
    margin: 0;
  }

  h1 {
    font-size: 24px;
    font-weight: 650;
    margin-bottom: 6px;
  }

  .page-head p,
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

</style>
