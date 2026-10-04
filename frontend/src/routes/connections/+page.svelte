<script lang="ts">
  import { goto } from '$app/navigation';
  import { Cable, FolderOpen, Cpu, ArrowUpRight, Settings2 } from 'lucide-svelte';

  const connections = [
    { name: 'Local files', status: 'On device', detail: 'Indexed folders and their search index stay on this computer.', Icon: FolderOpen, tone: 'green' },
    { name: 'Language model', status: 'Optional', detail: 'Connect a local Ollama or LM Studio model, or an OpenAI-compatible endpoint.', Icon: Cpu, tone: 'warm' },
    { name: 'Local API', status: 'Optional', detail: 'Expose search to local scripts and tools from the API settings.', Icon: Cable, tone: 'neutral' }
  ];
</script>

<svelte:head><title>Connections - Filosophy</title></svelte:head>

<div class="f-page connections-page">
  <header class="page-head">
    <div><p class="eyebrow">Workspace</p><h1>Connections</h1></div>
    <button class="f-button" on:click={() => goto('/settings')}><Settings2 size={15} /> Connection settings</button>
  </header>
  <p class="intro">Choose what Filosophy can connect to. External services are only used when configured.</p>
  <section class="connection-list" aria-label="Available connections">
    {#each connections as item}
      <article class="connection-row">
        <div class={`connection-icon ${item.tone}`}><svelte:component this={item.Icon} size={18} strokeWidth={1.8} /></div>
        <div class="connection-copy"><div class="connection-title"><h2>{item.name}</h2><span class={`status ${item.tone}`}>{item.status}</span></div><p>{item.detail}</p></div>
        <button class="row-action" type="button" aria-label={`Configure ${item.name}`} on:click={() => goto('/settings')}><ArrowUpRight size={16} /></button>
      </article>
    {/each}
  </section>
  <aside class="privacy-note"><span class="privacy-dot"></span><div><strong>Private by default</strong><p>Search and indexing run locally. Assistant requests follow the provider and endpoint configured in Settings.</p></div></aside>
</div>

<style>
  .connections-page { display: flex; flex-direction: column; gap: 18px; }
  .page-head { display: flex; align-items: end; justify-content: space-between; gap: 18px; }
  h1 { margin: 0; font-size: 23px; line-height: 1.2; font-weight: 650; }
  .eyebrow { margin: 0 0 4px; color: var(--f-text-3); font-size: 10px; font-weight: 700; text-transform: uppercase; }
  .intro { max-width: 680px; margin: 0; color: var(--f-text-2); font-size: 13px; }
  .connection-list { max-width: 920px; border-top: 1px solid var(--f-border); }
  .connection-row { display: flex; min-height: 88px; align-items: center; gap: 14px; border-bottom: 1px solid var(--f-border); }
  .connection-icon { display: grid; width: 38px; height: 38px; flex: 0 0 auto; place-items: center; border-radius: 5px; background: var(--f-surface-2); color: var(--f-text-2); }
  .connection-icon.green { color: var(--f-success); background: var(--f-accent-soft); }
  .connection-icon.warm { color: var(--f-warm); }
  .connection-copy { min-width: 0; flex: 1; padding: 14px 0; }
  .connection-title { display: flex; flex-wrap: wrap; align-items: center; gap: 9px; }
  h2 { margin: 0; font-size: 13px; font-weight: 650; }
  .status { padding: 2px 6px; border-radius: 3px; background: var(--f-surface-2); color: var(--f-text-2); font-size: 10px; font-weight: 600; }
  .status.green { background: var(--f-accent-soft); color: var(--f-success); }
  .status.warm { color: var(--f-warm); }
  .connection-copy p { margin: 4px 0 0; color: var(--f-text-2); font-size: 12px; }
  .row-action { display: grid; width: 32px; height: 32px; flex: 0 0 auto; place-items: center; border: 0; border-radius: 4px; background: transparent; color: var(--f-text-3); cursor: pointer; }
  .row-action:hover { background: var(--f-surface-2); color: var(--f-text); }
  .privacy-note { display: flex; max-width: 920px; gap: 10px; padding: 14px 0; border-top: 1px solid var(--f-border); }
  .privacy-dot { width: 7px; height: 7px; flex: 0 0 auto; margin-top: 5px; border-radius: 50%; background: var(--f-success); }
  .privacy-note strong { font-size: 12px; font-weight: 650; }
  .privacy-note p { margin: 3px 0 0; color: var(--f-text-2); font-size: 11px; }
  @media (max-width: 560px) { .page-head { align-items: start; flex-direction: column; } .connection-row { gap: 10px; } }
</style>
