<script>
  import { onMount, onDestroy } from 'svelte';
  import { fade, slide } from 'svelte/transition';
  import { Search as SearchIcon, FolderPlus } from 'lucide-svelte';
  import { GetHomeFolders, BrowseForDirectory, AddExtraDirectory, RemoveExtraDirectory } from '$lib/wailsjs/go/main/App';
  import { indexingStatus } from '../../stores/indexer';
  import FolderNode from './FolderNode.svelte';

  export let flat = false;

  /** @type {any[]} */
  let folders = [];
  let isOpen = false;
  let searchTerm = "";

  /** @type {(() => void) | null} */
  let eventsCleanup = null;

  async function loadFolders() {
    try {
      folders = (await GetHomeFolders()) || [];
    } catch (e) {
      console.error('GetHomeFolders error:', e);
    }
  }

  onMount(async () => {
    await loadFolders();

    const w = /** @type {any} */ (window);
    if (w.runtime && w.runtime.EventsOn) {
      w.runtime.EventsOn('home_folders_changed', loadFolders);
      eventsCleanup = () => {
        w.runtime.EventsOff('home_folders_changed');
      };
    }
  });

  onDestroy(() => {
    if (eventsCleanup) eventsCleanup();
  });

  /** @param {MouseEvent} e */
  function onBackdropClick(e) {
    if (e.target === e.currentTarget) isOpen = false;
  }

  async function browseAndAddFolder() {
    try {
      const dir = await BrowseForDirectory();
      if (dir) {
        await AddExtraDirectory(dir);
        await loadFolders();
      }
    } catch (e) {
      console.error('Add folder error:', e);
    }
  }
</script>

<div class="indexer-container" class:flat>
  {#if !flat}
    <div class="picker-wrap">
      <button
        class="btn-folders"
        on:click={() => { isOpen = !isOpen; if (isOpen) loadFolders(); }}
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="square">
          <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>
        </svg>
        <span>Indexed Folders</span>
        <svg class="chevron" class:open={isOpen} width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
          <path d="M6 9l6 6 6-6"/>
        </svg>
      </button>

      {#if isOpen}
        <!-- svelte-ignore a11y-click-events-have-key-events -->
        <!-- svelte-ignore a11y-no-static-element-interactions -->
        <div class="backdrop" on:click={onBackdropClick}></div>

        <div class="dropdown" transition:slide={{ duration: 150 }}>
          <div class="dropdown-header">
            <span class="dropdown-title">Home Folder</span>
            <span class="dropdown-hint">Only checked folders are indexed</span>
          </div>

          <div class="folder-tree">
            {#if folders.length === 0}
              <div class="empty-hint">No subfolders found.</div>
            {:else}
              {#each folders as folder (folder.Path)}
                <FolderNode {folder} depth={0} />
              {/each}
            {/if}
          </div>
        </div>
      {/if}
    </div>

    <!-- Progress bar (only shown in non-flat mode or kept for compatibility) -->
    {#if $indexingStatus.statusMessage}
      <div class="progress-wrapper" transition:fade={{ duration: 300 }}>
        <span class="status-label">{$indexingStatus.statusMessage}</span>
        {#if $indexingStatus.isIndexing}
          <div class="progress-track">
            <div class="progress-fill" class:indeterminate={!$indexingStatus.searchReady} style="width: {$indexingStatus.searchReady ? $indexingStatus.progress : 30}%"></div>
          </div>
        {/if}
      </div>
    {/if}
  {:else}
    <!-- Flat Integrated View for Modal -->
    <div class="flat-view">
      <div class="search-bar">
        <div class="search-row">
          <div class="search-input-wrap">
            <SearchIcon class="w-3.5 h-3.5" />
            <input 
              type="text" 
              bind:value={searchTerm} 
              placeholder="Search folders..." 
              class="folder-search-input"
            />
          </div>
          <button class="btn-add-folder" on:click={browseAndAddFolder} title="Add a network drive or external folder">
            <FolderPlus class="w-3.5 h-3.5" />
            <span>Add Folder</span>
          </button>
        </div>
      </div>

      <div class="folder-tree scrollbar-custom">
        {#if folders.length === 0}
          <div class="empty-hint">Scanning file system...</div>
        {:else}
          {#each folders as folder (folder.Path)}
            <FolderNode {folder} depth={0} {searchTerm} />
          {/each}
        {/if}
      </div>
    </div>
  {/if}
</div>

<style>
  .indexer-container {
    width: 100%;
    position: relative;
  }

  .indexer-container:not(.flat) {
    display: flex;
    align-items: center;
    gap: 1.5rem;
    padding: 0.25rem 0;
  }

  .picker-wrap {
    position: relative;
    flex-shrink: 0;
  }

  .btn-folders {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    padding: 0.35rem 0.85rem;
    background: transparent;
    border: 1px solid rgba(71, 72, 72, 0.2);
    border-radius: 0.125rem;
    color: #bfc8ca;
    font-family: 'Inter', sans-serif;
    font-size: 0.7rem;
    font-weight: 600;
    cursor: pointer;
    transition: background 0.15s ease, border-color 0.15s ease;
    white-space: nowrap;
  }
  .btn-folders:hover {
    background: rgba(191, 200, 202, 0.06);
    border-color: rgba(191, 200, 202, 0.2);
  }

  .chevron {
    transition: transform 0.2s ease;
  }
  .chevron.open {
    transform: rotate(180deg);
  }

  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 40;
  }

  .dropdown {
    position: absolute;
    top: calc(100% + 6px);
    left: 0;
    z-index: 50;
    width: 280px;
    max-height: 400px;
    overflow-y: auto;
    background: #131313;
    border: 1px solid rgba(71, 72, 72, 0.2);
    box-shadow: 0 16px 48px rgba(0, 0, 0, 0.8);
    border-radius: 0.125rem;
    display: flex;
    flex-direction: column;
  }

  .dropdown-header {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    padding: 0.6rem 0.75rem 0.4rem;
    border-bottom: 1px solid rgba(71, 72, 72, 0.1);
    flex-shrink: 0;
  }

  .dropdown-title {
    font-family: 'Inter', sans-serif;
    font-size: 0.6rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    color: #bfc8ca;
  }

  .dropdown-hint {
    font-family: 'Inter', sans-serif;
    font-size: 0.56rem;
    color: #555;
  }

  .folder-tree {
    padding: 0.3rem 0;
    overflow-y: auto;
  }

  .empty-hint {
    padding: 1.5rem;
    font-family: 'Inter', sans-serif;
    font-size: 0.7rem;
    color: #666;
    text-align: center;
    font-style: italic;
  }

  /* Flat View */
  .flat-view {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 300px;
  }

  .search-bar {
    padding: 0 0 14px;
  }

  .search-input-wrap {
    display: flex;
    min-width: 0;
    min-height: 40px;
    align-items: center;
    flex: 1;
    gap: 9px;
    padding: 0 11px;
    background: var(--f-surface);
    border: 1px solid var(--f-border-strong);
    border-radius: 4px;
    transition: border-color 120ms ease, box-shadow 120ms ease;
  }
  .search-input-wrap:focus-within {
    border-color: var(--f-accent);
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--f-accent) 16%, transparent);
  }
  .search-input-wrap :global(svg) {
    flex: 0 0 auto;
    color: var(--f-text-3);
  }

  .folder-search-input {
    flex: 1;
    min-width: 0;
    height: 38px;
    background: transparent;
    border: none;
    outline: none;
    font-size: 13px;
    font-family: inherit;
    color: var(--f-text);
  }
  .folder-search-input::placeholder {
    color: var(--f-text-3);
    opacity: 1;
  }

  .search-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .btn-add-folder {
    display: flex;
    min-height: 40px;
    align-items: center;
    gap: 7px;
    padding: 0 12px;
    background: var(--f-surface);
    border: 1px solid var(--f-border);
    border-radius: 4px;
    color: var(--f-text);
    font-family: inherit;
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
    white-space: nowrap;
    transition: background 120ms ease, border-color 120ms ease;
  }
  .btn-add-folder:hover {
    background: var(--f-surface-2);
    border-color: var(--f-border-strong);
  }

  .flat-view .folder-tree {
    flex: 1;
    padding-top: 0.5rem;
    padding-bottom: 2rem;
  }

  /* Progress (Legacy support or sidebar) */
  .progress-wrapper {
    display: flex;
    align-items: center;
    gap: 1rem;
    flex-grow: 1;
    min-width: 0;
  }

  .status-label {
    font-family: 'Inter', sans-serif;
    font-size: 0.62rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: #acabab;
    white-space: pre-line;
    flex-shrink: 0;
  }

  .progress-track {
    flex-grow: 1;
    height: 4px;
    background-color: #252626;
    border-radius: 0.125rem;
    overflow: hidden;
  }

  .progress-fill {
    height: 100%;
    background: linear-gradient(to bottom right, #bfc8ca, #3f484a);
    border-radius: 0.125rem;
    transition: width 0.3s ease-out;
  }

  .progress-fill.indeterminate {
    animation: scan-progress 1.8s ease-in-out infinite alternate;
  }

  @keyframes scan-progress {
    from { transform: translateX(0); }
    to { transform: translateX(230%); }
  }

  /* Custom Scrollbar integration */
  .scrollbar-custom::-webkit-scrollbar { width: 4px; }
  .scrollbar-custom::-webkit-scrollbar-track { background: transparent; }
  .scrollbar-custom::-webkit-scrollbar-thumb { background: #e2e8f0; border-radius: 10px; }
  :global(.dark) .scrollbar-custom::-webkit-scrollbar-thumb { background: #1a1a1a; }
</style>
