<script>
  import { Search } from "carbon-components-svelte";
  import SearchIcon from "carbon-icons-svelte/lib/Search.svelte";
  import DocumentIcon from "carbon-icons-svelte/lib/Document.svelte";

  let searchQuery = "";
  let selectedFile = null;
</script>

<div class="app-container">
  <aside class="sidebar">
    <div class="header">
      <h2>File Search</h2>
      <Search bind:value={searchQuery} placeholder="Search file contents..." />
    </div>

    <div class="sidebar-content">
      <div class="empty-state">
        <SearchIcon size={32} class="icon" />
        <p>Enter a search query to find files</p>
      </div>
    </div>
  </aside>

  <main class="detail-view">
    <div class="empty-state">
      <DocumentIcon size={64} class="icon" />
      <p>Select a file to view its contents</p>
    </div>
  </main>
</div>

<style>
  .app-container {
    display: flex;
    height: calc(100vh - 3rem); /* Adjusting for Carbon Content padding */
    margin: -1rem; /* Offsetting default Carbon padding for flush edges */
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

  .sidebar-content, .detail-view {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .detail-view {
    background-color: #f4f4f4;
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