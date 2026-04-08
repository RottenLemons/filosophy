<script>
  import { slide } from 'svelte/transition';
  import { GetFolderChildren, SetFolderIndexed } from '$lib/wailsjs/go/main/App';

  /** @typedef {{ Name: string, Path: string, Indexed: boolean, HasChildren: boolean }} FolderState */

  /** @type {FolderState} */
  export let folder;
  /** @type {number} */
  export let depth = 0;

  let isExpanded = false;
  /** @type {FolderState[] | null} */
  let children = null;
  let loading = false;

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
      // revert on error
      folder = { ...folder, Indexed: !checked };
      console.error('SetFolderIndexed error:', err);
    }
  }
</script>

<div class="node" style="--depth: {depth}">
  <div class="row">
    <!-- Expand toggle -->
    {#if folder.HasChildren}
      <button class="expand-btn" on:click={toggleExpand} aria-label={isExpanded ? 'Collapse' : 'Expand'}>
        <svg class="chevron" class:open={isExpanded} width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
          <path d="M9 18l6-6-6-6"/>
        </svg>
      </button>
    {:else}
      <span class="expand-spacer"></span>
    {/if}

    <!-- Checkbox -->
    <label class="folder-label">
      <input
        type="checkbox"
        class="folder-checkbox"
        checked={folder.Indexed}
        on:change={onCheckbox}
      />
      <span class="folder-name" title={folder.Path}>{folder.Name}</span>
    </label>
  </div>

  <!-- Children (lazy loaded) -->
  {#if isExpanded}
    <div class="children" transition:slide={{ duration: 150 }}>
      {#if loading}
        <div class="loading-hint">Loading...</div>
      {:else if children && children.length > 0}
        {#each children as child (child.Path)}
          <svelte:self folder={child} depth={depth + 1} />
        {/each}
      {:else}
        <div class="empty-hint">No subfolders</div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .node {
    width: 100%;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    padding: 0.3rem 0.4rem;
    padding-left: calc(0.4rem + var(--depth) * 1.1rem);
    border-radius: 0.125rem;
    transition: background 0.1s ease;
  }
  .row:hover {
    background: rgba(255, 255, 255, 0.04);
  }

  /* Expand button */
  .expand-btn {
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    color: #555;
    display: flex;
    align-items: center;
    flex-shrink: 0;
    width: 14px;
    height: 14px;
    transition: color 0.15s;
  }
  .expand-btn:hover {
    color: #aaa;
  }

  .expand-spacer {
    display: inline-block;
    width: 14px;
    flex-shrink: 0;
  }

  .chevron {
    transition: transform 0.15s ease;
  }
  .chevron.open {
    transform: rotate(90deg);
  }

  /* Checkbox + label */
  .folder-label {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    cursor: pointer;
    flex: 1;
    min-width: 0;
  }

  .folder-checkbox {
    appearance: none;
    -webkit-appearance: none;
    width: 13px;
    height: 13px;
    border: 1px solid #3a3a3a;
    border-radius: 2px;
    background: transparent;
    cursor: pointer;
    flex-shrink: 0;
    position: relative;
    transition: border-color 0.15s ease, background 0.15s ease;
  }
  .folder-checkbox:checked {
    background: #bfc8ca;
    border-color: #bfc8ca;
  }
  .folder-checkbox:checked::after {
    content: '';
    position: absolute;
    left: 2px;
    top: 0px;
    width: 5px;
    height: 8px;
    border: 1.5px solid #111;
    border-top: none;
    border-left: none;
    transform: rotate(45deg);
  }

  .folder-name {
    font-family: 'Inter', sans-serif;
    font-size: 0.71rem;
    color: #c0c0c0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* Children container */
  .children {
    width: 100%;
  }

  .loading-hint,
  .empty-hint {
    font-family: 'Inter', sans-serif;
    font-size: 0.62rem;
    color: #444;
    padding: 0.25rem 0.4rem;
    padding-left: calc(0.4rem + var(--depth) * 1.1rem + 1.8rem);
    font-style: italic;
  }
</style>
