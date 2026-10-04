<script lang="ts">
  import Chat from '$lib/components/Chat.svelte';
</script>

<svelte:head>
  <title>Ask AI - Filosophy</title>
</svelte:head>

<div class="f-page chat-page">
  <div class="chat-page-head"><div><p>Workspace</p><h1>Assistant</h1></div><span>Uses the provider configured in Settings</span></div>
  <Chat embedded />
</div>

<style>
  .chat-page {
    max-width: none;
    padding-bottom: 0;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  .chat-page-head { display: flex; align-items: end; justify-content: space-between; gap: 16px; }
  .chat-page-head p { margin: 0 0 4px; color: var(--f-text-3); font-size: 10px; font-weight: 700; text-transform: uppercase; }
  .chat-page-head h1 { margin: 0; font-size: 23px; line-height: 1.2; font-weight: 650; }
  .chat-page-head span { color: var(--f-text-3); font-size: 11px; }
  @media (max-width: 560px) { .chat-page-head { align-items: start; flex-direction: column; gap: 5px; } }
</style>
