<script>
  import { onMount, onDestroy } from 'svelte';
  import { fade, slide } from 'svelte/transition';
  import { GetHomeFolders } from '$lib/wailsjs/go/main/App';
  import FolderNode from './FolderNode.svelte';

  /** @typedef {{ Name: string, Path: string, Indexed: boolean, HasChildren: boolean }} FolderState */

  /** @type {FolderState[]} */
  let folders = [];
  let isOpen = false;
  let isIndexing = false;
  let progress = 0;
  let statusMessage = '';

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

      w.runtime.EventsOn('indexing_progress', (/** @type {number} */ p) => {
        if (p > progress || p === 0) progress = p;
        isIndexing = true;
      });

      w.runtime.EventsOn('indexing_status', (/** @type {string} */ s) => {
        statusMessage = s;
        if (s === 'Indexing complete.') {
          setTimeout(() => {
            isIndexing = false;
            progress = 0;
            statusMessage = '';
          }, 2000);
        } else if (s) {
          isIndexing = true;
        }
      });

      eventsCleanup = () => {
        w.runtime.EventsOff('home_folders_changed');
        w.runtime.EventsOff('indexing_progress');
        w.runtime.EventsOff('indexing_status');
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
</script>

<div class="indexer-row">
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
          <span class="dropdown-hint">Uncheck to exclude from indexing</span>
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

  <!-- Progress bar -->
  {#if isIndexing || statusMessage}
    <div class="progress-wrapper" transition:fade={{ duration: 300 }}>
      <span class="status-label">{statusMessage}</span>
      <div class="progress-track">
        <div class="progress-fill" style="width: {progress}%"></div>
      </div>
    </div>
  {/if}
</div>

<style>
  .indexer-row {
    display: flex;
    align-items: center;
    gap: 1.5rem;
    width: 100%;
    padding: 0.25rem 0;
    position: relative;
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
    border: 1px solid rgba(191, 200, 202, 0.2);
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
    border-color: rgba(191, 200, 202, 0.4);
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
    background: #111;
    border: 1px solid #2a2a2a;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.6);
    border-radius: 0.125rem;
    display: flex;
    flex-direction: column;
  }

  .dropdown-header {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    padding: 0.6rem 0.75rem 0.4rem;
    border-bottom: 1px solid #1e1e1e;
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
    padding: 0.75rem;
    font-family: 'Inter', sans-serif;
    font-size: 0.68rem;
    color: #444;
    font-style: italic;
  }

  /* Progress */
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
    color: #acabab;
    white-space: nowrap;
    flex-shrink: 0;
  }

  .progress-track {
    flex-grow: 1;
    height: 2px;
    background-color: #252626;
    overflow: hidden;
  }

  .progress-fill {
    height: 100%;
    background-color: #bfc8ca;
    transition: width 0.3s ease-out;
  }
</style>
