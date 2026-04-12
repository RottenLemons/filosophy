<script>
  import { slide } from 'svelte/transition';
  import { ChevronRight, Folder, FolderOpen } from 'lucide-svelte';
  import { GetFolderChildren, SetFolderIndexed, SetDirPathOnly } from '$lib/wailsjs/go/main/App';

  /** @typedef {{ Name: string, Path: string, Indexed: boolean, PathOnly: boolean, HasChildren: boolean }} FolderState */

  /** @type {FolderState} */
  export let folder;
  /** @type {number} */
  export let depth = 0;
  export let searchTerm = "";

  // Cascade prop
  export let parentExcluded = false;

  let isExpanded = false;
  /** @type {FolderState[] | null} */
  let children = null;
  let loading = false;

  $: isGloballyExcluded = parentExcluded || !folder.Indexed;

  $: isVisible = !searchTerm ||
                 folder.Name.toLowerCase().includes(searchTerm.toLowerCase()) ||
                 folder.Path.toLowerCase().includes(searchTerm.toLowerCase());

  async function toggleExpand() {
    isExpanded = !isExpanded;
    if (isExpanded && children === null) {
      loading = true;
      try {
        children = await GetFolderChildren(folder.Path) || [];
      } catch (e) {
        children = [];
        console.error('GetFolderChildren error:', e);
      } finally {
        loading = false;
      }
    }
  }

  /** @param {Event} e */
  async function onCheckbox(e) {
    const checked = /** @type {HTMLInputElement} */ (e.currentTarget).checked;
    folder = { ...folder, Indexed: checked };
    try {
      await SetFolderIndexed(folder.Path, checked);
    } catch (err) {
      folder = { ...folder, Indexed: !checked };
      console.error('SetFolderIndexed error:', err);
    }
  }

  async function togglePathOnly() {
    const next = !folder.PathOnly;
    folder = { ...folder, PathOnly: next };
    try {
      await SetDirPathOnly(folder.Path, next);
    } catch (err) {
      folder = { ...folder, PathOnly: !next };
      console.error('SetDirPathOnly error:', err);
    }
  }
</script>

{#if isVisible}
  <div class="node" style="--depth: {depth}">
    <div class="row {isGloballyExcluded ? 'opacity-40 grayscale-[0.5]' : 'opacity-100'} transition-all duration-300">
      <!-- Expand toggle -->
      <div class="expand-col">
        {#if folder.HasChildren}
          <button class="expand-btn" on:click={toggleExpand} aria-label={isExpanded ? 'Collapse' : 'Expand'}>
            <ChevronRight size={14} class="chevron {isExpanded ? 'open' : ''}" />
          </button>
        {/if}
      </div>

      <!-- Icon -->
      <div class="folder-icon">
        {#if isExpanded}
          <FolderOpen size={14} class={isGloballyExcluded ? 'text-gray-400' : 'text-blue-500'} />
        {:else}
          <Folder size={14} class="text-gray-400 dark:text-gray-500" />
        {/if}
      </div>

      <!-- Checkbox + label -->
      <label class="folder-label">
        <input
          type="checkbox"
          class="folder-checkbox"
          checked={folder.Indexed}
          disabled={parentExcluded}
          on:change={onCheckbox}
        />
        <span class="folder-name" title={folder.Path}>
          {folder.Name}
          {#if parentExcluded}
            <span class="text-[8px] italic ml-2 opacity-60">(Excluded by parent)</span>
          {/if}
        </span>
      </label>

      <!-- Path-only toggle (only shown when folder is indexed) -->
      {#if !isGloballyExcluded}
        <button
          class="path-only-btn {folder.PathOnly ? 'active' : ''}"
          on:click|stopPropagation={togglePathOnly}
          title={folder.PathOnly ? 'Path only — click to enable full content indexing' : 'Click to make path-only (skip content indexing)'}
        >
          {folder.PathOnly ? 'path only' : 'P'}
        </button>
      {/if}
    </div>

    <!-- Children (lazy loaded) -->
    {#if isExpanded}
      <div class="children" transition:slide={{ duration: 150 }}>
        {#if loading}
          <div class="loading-hint">Loading subdirectories...</div>
        {:else if children && children.length > 0}
          {#each children as child (child.Path)}
            <!-- Task 3: Recursive cascade -->
            <svelte:self folder={child} depth={depth + 1} {searchTerm} parentExcluded={isGloballyExcluded} />
          {/each}
        {:else}
          <div class="empty-hint">Empty directory</div>
        {/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  .node {
    width: 100%;
    user-select: none;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.35rem 0.5rem;
    padding-left: calc(0.5rem + var(--depth) * 1.25rem);
    border-radius: 4px;
    transition: background 0.15s ease;
  }
  .row:hover:not(.opacity-40) {
    background: rgba(0, 0, 0, 0.03);
  }
  :global(.dark) .row:hover:not(.opacity-40) {
    background: rgba(255, 255, 255, 0.04);
  }

  .expand-col {
    width: 14px;
    display: flex;
    justify-content: center;
  }

  /* Expand button */
  .expand-btn {
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    color: #64748b;
    display: flex;
    align-items: center;
    transition: color 0.15s;
  }
  .expand-btn:hover {
    color: #3b82f6;
  }

  :global(.chevron) {
    transition: transform 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  }
  :global(.chevron.open) {
    transform: rotate(90deg);
  }

  .folder-icon {
    display: flex;
    align-items: center;
    flex-shrink: 0;
  }

  /* Checkbox + label */
  .folder-label {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    cursor: pointer;
    flex: 1;
    min-width: 0;
  }

  .folder-checkbox {
    appearance: none;
    -webkit-appearance: none;
    width: 14px;
    height: 14px;
    border: 1.5px solid #cbd5e1;
    border-radius: 3px;
    background: transparent;
    cursor: pointer;
    flex-shrink: 0;
    position: relative;
    transition: border-color 0.15s ease, background 0.15s ease, transform 0.1s;
  }
  .folder-checkbox:disabled {
    cursor: not-allowed;
    opacity: 0.5;
  }
  :global(.dark) .folder-checkbox {
    border-color: #334155;
  }

  .folder-checkbox:active:not(:disabled) {
    transform: scale(0.9);
  }

  .folder-checkbox:checked {
    background: #3b82f6;
    border-color: #3b82f6;
  }
  .folder-checkbox:checked::after {
    content: '';
    position: absolute;
    left: 4px;
    top: 1px;
    width: 4px;
    height: 7px;
    border: 1.5px solid white;
    border-top: none;
    border-left: none;
    transform: rotate(45deg);
  }

  .folder-name {
    font-family: 'Inter', sans-serif;
    font-size: 0.8rem;
    font-weight: 500;
    color: #334155;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    letter-spacing: -0.01em;
  }
  :global(.dark) .folder-name {
    color: #cbd5e1;
  }

  /* Path-only toggle button */
  .path-only-btn {
    flex-shrink: 0;
    font-family: 'Inter', sans-serif;
    font-size: 0.6rem;
    font-weight: 600;
    letter-spacing: 0.03em;
    padding: 1px 5px;
    border-radius: 3px;
    border: 1px solid transparent;
    cursor: pointer;
    opacity: 0;
    transition: opacity 0.15s ease, background 0.15s ease, color 0.15s ease;
    background: transparent;
    color: #94a3b8;
    border-color: #cbd5e1;
    text-transform: uppercase;
  }
  .row:hover .path-only-btn {
    opacity: 1;
  }
  .path-only-btn:hover {
    background: #fef3c7;
    border-color: #f59e0b;
    color: #92400e;
  }
  .path-only-btn.active {
    opacity: 1;
    background: #fef3c7;
    border-color: #f59e0b;
    color: #92400e;
  }
  :global(.dark) .path-only-btn {
    color: #64748b;
    border-color: #334155;
  }
  :global(.dark) .path-only-btn:hover,
  :global(.dark) .path-only-btn.active {
    background: #451a03;
    border-color: #d97706;
    color: #fcd34d;
  }

  /* Children container */
  .children {
    width: 100%;
  }

  .loading-hint,
  .empty-hint {
    font-family: 'Inter', sans-serif;
    font-size: 0.7rem;
    color: #94a3b8;
    padding: 0.2rem 0.5rem;
    padding-left: calc(0.5rem + var(--depth) * 1.25rem + 2.5rem);
    font-style: italic;
  }
</style>
