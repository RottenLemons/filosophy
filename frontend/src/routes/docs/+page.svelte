<script lang="ts">
  import { goto } from '$app/navigation';
  import SearchIcon from 'carbon-icons-svelte/lib/Search.svelte';

  let filter = '';

  const examples = [
    'PDF documents',
    'Text notes',
    'Images',
    'Recent files',
    'Markdown files',
    'Spreadsheets'
  ];

  $: visible = examples.filter((item) => item.toLowerCase().includes(filter.toLowerCase()));
</script>

<svelte:head>
  <title>Documents - Filosophy</title>
</svelte:head>

<div class="f-page docs-page">
  <div class="page-head">
    <h1>Documents</h1>
    <p>Use search to open indexed content. This view gives beginners a few clear starting points.</p>
  </div>

  <div class="filter">
    <SearchIcon size={18} />
    <input class="f-input" bind:value={filter} placeholder="Filter document types" />
  </div>

  <div class="grid">
    {#each visible as item}
      <button class="f-panel doc-card" type="button" on:click={() => goto(`/search?q=${encodeURIComponent(item)}`)}>
        <strong>{item}</strong>
        <span>Search indexed content</span>
      </button>
    {/each}
  </div>
</div>

<style>
  .docs-page {
    display: flex;
    flex-direction: column;
    gap: 18px;
  }

  h1 {
    margin: 0 0 6px;
    font-size: 24px;
    font-weight: 650;
  }

  p {
    margin: 0;
    color: var(--f-text-2);
    line-height: 1.5;
  }

  .filter {
    position: relative;
    max-width: 420px;
  }

  .filter :global(svg) {
    position: absolute;
    left: 12px;
    top: 50%;
    transform: translateY(-50%);
    color: var(--f-text-3);
  }

  input {
    width: 100%;
    height: 38px;
    padding: 0 12px 0 38px;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
    gap: 10px;
    max-width: 840px;
  }

  .doc-card {
    padding: 16px;
    text-align: left;
    display: flex;
    flex-direction: column;
    gap: 6px;
    color: var(--f-text);
  }

  .doc-card:hover {
    background: var(--f-surface-2);
  }

  .doc-card span {
    color: var(--f-text-3);
    font-size: 13px;
  }
</style>
