<script>
  import SearchIcon from "carbon-icons-svelte/lib/Search.svelte";
  import DocumentIcon from "carbon-icons-svelte/lib/Document.svelte";
  import Button from "$lib/components/Button.svelte";
  import Input from "$lib/components/Input.svelte";

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
      if (files.length > 0) {
        selectedFile = files[0]; // Auto-select first result for better flow
      } else {
        selectedFile = null;
      }
    } catch (error) {
      console.error("Search failed:", error);
      files = [];
      selectedFile = null;
    }
    searching = false;
  }

  function onKeyUp(e) {
    if (e.key === "Enter") {
      handleSearch();
    }
  }
</script>

<div class="relative min-h-screen flex text-md-on-background overflow-hidden">
  
  <!-- Atmospheric Background Decor (MD3 Signature) -->
  <div class="absolute top-0 right-0 w-[800px] h-[800px] bg-md-secondary-container/30 blur-[100px] rounded-full mix-blend-multiply translate-x-1/3 -translate-y-1/3 pointer-events-none z-0"></div>
  <div class="absolute bottom-0 left-[20%] w-[600px] h-[600px] bg-md-primary/10 blur-[80px] rounded-full mix-blend-multiply pointer-events-none z-0"></div>

  <!-- Sidebar (Surface Container) -->
  <aside class="relative z-10 w-96 flex-shrink-0 flex flex-col bg-md-surface-container border-r border-md-outline/10 shadow-md-sm">
    <!-- Header -->
    <div class="p-6 md-lg:p-8 flex flex-col gap-4 border-b border-md-outline/10 z-20 bg-md-surface-container">
      <h2 class="text-[2rem] font-medium leading-tight text-md-on-background">File Search</h2>
      
      <div class="flex flex-col gap-3 mt-2">
        <Input 
          bind:value={searchQuery} 
          placeholder="Search file contents..." 
          on:keyup={onKeyUp}
        >
          <div slot="leadingIcon">
            <SearchIcon size={20} />
          </div>
        </Input>
        
        <Button 
          variant="primary" 
          disabled={searching} 
          on:click={handleSearch}
          class="w-full"
        >
          {searching ? 'Searching...' : 'Search Files'}
        </Button>
      </div>
    </div>

    <!-- Results List -->
    <div class="flex-1 overflow-y-auto w-full p-4 custom-scrollbar">
      {#if files.length > 0}
        <ul class="flex flex-col gap-2 relative z-20">
          {#each files as file (file)}
            <!-- svelte-ignore a11y-click-events-have-key-events - File Item (Tonal Card) -->
            <li 
              class="group flex items-center gap-3 p-4 rounded-md-large cursor-pointer transition-all duration-300 ease-md-emphasized 
                     {selectedFile === file ? 'bg-md-primary-container text-md-on-primary-container shadow-md-sm' : 'hover:bg-md-primary/10 hover:shadow-md-sm text-md-on-surface'}"
              on:click={() => selectedFile = file}
            >
              <div class="flex-shrink-0 transition-transform duration-300 ease-md-emphasized group-hover:scale-110 group-active:scale-95 {selectedFile === file ? 'text-md-primary' : 'text-md-on-surface-variant'}">
                <DocumentIcon size={20} />
              </div>
              <span class="text-sm font-medium truncate flex-1">{file}</span>
            </li>
          {/each}
        </ul>
      {:else if searchQuery.trim() !== ''}
        <div class="flex flex-col items-center justify-center h-full opacity-60 relative z-20">
          <div class="mb-4 bg-md-secondary-container p-4 rounded-full text-md-on-secondary-container">
            <SearchIcon size={32} />
          </div>
          <p class="text-base font-medium text-md-on-surface-variant">No results found</p>
        </div>
      {:else}
        <div class="flex flex-col items-center justify-center h-full opacity-60 relative z-20">
          <div class="mb-4 bg-md-primary/10 p-4 rounded-full text-md-primary">
            <SearchIcon size={32} />
          </div>
          <p class="text-base font-medium text-md-on-surface-variant text-center px-4">Enter a search query to find files</p>
        </div>
      {/if}
    </div>
  </aside>

  <!-- Detail View (Main Surface) -->
  <main class="relative z-10 flex-1 flex flex-col p-6 md:p-8 overflow-y-auto">
    {#if selectedFile}
      <div class="bg-md-surface-container-low rounded-md-2xl shadow-md-sm p-8 flex-1 flex flex-col ring-1 ring-md-outline/5 transition-all duration-300 ease-md-emphasized hover:shadow-md-lg">
        <div class="flex items-center gap-4 mb-8">
          <div class="bg-md-primary-container text-md-on-primary-container p-3 rounded-full flex-shrink-0">
             <DocumentIcon size={24} />
          </div>
          <h3 class="text-2xl font-medium truncate max-w-full text-md-on-background">{selectedFile}</h3>
        </div>
        
        <div class="flex-1 bg-md-background rounded-md-large p-6 border border-md-outline/10 font-mono text-sm text-md-on-surface-variant overflow-y-auto shadow-inner relative">
           <div class="absolute inset-x-0 top-0 h-4 bg-gradient-to-b from-black/5 to-transparent pointer-events-none rounded-t-md-large"></div>
          <!-- TODO: implement file content preview inside here -->
          <div class="flex flex-col items-center justify-center h-full opacity-50">
             <p>File content preview will be loaded here.</p>
          </div>
        </div>
      </div>
    {:else}
      <div class="flex-1 flex flex-col items-center justify-center opacity-70">
        <div class="mb-6 bg-md-surface-container shadow-md-base p-6 rounded-[2rem] text-md-primary transform transition-transform duration-500 hover:scale-105 hover:rotate-3">
          <DocumentIcon size={48} />
        </div>
        <p class="text-xl font-medium text-md-on-surface-variant mb-2">Select a file</p>
        <p class="text-sm text-md-outline text-center max-w-xs">Click on any file in the sidebar to view its contents right here.</p>
      </div>
    {/if}
  </main>
</div>

<style>
  /* Custom scrollbar to match MD3 aesthetics */
  .custom-scrollbar::-webkit-scrollbar {
    width: 6px;
  }
  .custom-scrollbar::-webkit-scrollbar-track {
    background: transparent;
  }
  .custom-scrollbar::-webkit-scrollbar-thumb {
    background: theme('colors.md.outline-variant');
    border-radius: 9999px;
  }
  .custom-scrollbar::-webkit-scrollbar-thumb:hover {
    background: theme('colors.md.outline');
  }
</style>
