<script>
  import { Search } from "carbon-components-svelte";
  import SearchIcon from "carbon-icons-svelte/lib/Search.svelte";
  import DocumentIcon from "carbon-icons-svelte/lib/Document.svelte";
  import { onMount } from "svelte";

  let searchQuery = "";
  let selectedFile = null;
  let files = [];
  let searching = false;

  async function handleSearch() {
    if (!searchQuery.trim()) return;
    searching = true;
    try {
      // Assuming the Go backend method is bound to window.go.main.App.Search
      const result = await window.go.main.App.Search(searchQuery);
      files = result || [];
    } catch (error) {
      console.error("Search failed:", error);
      files = [];
    }
    searching = false;
  }

  // Search on Enter key press
  function onKeyUp(e) {
    if (e.key === "Enter") {
      handleSearch();
    }
  }
</script>

<div class="app-container">
  <aside class="sidebar">
    <div class="header">
      <h2>File Search</h2>
      <Search bind:value={searchQuery} placeholder="Search file contents..." on:keyup={onKeyUp} />
      <button on:click={handleSearch} disabled={searching} class="search-button">
        {searching ? 'Searching...' : 'Search'}
      </button>
    </div>

    <div class="sidebar-content">
      {#if files.length > 0}
        <ul class="file-list">
          {#each files as file (file)}
            <li class="file-item" on:click={() => selectedFile = file}>
              <DocumentIcon size={16} class="icon" />
              <span class="file-path">{file}</span>
            </li>
          {/each}
        </ul>
      {:else if searchQuery.trim() !== ''}
        <div class="empty-state">
          <SearchIcon size={32} class="icon" />
          <p>No results found</p>
        </div>
      {:else}
        <div class="empty-state">
          <SearchIcon size={32} class="icon" />
          <p>Enter a search query to find files</p>
        </div>
      {/if}
    </div>
  </aside>

  <main class="detail-view">
    {#if selectedFile}
      <div class="file-preview">
        <h3>{selectedFile}</h3>
        <!-- TODO: implement file content preview -->
      </div>
    {:else}
      <div class="empty-state">
        <DocumentIcon size={64} class="icon" />
        <p>Select a file to view its contents</p>
      </div>
    {/if}
  </main>
</div>

<style>
  .app-container {
    display: flex;
    height: calc(100vh - 3rem);
    margin: -1rem;
  }

  .sidebar {
    width: 400px;
    background: white;
    border-right: 1px solid #e0e0e0;
    display: flex;
    flex-direction: column;
  }

  .header {
    padding: 1.5rem;
    border-bottom: 1px solid #f4f4f4;
  }

  h2 {
    font-size: 1.25rem;
    font-weight: 400;
    margin-bottom: 1rem;
  }

  .search-button {
    margin-top: 1rem;
    padding: 0.5rem 1rem;
    background-color: #0f62fe;
    color: white;
    border: none;
    border-radius: 2px;
    cursor: pointer;
  }

  .search-button:disabled {
    background-color: #8d8d8d;
    cursor: not-allowed;
  }

  .sidebar-content {
    flex: 1;
    overflow-y: auto;
    padding: 1rem;
  }

  .file-list {
    list-style: none;
    padding: 0;
    margin: 0;
  }

  .file-item {
    display: flex;
    align-items: center;
    padding: 0.75rem;
    border-bottom: 1px solid #f0f0f0;
    cursor: pointer;
  }

  .file-item:hover {
    background-color: #f5f5f5;
  }

  .file-path {
    margin-left: 0.5rem;
    font-size: 0.875rem;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .detail-view {
    flex: 1;
    background-color: #f4f4f4;
    padding: 2rem;
  }

  .empty-state {
    text-align: center;
    color: #8d8d8d;
  }

  :global(.icon) {
    margin-bottom: 1rem;
    fill: #c6c6c6;
  }
</style>
