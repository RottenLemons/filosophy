<script>
  import { onMount } from "svelte";
  import { fade } from "svelte/transition";
  import { SelectDirectory, StartIndexing, GetLastDirectory } from "$lib/wailsjs/go/main/App";

  let selectedDirectory = "";
  let isIndexing = false;
  let progress = 0;
  let statusMessage = "";

  onMount(async () => {
    // Preset the previously requested search path
    try {
      const lastDir = await GetLastDirectory();
      if (lastDir) {
        selectedDirectory = lastDir;
      }
    } catch (e) {
      console.error("Failed to load last directory:", e);
    }

    const w = /** @type {any} */ (window);
    if (w.runtime && w.runtime.EventsOn) {
      w.runtime.EventsOn("indexing_progress", (/** @type {number} */ p) => {
        // Prevent backward jumping
        if (p > progress || p === 0) {
          progress = p;
        }
      });
      w.runtime.EventsOn("indexing_status", (/** @type {string} */ s) => {
        statusMessage = s;
        if (s === "Indexing complete.") {
          // Keep it visible for a moment then fade out
          setTimeout(() => {
            isIndexing = false;
            progress = 0;
            statusMessage = "";
          }, 2000);
        }
      });
    }
  });

  async function handleSelect() {
    try {
      const dir = await SelectDirectory();
      if (dir) {
        selectedDirectory = dir;
      }
    } catch (e) {
      console.error(e);
    }
  }

  async function handleStart() {
    if (!selectedDirectory || isIndexing) return;
    isIndexing = true;
    progress = 0;
    statusMessage = "Starting indexer...";
    try {
      await StartIndexing(selectedDirectory);
    } catch (e) {
      console.error(e);
      isIndexing = false;
    }
  }
</script>

<div class="indexer-inline">
  <div class="controls">
    <button class="btn-primary" on:click={handleSelect}>Select Folder</button>
    <button class="btn-ghost" on:click={handleStart} disabled={!selectedDirectory || isIndexing}>Begin Indexing</button>
  </div>

  {#if selectedDirectory}
    <div class="path-label" title={selectedDirectory}>
      {selectedDirectory}
    </div>
  {/if}

  {#if isIndexing}
    <div class="progress-wrapper" transition:fade={{ duration: 300 }}>
      <div class="status-label">{statusMessage}</div>
      <div class="progress-track">
        <div class="progress-fill" style="width: {progress}%"></div>
      </div>
    </div>
  {/if}
</div>

<style>
  .indexer-inline {
    display: flex;
    align-items: center;
    gap: 1.5rem;
    padding: 0.25rem 0;
    width: 100%;
    /* No borders, transparent background */
  }

  .controls {
    display: flex;
    gap: 0.75rem;
    align-items: center;
    flex-shrink: 0;
  }

  .btn-primary {
    /* Primary (#bfc8ca) background, on_primary (#394243) text, roundness-sm (0.125rem) */
    background-color: #bfc8ca;
    color: #394243;
    border-radius: 0.125rem;
    padding: 0.4rem 1rem;
    font-family: 'Inter', sans-serif;
    font-weight: 600;
    font-size: 0.75rem;
    border: none;
    cursor: pointer;
    transition: background-color 0.2s ease, opacity 0.2s ease;
  }
  .btn-primary:hover {
    opacity: 0.9;
  }

  .btn-ghost {
    /* Ghost style (20% outline variant) */
    background-color: transparent;
    color: #bfc8ca;
    border: 1px solid rgba(191, 200, 202, 0.2);
    border-radius: 0.125rem;
    padding: 0.4rem 1rem;
    font-family: 'Inter', sans-serif;
    font-weight: 500;
    font-size: 0.75rem;
    cursor: pointer;
    transition: background-color 0.2s ease;
  }
  .btn-ghost:hover:not(:disabled) {
    background-color: rgba(191, 200, 202, 0.05);
  }
  .btn-ghost:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .path-label {
    /* label-md */
    font-family: 'Inter', sans-serif;
    font-size: 0.7rem;
    color: #888;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 300px;
    letter-spacing: 0.05em;
  }

  .progress-wrapper {
    display: flex;
    align-items: center;
    gap: 1rem;
    flex-grow: 1;
  }

  .progress-track {
    /* ultra-thin 2px div */
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

  .status-label {
    font-family: 'Inter', sans-serif;
    font-size: 0.65rem;
    text-transform: uppercase;
    color: #acabab;
    white-space: nowrap;
    min-width: 100px;
    text-align: right;
  }
</style>
