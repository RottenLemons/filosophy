<script lang="ts">
  import { page } from '$app/stores';
  import { onMount } from 'svelte';
  import SearchIcon from 'carbon-icons-svelte/lib/Search.svelte';
  import DocumentIcon from 'carbon-icons-svelte/lib/Document.svelte';
  import ImageIcon from 'carbon-icons-svelte/lib/Image.svelte';
  import PDFIcon from 'carbon-icons-svelte/lib/PDF.svelte';
  import { Search, OpenFileNative } from '$lib/wailsjs/go/main/App';

  let searchValue = '';
  let searchedValue = '';
  let files: any[] = [];
  let searching = false;
  let error = '';
  let ticket = 0;

  $: queryFromUrl = $page.url.searchParams.get('q') || '';

  onMount(() => {
    if (queryFromUrl) {
      searchValue = queryFromUrl;
      runSearch();
    }
  });

  function getFileName(path: string) {
    return path ? path.split(/[/\\]/).pop() || path : '';
  }

  function getFileType(path: string) {
    const ext = (path || '').split('.').pop();
    return ext && ext !== path ? ext.toUpperCase() : 'File';
  }

  function getFileCategory(path: string) {
    const ext = (path || '').split('.').pop()?.toLowerCase();
    if (ext === 'pdf') return 'pdf';
    if (['png', 'jpg', 'jpeg', 'gif', 'webp'].includes(ext || '')) return 'image';
    return 'document';
  }

  function formatSize(bytes: number) {
    if (!bytes) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB'];
    let value = bytes;
    let i = 0;
    while (value >= 1024 && i < units.length - 1) {
      value /= 1024;
      i++;
    }
    return `${value.toFixed(value >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
  }

  function formatDate(value: string) {
    if (!value) return 'Unknown date';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? 'Unknown date' : date.toLocaleDateString();
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter') runSearch();
  }

  async function runSearch() {
    const query = searchValue.trim();
    ticket++;
    const current = ticket;
    error = '';

    if (!query) {
      files = [];
      searching = false;
      return;
    }

    searching = true;

    Search(query).then((result) => {
      if (current !== ticket) return;
      files = result || [];
    }).catch((err) => {
      if (current !== ticket) return;
      error = String(err);
      files = [];
    }).finally(() => {
      if (current === ticket) searching = false;
    });

  }
</script>

<svelte:head>
  <title>Search - Filosophy</title>
</svelte:head>

<div class="f-page search-page">
  <div class="page-heading">
    <div>
      <p class="eyebrow">Workspace</p>
      <h1>Search</h1>
    </div>
    <span class="local-label"><i></i> Local index</span>
  </div>
  <div class="search-top">
    <div class="search-box">
      <SearchIcon size={20} />
      <input
        class="f-input"
        bind:value={searchValue}
        on:keydown={handleKeydown}
        placeholder="Search files, folders, or messages"
      />
    </div>
    <button class="f-button primary" type="button" on:click={runSearch} disabled={searching}>Search</button>
  </div>

  <div class="meta">
    {#if searching}
      Searching...
    {:else if searchValue.trim()}
      {files.length} results
    {:else}
      Enter a search term to begin.
    {/if}
  </div>

  {#if error}
    <div class="f-panel error">
      <strong>Search failed</strong>
      <span>{error}</span>
    </div>
  {/if}

  {#if !searching && searchValue.trim() && files.length === 0 && !error}
    <div class="f-panel empty">
      No matching files found.
    </div>
  {/if}

  <div class="results">
    {#each files as file}
      <button class="f-panel result" type="button" on:click={() => OpenFileNative(file.Path)}>
        <span class="result-icon">
          {#if getFileCategory(file.Path) === 'image'}
            <ImageIcon size={22} />
          {:else if getFileCategory(file.Path) === 'pdf'}
            <PDFIcon size={22} />
          {:else}
            <DocumentIcon size={22} />
          {/if}
        </span>
        <span class="result-body">
          <strong>{getFileName(file.Path)}</strong>
          <small>{file.Path}</small>
          <span class="details">{getFileType(file.Path)} · {formatSize(file.Size)} · Modified {formatDate(file.Modified)}</span>
        </span>

      </button>
    {/each}

  </div>
</div>

<style>
  .search-page { display: flex; flex-direction: column; gap: 17px; }
  .page-heading { display: flex; justify-content: space-between; align-items: end; gap: 16px; }
  .page-heading p { margin: 0 0 4px; color: var(--f-text-3); font-size: 10px; font-weight: 700; text-transform: uppercase; }
  .page-heading h1 { margin: 0; font-size: 23px; line-height: 1.2; font-weight: 650; }
  .local-label { display: inline-flex; align-items: center; gap: 7px; color: var(--f-text-2); font-size: 11px; white-space: nowrap; }
  .local-label i { width: 7px; height: 7px; border-radius: 50%; background: var(--f-success); }

  .search-top {
    display: flex;
    gap: 10px;
    max-width: 920px;
  }

  .search-box {
    position: relative;
    flex: 1;
    min-width: 0;
  }

  .search-box :global(svg) {
    position: absolute;
    left: 12px;
    top: 50%;
    transform: translateY(-50%);
    color: var(--f-text-3);
  }

  input {
    width: 100%;
    height: 42px;
    padding: 0 12px 0 40px;
  }

  .meta {
    color: var(--f-text-3);
    font-size: 14px;
  }

  .results {
    display: flex;
    flex-direction: column;
    gap: 5px;
    max-width: 960px;
  }

  .result {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 11px 12px;
    text-align: left;
    color: var(--f-text);
    transition: background 150ms ease, border-color 150ms ease;
  }

  .result:hover {
    background: var(--f-surface-2);
    border-color: var(--f-border-strong);
  }

  .result-icon {
    width: 34px;
    height: 34px;
    border-radius: 4px;
    background: var(--f-surface-2);
    color: var(--f-text-3);
    display: flex;
    align-items: center;
    justify-content: center;
    flex: 0 0 auto;
  }

  .result-body {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
    flex: 1;
  }

  .result-body strong,
  .result-body small,
  .details {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .result-body strong {
    font-size: 15px;
  }

  .result-body small {
    color: var(--f-text-2);
    font-size: 13px;
  }

  .details {
    color: var(--f-text-3);
    font-size: 12px;
  }


  .error,
  .empty {
    max-width: 860px;
    padding: 16px;
  }

  .error {
    border-color: color-mix(in srgb, var(--f-danger) 45%, var(--f-border));
    color: var(--f-danger);
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  @media (max-width: 640px) {
    .page-heading { align-items: center; }
    .search-top {
      flex-direction: column;
    }
    .search-top .f-button { width: 100%; }
    .result { align-items: flex-start; }
    .details { white-space: normal; }
  }
</style>
