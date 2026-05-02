<script lang="ts">
  import { page } from '$app/stores';
  import { onMount } from 'svelte';
  import SearchIcon from 'carbon-icons-svelte/lib/Search.svelte';
  import DocumentIcon from 'carbon-icons-svelte/lib/Document.svelte';
  import ImageIcon from 'carbon-icons-svelte/lib/Image.svelte';
  import PDFIcon from 'carbon-icons-svelte/lib/PDF.svelte';
  import MessageIcon from 'carbon-icons-svelte/lib/Chat.svelte';
  import { Search, OpenFileNative, WASearchMessages, WAOpenChat } from '$lib/wailsjs/go/main/App';

  let searchValue = '';
  let files: any[] = [];
  let messages: any[] = [];
  let searching = false;
  let messageSearching = false;
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
      messages = [];
      return;
    }

    searching = true;
    messageSearching = true;

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

    WASearchMessages(query).then((result) => {
      if (current !== ticket) return;
      messages = result || [];
    }).catch(() => {
      if (current !== ticket) return;
      messages = [];
    }).finally(() => {
      if (current === ticket) messageSearching = false;
    });
  }
</script>

<svelte:head>
  <title>Search - Filosophy</title>
</svelte:head>

<div class="f-page search-page">
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
    {#if searching || messageSearching}
      Searching...
    {:else if searchValue.trim()}
      {files.length + messages.length} results
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

  {#if !searching && !messageSearching && searchValue.trim() && files.length === 0 && messages.length === 0 && !error}
    <div class="f-panel empty">
      No matching files or messages found.
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
        {#if typeof file.Score === 'number'}
          <span class="score">{Math.round(file.Score * 100)}%</span>
        {/if}
      </button>
    {/each}

    {#each messages as msg}
      <button class="f-panel result" type="button" on:click={() => WAOpenChat(msg.chatJid)}>
        <span class="result-icon">
          <MessageIcon size={22} />
        </span>
        <span class="result-body">
          <strong>{msg.chatName || msg.chatJid}</strong>
          <small>{msg.text}</small>
          <span class="details">WhatsApp · {msg.senderName || 'Unknown sender'} · {formatDate(msg.timestamp)}</span>
        </span>
      </button>
    {/each}
  </div>
</div>

<style>
  .search-page {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  .search-top {
    display: flex;
    gap: 10px;
    max-width: 860px;
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
    gap: 8px;
    max-width: 960px;
  }

  .result {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    padding: 14px;
    text-align: left;
    color: var(--f-text);
    transition: background 150ms ease, border-color 150ms ease;
  }

  .result:hover {
    background: var(--f-surface-2);
    border-color: var(--f-border-strong);
  }

  .result-icon {
    width: 36px;
    height: 36px;
    border-radius: 8px;
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

  .details,
  .score {
    color: var(--f-text-3);
    font-size: 12px;
  }

  .score {
    flex: 0 0 auto;
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
    .search-top {
      flex-direction: column;
    }
  }
</style>
