<script>
  import SearchIcon from "carbon-icons-svelte/lib/Search.svelte";
  import DocumentIcon from "carbon-icons-svelte/lib/Document.svelte";
  import Input from "$lib/components/Input.svelte";
  // Assuming Button might not be used if we strictly follow code.html, but let's keep it if needed.
  // Actually code.html uses a standard <button> for filter.

  let searchQuery = "";
  let selectedFile = null;
  let files = [];
  let searching = false;

  async function handleSearch() {
    if (!searchQuery.trim()) return;
    searching = true;
    try {
      const result = await window.go.main.App.Search(searchQuery);
      files = result || [];
      if (files.length > 0) {
        selectedFile = files[0];
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

  function formatBytes(bytes, decimals = 2) {
      if (!+bytes) return '0 Bytes'
      const k = 1024
      const dm = decimals < 0 ? 0 : decimals
      const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB']
      const i = Math.floor(Math.log(bytes) / Math.log(k))
      return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`
  }
</script>

<div class="h-screen flex flex-col bg-surface text-on-surface overflow-hidden">
  
  <!-- Header -->
  <header class="bg-surface text-on-surface flex justify-between items-center w-full px-6 py-4 z-50">
    <div class="flex items-center">
      <h1 class="text-2xl font-bold tracking-tight font-headline">Filosophy</h1>
    </div>
  </header>

  <main class="flex-1 flex overflow-hidden">
    
    <!-- Main Search Content -->
    <div class="flex-1 overflow-y-auto px-8 py-12 flex flex-col items-center scrollbar-custom">
      <div class="w-full max-w-3xl space-y-12">
        
        <!-- Search Section -->
        <div class="space-y-6">
          <div class="flex items-end gap-4 w-full border-b border-outline-variant focus-within:border-primary-container transition-all duration-300 pb-2">
            <div class="pb-1 text-outline">
                <SearchIcon size={24} />
            </div>
            <input 
              bind:value={searchQuery}
              on:keyup={onKeyUp}
              class="flex-1 bg-transparent border-none focus:ring-0 text-2xl font-headline placeholder:text-outline/40 pb-1 outline-none" 
              placeholder="Search the archive..." 
              type="text"
            />
            <button class="flex items-center gap-2 px-4 py-2 border border-on-surface hover:bg-surface-container-low transition-colors font-label text-xs uppercase tracking-widest group disabled:opacity-50" on:click={handleSearch} disabled={searching}>
              <span>{searching ? 'Wait' : 'Filter'}</span>
              <svg class="w-4 h-4 group-hover:rotate-180 transition-transform" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"></path></svg>
            </button>
          </div>
          
          <!-- Filter Badges -->
          <div class="flex flex-wrap gap-3">
            <div class="bg-on-surface text-surface px-3 py-1 flex items-center gap-2">
              <span class="font-label text-[10px] uppercase tracking-tighter">Query</span>
              <span class="font-label text-[10px] font-bold">ALL</span>
            </div>
          </div>
        </div>
        
        <!-- Results List -->
        <div class="space-y-1">
          <div class="font-label text-[10px] uppercase tracking-widest text-secondary mb-4 flex justify-between">
            <span>{files.length} Records Found</span>
            <span>Sorted by relevance</span>
          </div>
          
          {#if files.length > 0}
            <div class="flex flex-col gap-1 relative z-20">
              {#each files as file, i (file)}
                <!-- svelte-ignore a11y-click-events-have-key-events - Result Card -->
                <div 
                  class="group relative flex items-center justify-between p-6 cursor-pointer transition-all duration-200
                         {selectedFile === file ? 'bg-surface-container-lowest border-l-4 border-primary-container' : 'hover:bg-surface-container-low border-l-4 border-transparent hover:border-outline-variant'}"
                  on:click={() => selectedFile = file}
                  style="animation: slideFadeIn 0.3s ease-out forwards; animation-delay: {i * 20}ms; opacity: 0; transform: translateY(10px);"
                >
                  <div class="flex gap-6">
                    <div class="w-12 h-12 flex items-center justify-center {selectedFile === file ? 'bg-primary-container/10' : 'bg-on-surface/5'}">
                      <div class={selectedFile === file ? 'text-primary' : 'text-secondary'}>
                        <DocumentIcon size={24} />
                      </div>
                    </div>
                    <div class="space-y-1">
                      <h3 class="text-lg font-headline font-medium text-on-surface">{file.split('/').pop() || file.split('\\').pop() || file}</h3>
                      <p class="font-label text-[11px] text-secondary tracking-tight">{file}</p>
                      <div class="flex gap-4 pt-2">
                        <span class="font-label text-[10px] uppercase {selectedFile === file ? 'text-primary font-bold' : 'text-secondary'}">DOC</span>
                        <span class="font-label text-[10px] text-secondary">Accessed recently</span>
                      </div>
                    </div>
                  </div>
                </div>
              {/each}
            </div>
          {:else if searchQuery.trim() !== ''}
             <div class="p-8 text-center text-secondary font-headline italic">No relevant documents found.</div>
          {/if}
        </div>
        
      </div>
    </div>
    
    <!-- Document Viewer Side Panel -->
    {#if selectedFile}
      <aside class="w-[500px] bg-surface-container-low border-l border-outline-variant flex flex-col transition-all duration-300 transform translate-x-0">
        <div class="flex-1 overflow-y-auto p-10 space-y-8 bg-surface scrollbar-custom border-l border-outline-variant shadow-none">
          <header class="space-y-4">
            <div class="font-label text-[10px] text-primary uppercase font-bold tracking-[0.2em]">Document Preview</div>
            <h2 class="text-3xl font-headline font-bold leading-tight truncate">{selectedFile.split('/').pop() || selectedFile.split('\\').pop() || selectedFile}</h2>
            
            <div class="flex gap-6 border-y border-outline-variant py-4">
              <div class="space-y-1">
                <div class="font-label text-[9px] text-secondary uppercase">Path</div>
                <div class="font-headline text-sm truncate max-w-[200px]" title={selectedFile}>{selectedFile}</div>
              </div>
              <div class="space-y-1">
                <div class="font-label text-[9px] text-secondary uppercase">Status</div>
                <div class="font-headline text-sm">Indexed</div>
              </div>
            </div>
          </header>
          
          <div class="space-y-6 font-headline text-lg leading-relaxed text-on-surface opacity-50 text-center py-20">
            <DocumentIcon size={48} class="mx-auto text-secondary mb-4 opacity-30" />
            <p>File content preview will be loaded here.</p>
          </div>
        </div>
      </aside>
    {/if}
  </main>
</div>

<style>
  :global(.scrollbar-custom::-webkit-scrollbar) { width: 4px; }
  :global(.scrollbar-custom::-webkit-scrollbar-track) { background: #FAF5F5; }
  :global(.scrollbar-custom::-webkit-scrollbar-thumb) { background: #C3C5D9; }

  @keyframes slideFadeIn {
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }
</style>
