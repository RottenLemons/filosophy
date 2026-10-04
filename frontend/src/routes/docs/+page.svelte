<script lang="ts">
  import { goto } from '$app/navigation';
  import { FileText, Image, FileSpreadsheet, Clock3, Search } from 'lucide-svelte';

  let filter = '';

  const shortcuts = [
    { label: 'PDF documents', hint: 'PDF', Icon: FileText },
    { label: 'Text notes', hint: 'TXT · MD', Icon: FileText },
    { label: 'Images', hint: 'PNG · JPG · WEBP', Icon: Image },
    { label: 'Spreadsheets', hint: 'XLSX · CSV', Icon: FileSpreadsheet },
    { label: 'Recent files', hint: 'Recently modified', Icon: Clock3 }
  ];

  $: visible = shortcuts.filter(item => `${item.label} ${item.hint}`.toLowerCase().includes(filter.toLowerCase()));
</script>

<svelte:head><title>Documents - Filosophy</title></svelte:head>

<div class="f-page docs-page">
  <header class="page-head">
    <div><p class="eyebrow">Your library</p><h1>Documents</h1></div>
    <button class="f-button" on:click={() => goto('/') }><Search size={15} /> Search all files</button>
  </header>
  <label class="filter f-input">
    <Search size={16} />
    <input bind:value={filter} placeholder="Filter file categories" aria-label="Filter file categories" />
  </label>
  <div class="section-heading"><strong>Browse by type</strong><span>{visible.length} categories</span></div>
  <div class="shortcut-list">
    {#each visible as item}
      <button class="shortcut" type="button" on:click={() => goto(`/search?q=${encodeURIComponent(item.label)}`)}>
        <span class="shortcut-icon"><svelte:component this={item.Icon} size={17} strokeWidth={1.8} /></span>
        <span class="shortcut-copy"><strong>{item.label}</strong><small>{item.hint}</small></span>
        <span class="shortcut-action">Search</span>
      </button>
    {:else}
      <p class="empty">No categories match “{filter}”.</p>
    {/each}
  </div>
</div>

<style>
  .docs-page { display: flex; flex-direction: column; gap: 20px; }
  .page-head { display: flex; align-items: end; justify-content: space-between; gap: 18px; }
  .page-head h1 { margin: 0; font-size: 23px; line-height: 1.2; font-weight: 650; }
  .eyebrow { margin: 0 0 4px; color: var(--f-text-3); font-size: 10px; font-weight: 700; text-transform: uppercase; }
  .filter { display: flex; width: min(100%, 440px); height: 40px; align-items: center; gap: 10px; padding: 0 11px; color: var(--f-text-3); }
  .filter input { min-width: 0; width: 100%; border: 0; outline: 0; background: transparent; color: var(--f-text); font-size: 13px; }
  .filter:focus-within { border-color: var(--f-accent); }
  .section-heading { display: flex; max-width: 900px; align-items: center; justify-content: space-between; padding-bottom: 9px; border-bottom: 1px solid var(--f-border); }
  .section-heading strong { font-size: 12px; font-weight: 650; }
  .section-heading span { color: var(--f-text-3); font-size: 11px; }
  .shortcut-list { width: min(100%, 900px); }
  .shortcut { display: flex; width: 100%; min-height: 60px; align-items: center; gap: 12px; padding: 9px 10px; border: 0; border-bottom: 1px solid var(--f-border); background: transparent; text-align: left; cursor: pointer; }
  .shortcut:hover { background: var(--f-surface); }
  .shortcut-icon { display: grid; width: 34px; height: 34px; flex: 0 0 auto; place-items: center; border-radius: 4px; background: var(--f-accent-soft); color: var(--f-accent); }
  .shortcut-copy { display: flex; min-width: 0; flex: 1; flex-direction: column; gap: 3px; }
  .shortcut-copy strong { font-size: 13px; font-weight: 600; }
  .shortcut-copy small { color: var(--f-text-3); font-size: 11px; }
  .shortcut-action { color: var(--f-accent); font-size: 11px; font-weight: 600; }
  .empty { color: var(--f-text-2); font-size: 13px; }
  @media (max-width: 560px) { .page-head { align-items: start; flex-direction: column; } }
</style>
