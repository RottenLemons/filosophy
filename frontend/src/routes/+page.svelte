<script>
  import { slide, fade } from 'svelte/transition';
  import { onMount, onDestroy } from 'svelte';
  import SearchIcon from "carbon-icons-svelte/lib/Search.svelte";
  import DocumentIcon from "carbon-icons-svelte/lib/Document.svelte";
  import ImageIcon from "carbon-icons-svelte/lib/Image.svelte";
  import PDFIcon from "carbon-icons-svelte/lib/PDF.svelte";
  import { Settings as SettingsIcon, XCircle, AlertCircle, X, Activity, MessageSquare } from 'lucide-svelte';
  import { Search, OpenFileNative } from "$lib/wailsjs/go/main/App";
  import ThemeToggle from '$lib/components/ThemeToggle.svelte';
  import SettingsModal from '$lib/components/SettingsModal.svelte';
  import Chat from '$lib/components/Chat.svelte';
  import APIConfirmDialog from '$lib/components/APIConfirmDialog.svelte';
  import { indexingStatus } from '../stores/indexer';

  let showSettings = false;
  let showChat = false;

  // ── API confirm dialog ──────────────────────────────────────────────────────
  /** @type {{ id: string; query: string } | null} */
  let apiConfirmRequest = null;
  // Queue: if multiple requests arrive before user responds, line them up
  /** @type {{ id: string; query: string }[]} */
  let apiConfirmQueue = [];

  let apiConfirmUnsubscribe = /** @type {(() => void) | null} */ (null);

  onMount(() => {
    if (window['runtime']?.EventsOn) {
      apiConfirmUnsubscribe = window['runtime'].EventsOn('api_confirm_request', (data) => {
        if (!data?.id || !data?.query) return;
        if (apiConfirmRequest === null) {
          apiConfirmRequest = data;
        } else {
          apiConfirmQueue = [...apiConfirmQueue, data];
        }
      });
    }
  });

  onDestroy(() => {
    apiConfirmUnsubscribe?.();
  });

  function handleConfirmReply(e) {
    const { id, allowed } = e.detail;
    window['runtime']?.EventsEmit('api_confirm_reply', { id, allowed });
    // Show next queued request if any
    if (apiConfirmQueue.length > 0) {
      const [next, ...rest] = apiConfirmQueue;
      apiConfirmRequest = next;
      apiConfirmQueue = rest;
    } else {
      apiConfirmRequest = null;
    }
  }

  /** @type {string} */
  let searchQuery = "";
  /** @type {any} */
  let selectedFile = null;
  /** @type {any[]} */
  let files = [];
  /** @type {boolean} */
  let searching = false;
  /** @type {string | null} */
  let searchErrorMsg = null;

  /** @type {number} */
  let currentSearchTicket = 0;
  /** @type {number} */
  let renderLimit = 50;

  /** @type {boolean} */
  let previewImageError = false;

  let isFilterOpen = false;
  
  const loadingMessages = [
    "Searching for Untitled_final_FINAL_v3.pdf...",
    "Attempting to decipher your file naming conventions...",
    "Judging the contents of your Downloads folder...",
    "Translating your typos into machine logic...",
    "Interrogating the local language model...",
    "Melting your CPU to find a single PDF...",
    "Coercing the vector database...",
    "Going exactly as fast as your RAM currently allows...",
    "Forcing the neural network to read your documents...",
    "Doing actual math. Hold on..."
  ];
  let currentMessageIndex = 0;
  /** @type {any} */
  let loadingInterval;

  $: {
    if (searching) {
      if (!loadingInterval) {
        currentMessageIndex = Math.floor(Math.random() * loadingMessages.length);
        loadingInterval = setInterval(() => {
          let nextIndex;
          do {
            nextIndex = Math.floor(Math.random() * loadingMessages.length);
          } while (nextIndex === currentMessageIndex);
          currentMessageIndex = nextIndex;
        }, 1800);
      }
    } else {
      clearInterval(loadingInterval);
      loadingInterval = null;
    }
  }

  // Task 4: Reset image error on file selection change
  $: if (selectedFile) {
    previewImageError = false;
  }

  let filterType = 'All'; // Options: 'All', 'PDF', 'DOCX', 'XLSX', 'CSV', 'TXT', 'MD', 'JPG', 'PNG', 'Other'
  let filterDate = 'Anytime'; // Options: 'Anytime', 'Last 7 Days', 'Last 30 Days', 'This Year'
  let filterSize = 'Any'; // Options: 'Any', '< 1 MB', '1 MB - 10 MB', '10 MB - 100 MB', '> 100 MB'

  $: isFilterActive = filterType !== 'All' || filterDate !== 'Anytime' || filterSize !== 'Any';

  $: filteredFiles = (() => {
    const now = new Date();
    return files.filter(file => {
      // 1. Type Match
      const knownTypes = ['PDF', 'DOCX', 'XLSX', 'CSV', 'TXT', 'MD', 'JPG', 'PNG'];
      const type = getFileType(file.Path);
      let matchType = false;
      if (filterType === 'All') {
        matchType = true;
      } else if (filterType === 'Other') {
        matchType = !knownTypes.includes(type);
      } else {
        matchType = type === filterType;
      }

      // 2. Size Match (file.Size is in bytes)
      let matchSize = true;
      const sizeMB = file.Size / (1024 * 1024);
      if (filterSize === '< 1 MB') matchSize = sizeMB < 1;
      else if (filterSize === '1 MB - 10 MB') matchSize = sizeMB >= 1 && sizeMB <= 10;
      else if (filterSize === '10 MB - 100 MB') matchSize = sizeMB >= 10 && sizeMB <= 100;
      else if (filterSize === '> 100 MB') matchSize = sizeMB > 100;

      // 3. Date Match (file.Modified is ISO string)
      let matchDate = true;
      if (filterDate !== 'Anytime' && file.Modified) {
        const modifiedDate = new Date(file.Modified);
        if (filterDate === 'Last 7 Days') {
          matchDate = (now.getTime() - modifiedDate.getTime()) <= 7 * 24 * 60 * 60 * 1000;
        } else if (filterDate === 'Last 30 Days') {
          matchDate = (now.getTime() - modifiedDate.getTime()) <= 30 * 24 * 60 * 60 * 1000;
        } else if (filterDate === 'This Year') {
          matchDate = modifiedDate.getFullYear() === now.getFullYear();
        }
      }

      return matchType && matchSize && matchDate;
    });
  })();

  /** @param {HTMLElement} node */
  function clickOutside(node) {
    /** @param {MouseEvent} event */
    const handleClick = (/** @type {MouseEvent} */ event) => {
      const target = /** @type {Node} */ (event.target);
      if (node && !node.contains(target) && !event.defaultPrevented) {
        node.dispatchEvent(new CustomEvent('click_outside', { detail: node }));
      }
    }
    document.addEventListener('click', handleClick, true);
    return { destroy() { document.removeEventListener('click', handleClick, true); } }
  }

  /** @type {any} */
  let searchTimeout;

  // Task 1 & Task 2: Ticket-based concurrency and Reset renderLimit
  async function performingSearch() {
    if (!searchQuery.trim()) {
      files = [];
      selectedFile = null;
      searching = false;
      searchErrorMsg = null;
      renderLimit = 50;
      return;
    }

    currentSearchTicket++;
    const localTicket = currentSearchTicket;
    
    try {
      const result = await Search(searchQuery);
      if (localTicket === currentSearchTicket) {
        files = result || [];
        selectedFile = null; 
        searchErrorMsg = null;
      }
    } catch (error) {
      if (localTicket === currentSearchTicket) {
        console.error("Search failed:", error);
        searchErrorMsg = String(error);
        files = [];
        selectedFile = null;
      }
    } finally {
      if (localTicket === currentSearchTicket) {
        searching = false;
      }
    }
  }

  function debouncedSearch() {
    clearTimeout(searchTimeout);
    if (!searchQuery.trim()) {
        files = [];
        selectedFile = null;
        searching = false;
        searchErrorMsg = null;
        renderLimit = 50;
        return;
    }
    searchTimeout = setTimeout(performingSearch, 300);
  }

  /** @param {KeyboardEvent} e */
  function onKeyUp(e) {
    if (e.key === "Enter") {
      clearTimeout(searchTimeout);
      performingSearch();
    }
  }

  /** @param {KeyboardEvent} e */
  function handleResultKeydown(e, file) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      selectedFile = file;
    }
  }

  function cancelSearch() {
    clearTimeout(searchTimeout);
    currentSearchTicket++; // Invalidate pending search
    searching = false;
    searchErrorMsg = null;
  }

  function loadMore() {
    renderLimit += 50;
  }

  /** @param {number} score */
  function formatScore(score) {
    let percentage = Math.round(score * 100);
    if (percentage < 1) percentage = 1;
    if (percentage > 100) percentage = 100;
    return percentage + "%";
  }

  /** @param {number} bytes */
  function formatSize(bytes) {
    if (bytes === 0 || !bytes) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
  }

  /** @param {string} isoString */
  function formatDate(isoString) {
    if (!isoString) return 'Unknown';
    const d = new Date(isoString);
    if (isNaN(d.getTime())) return 'Unknown';
    return d.toLocaleDateString();
  }

  /** @param {string} path */
  function getFileType(path) {
    if (!path) return 'DOC';
    const parts = path.split('.');
    if (parts.length > 1) {
      return (parts.pop() || '').toUpperCase();
    }
    return 'DOC';
  }

  /** @param {string} path */
  function getFileName(path) {
    if (!path) return "";
    return path.split(/[/\\]/).pop();
  }

  /** @param {string} path */
  function getFileCategory(path) {
    if (!path) return 'other';
    const ext = (path.split('.').pop() || '').toLowerCase();
    if (['pdf'].includes(ext)) return 'pdf';
    if (['png', 'jpg', 'jpeg', 'gif', 'webp'].includes(ext)) return 'image';
    if (['txt', 'md', 'csv', 'json', 'log'].includes(ext)) return 'text';
    return 'other';
  }

  let textContent = "";
  $: if (selectedFile && selectedFile.Path && getFileCategory(selectedFile.Path) === 'text') {
    fetch('/loadfile/' + encodeURIComponent(selectedFile.Path || ''))
      .then(res => res.text())
      .then(text => textContent = text)
      .catch(err => {
        console.error("Failed to load text:", err);
        textContent = "Error loading document.";
      });
  } else {
    textContent = "";
  }
</script>

<div class="h-screen flex flex-col bg-[#FAF9F6] dark:bg-[#111] text-slate-800 dark:text-gray-100 overflow-hidden font-sans relative">
  <div class="absolute top-6 right-8 flex items-center gap-4 z-[9999]">
    <button
      on:click={() => showChat = !showChat}
      class="p-2 rounded-full transition-all duration-300 hover:opacity-100 {showChat ? 'opacity-100 text-blue-600 dark:text-blue-400' : 'opacity-60 text-slate-900 dark:text-gray-100'} focus:outline-none cursor-pointer"
      style="--wails-draggable:no-drag; -webkit-app-region:no-drag; pointer-events:auto;"
      aria-label="Toggle Chat"
    >
      <MessageSquare class="w-5 h-5" />
    </button>
    <button
      on:click={() => showSettings = true}
      class="p-2 rounded-full transition-all duration-300 hover:opacity-100 opacity-60 text-slate-900 dark:text-gray-100 focus:outline-none group cursor-pointer"
      style="--wails-draggable:no-drag; -webkit-app-region:no-drag; pointer-events:auto;"
      aria-label="Open Settings"
    >
      <SettingsIcon class="w-5 h-5 transition-transform group-hover:rotate-45" />
    </button>
    <ThemeToggle />
  </div>

  <SettingsModal bind:show={showSettings} on:close={() => showSettings = false} />
  
  <header class="w-full px-12 py-6 z-50 shrink-0">
    <h1 class="text-3xl font-serif text-slate-900 dark:text-gray-100 tracking-tight">Filosophy</h1>
  </header>

  <!-- Task 3: Desktop layout lg:flex-row -->
  <main class="flex-1 flex flex-col lg:flex-row overflow-hidden relative">
    
    <div class="flex-1 overflow-y-auto px-12 pb-12 flex flex-col scrollbar-custom border-r border-gray-100 dark:border-[#2a2a2a] transition-all duration-300">
      <div class="w-full max-w-5xl mx-auto space-y-8 pr-6">
        
        <div class="space-y-4">
          <div class="flex items-center gap-4 w-full border-b border-gray-300 dark:border-[#2a2a2a] pb-3 transition-colors focus-within:border-blue-500">
            <div class="text-gray-400">
              <SearchIcon size={24} />
            </div>
            <input 
              bind:value={searchQuery}
              on:input={debouncedSearch}
              on:keyup={onKeyUp}
              class="flex-1 bg-transparent border-none focus:ring-0 text-3xl font-serif placeholder:text-gray-200 dark:placeholder:text-gray-600 pb-1 outline-none text-slate-700 dark:text-gray-100" 
              placeholder="Search the collection..." 
              type="text"
            />
            <div class="relative" use:clickOutside on:click_outside={() => isFilterOpen = false}>
              <button 
                on:click={() => isFilterOpen = !isFilterOpen}
                class="flex items-center gap-2 px-3 py-1.5 border border-gray-300 dark:border-[#2a2a2a] hover:border-gray-400 text-gray-600 dark:text-gray-400 transition-colors text-[10px] font-semibold uppercase tracking-widest disabled:opacity-50 rounded-none bg-white dark:bg-[#111]" 
                disabled={searching}
              >
                <span>Filter</span>
                <svg class="w-3 h-3 pt-0.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"></path></svg>
              </button>

              {#if isFilterOpen}
                <div class="absolute right-0 top-full mt-2 w-72 bg-white dark:bg-[#111] border border-gray-200 dark:border-[#2a2a2a] shadow-xl z-50 rounded-none p-4 flex flex-col gap-6" transition:slide={{ duration: 150 }}>
                  <div class="space-y-2">
                    <span class="text-[9px] font-bold uppercase tracking-widest text-gray-400">File Type</span>
                    <div class="grid grid-cols-2 gap-1">
                      {#each ['All', 'PDF', 'DOCX', 'XLSX', 'CSV', 'TXT', 'MD', 'JPG', 'PNG', 'Other'] as type}
                        <button 
                          on:click={() => filterType = type}
                          class="px-2 py-1.5 text-left text-[10px] uppercase tracking-wider transition-colors rounded-none
                                 {filterType === type ? 'bg-slate-900 text-white font-bold dark:bg-white dark:text-black' : 'hover:bg-gray-100 dark:hover:bg-[#222] text-gray-600 dark:text-gray-400 border border-transparent'}"
                        >
                          {type}
                        </button>
                      {/each}
                    </div>
                  </div>

                  <div class="space-y-2">
                    <span class="text-[9px] font-bold uppercase tracking-widest text-gray-400">Timeframe</span>
                    <div class="flex flex-col gap-1">
                      {#each ['Anytime', 'Last 7 Days', 'Last 30 Days', 'This Year'] as date}
                        <button 
                          on:click={() => filterDate = date}
                          class="px-2 py-1.5 text-left text-[10px] uppercase tracking-wider transition-colors rounded-none
                                 {filterDate === date ? 'bg-slate-900 text-white font-bold dark:bg-white dark:text-black' : 'hover:bg-gray-100 dark:hover:bg-[#222] text-gray-600 dark:text-gray-400 border border-transparent'}"
                        >
                          {date}
                        </button>
                      {/each}
                    </div>
                  </div>

                  <div class="space-y-2">
                    <span class="text-[9px] font-bold uppercase tracking-widest text-gray-400">File Size</span>
                    <div class="flex flex-col gap-1">
                      {#each ['Any', '< 1 MB', '1 MB - 10 MB', '10 MB - 100 MB', '> 100 MB'] as size}
                        <button 
                          on:click={() => filterSize = size}
                          class="px-2 py-1.5 text-left text-[10px] uppercase tracking-wider transition-colors rounded-none
                                 {filterSize === size ? 'bg-slate-900 text-white font-bold dark:bg-white dark:text-black' : 'hover:bg-gray-100 dark:hover:bg-[#222] text-gray-600 dark:text-gray-400 border border-transparent'}"
                        >
                          {size}
                        </button>
                      {/each}
                    </div>
                  </div>
                </div>
              {/if}
            </div>
          </div>
          
          {#if isFilterActive}
            <div class="flex flex-wrap gap-2 pt-1">
              {#if filterType !== 'All'}
                <button 
                  on:click={() => filterType = 'All'}
                  class="flex items-center gap-1.5 border px-2 py-1 bg-slate-900 text-white border-slate-900 dark:bg-white dark:text-black dark:border-white rounded-none hover:bg-slate-800 dark:hover:bg-gray-200 transition-colors group"
                >
                  <span class="text-[9px] font-bold uppercase tracking-widest">Type</span>
                  <span class="text-[9px] font-bold uppercase tracking-widest opacity-80">{filterType}</span>
                  <XCircle size={10} class="ml-1 opacity-60 group-hover:opacity-100" />
                </button>
              {/if}
              {#if filterDate !== 'Anytime'}
                <button 
                  on:click={() => filterDate = 'Anytime'}
                  class="flex items-center gap-1.5 border px-2 py-1 bg-transparent text-gray-500 dark:text-gray-500 border-gray-300 dark:border-[#2a2a2a] rounded-none hover:bg-gray-50 dark:hover:bg-[#1a1a1a] transition-colors group"
                >
                  <span class="text-[9px] font-bold uppercase tracking-widest">Date</span>
                  <span class="text-[9px] font-bold uppercase tracking-widest text-gray-700 dark:text-gray-400">{filterDate}</span>
                  <XCircle size={10} class="ml-1 opacity-40 group-hover:opacity-100" />
                </button>
              {/if}
              {#if filterSize !== 'Any'}
                <button 
                  on:click={() => filterSize = 'Any'}
                  class="flex items-center gap-1.5 border px-2 py-1 bg-transparent text-gray-500 dark:text-gray-500 border-gray-300 dark:border-[#2a2a2a] rounded-none hover:bg-gray-50 dark:hover:bg-[#1a1a1a] transition-colors group"
                >
                  <span class="text-[9px] font-bold uppercase tracking-widest">Size</span>
                  <span class="text-[9px] font-bold uppercase tracking-widest text-gray-700 dark:text-gray-400">{filterSize}</span>
                  <XCircle size={10} class="ml-1 opacity-40 group-hover:opacity-100" />
                </button>
              {/if}
            </div>
          {/if}
        </div>
        
        <div class="space-y-4 pt-4">
          {#if filteredFiles.length > 0}
            <div class="flex justify-between items-center text-[10px] font-bold uppercase tracking-widest text-gray-400 dark:text-gray-500 pb-2 border-b border-gray-100 dark:border-[#2a2a2a]">
              <span>{filteredFiles.length} Records Found</span>
              <span>Sorted by relevance</span>
            </div>
          {/if}
          
          {#if searchErrorMsg}
            <div class="p-8 border {searchErrorMsg.includes('backend engine not initialized') ? 'border-amber-400 dark:border-amber-900/50 bg-amber-50/50 dark:bg-amber-950/20' : 'border-red-200 dark:border-red-900/30 bg-red-50/50 dark:bg-red-950/10'} rounded-none flex flex-col items-center gap-4 text-center" transition:fade>
              {#if searchErrorMsg.includes('backend engine not initialized')}
                <Activity class="text-amber-500 animate-pulse" size={32} />
                <div class="space-y-1">
                  <h3 class="text-amber-900 dark:text-amber-400 font-bold uppercase tracking-widest text-xs">Engine Offline</h3>
                  <p class="text-[11px] text-amber-700 dark:text-amber-500/70 italic max-w-sm">The local AI search backend failed to boot. This usually happens if model files are missing or the database is locked. Please check <code>filosophy.log</code> and restart.</p>
                </div>
              {:else}
                <AlertCircle class="text-red-500" size={32} />
                <div class="space-y-1">
                  <h3 class="text-red-900 dark:text-red-400 font-bold uppercase tracking-widest text-xs">Search Failed</h3>
                  <p class="text-[11px] text-red-700 dark:text-red-500/70 italic">An error occurred while communicating with the search engine.</p>
                  <p class="text-[9px] font-mono text-red-400 mt-2">{searchErrorMsg}</p>
                </div>
                <button 
                  on:click={performingSearch}
                  class="px-6 py-2 bg-red-600 text-white text-[10px] font-bold uppercase tracking-widest hover:bg-red-700 transition-colors"
                >
                  Retry Search
                </button>
              {/if}
            </div>
          {:else if searching}
            <div class="p-12 flex flex-col justify-center items-center bg-transparent gap-8" transition:fade>
              <svg class="animate-spin" style="animation-duration: 6s;" width="80" height="80" viewBox="0 0 80 80" fill="none" stroke="#757575" stroke-width="8" stroke-linecap="square">
                <circle cx="40" cy="40" r="30" stroke-dasharray="140 48"></circle>
              </svg>
              <div class="h-8 relative w-full flex justify-center">
                {#key currentMessageIndex}
                  <span transition:fade={{duration: 600}} class="absolute text-[18px] text-[#acabab] text-center tracking-wide" style="font-family: 'Inter', sans-serif;">
                    {loadingMessages[currentMessageIndex]}
                  </span>
                {/key}
              </div>
              <button 
                on:click={cancelSearch}
                class="px-4 py-1.5 border border-gray-300 dark:border-[#2a2a2a] text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 text-[10px] font-bold uppercase tracking-widest transition-colors"
                aria-label="Cancel Search"
              >
                Cancel
              </button>
            </div>
          {:else if filteredFiles.length > 0}
            <div class="flex flex-col gap-6 relative z-20" role="listbox" aria-label="Search results">
              <!-- Task 2: renderLimit paging -->
              {#each filteredFiles.slice(0, renderLimit) as file, i (file.Path)}
                <div 
                  class="group relative flex items-center justify-between p-6 cursor-pointer bg-white dark:bg-transparent transition-all duration-200 border-l-4 shadow-sm outline-none focus:ring-2 focus:ring-blue-500 focus:ring-inset
                         {selectedFile === file ? 'bg-blue-50 border-blue-600 dark:bg-[#1a1a1a] dark:border-l-gray-400' : 'border-transparent hover:shadow hover:border-gray-200 dark:hover:border-[#2a2a2a] hover:bg-white dark:hover:bg-[#1a1a1a]'}"
                  on:click={() => selectedFile = file}
                  on:dblclick={() => OpenFileNative(file.Path)}
                  on:keydown={(e) => handleResultKeydown(e, file)}
                  tabindex="0"
                  role="option"
                  aria-selected={selectedFile === file}
                  style="animation: slideFadeIn 0.3s ease-out forwards; animation-delay: {i * 5}ms; opacity: 0; transform: translateY(10px);"
                >
                  <div class="flex gap-6 items-start">
                    <div class="w-12 h-12 shrink-0 flex items-center justify-center {selectedFile === file ? 'bg-blue-100 text-blue-600 dark:bg-[#1a1a1a] dark:text-gray-100' : 'bg-gray-100 dark:bg-[#1a1a1a] text-gray-400 dark:text-gray-500'}">
                      {#if getFileCategory(file.Path) === 'image'}
                        <ImageIcon size={24} />
                      {:else if getFileCategory(file.Path) === 'pdf'}
                        <PDFIcon size={24} />
                      {:else}
                        <DocumentIcon size={24} />
                      {/if}
                    </div>
                    <div class="space-y-1.5 mt-0.5">
                      <h3 class="text-xl font-serif font-bold text-slate-800 dark:text-gray-100 leading-tight">{getFileName(file.Path)}</h3>
                      <p class="text-[11px] text-gray-400 dark:text-gray-500 font-sans truncate max-w-sm">{file.Path}</p>
                      <div class="flex gap-4 pt-2">
                        <span class="text-[10px] font-bold uppercase tracking-widest {selectedFile === file ? 'text-blue-600 dark:text-white' : 'text-gray-500'}">{getFileType(file.Path)}</span>
                        <span class="text-[10px] font-semibold uppercase tracking-widest text-gray-400 dark:text-gray-500">{formatSize(file.Size)}</span>
                        <span class="text-[10px] font-semibold text-gray-400 dark:text-gray-500">Modified {formatDate(file.Modified)}</span>
                      </div>
                    </div>
                  </div>
                  <div class="flex flex-col items-end pr-4">
                    <div class="text-4xl font-bold {selectedFile === file ? 'text-blue-600 dark:text-white' : 'text-gray-300 dark:text-gray-600'}">{formatScore(file.Score)}</div>
                    <div class="text-[8px] font-bold uppercase tracking-widest text-gray-400 dark:text-gray-500 mt-1">Match</div>
                  </div>
                </div>
              {/each}

              {#if filteredFiles.length > renderLimit}
                <button 
                  on:click={loadMore}
                  class="w-full py-6 border-2 border-dashed border-gray-200 dark:border-[#2a2a2a] text-gray-400 dark:text-gray-500 hover:text-blue-500 hover:border-blue-500 dark:hover:text-blue-400 dark:hover:border-blue-400 font-bold uppercase tracking-widest text-[10px] transition-all"
                >
                  Load More Results ({filteredFiles.length - renderLimit} Remaining)
                </button>
              {/if}
            </div>
          {:else if searchQuery.trim() !== ''}
             <div class="p-12 text-center text-gray-400 font-serif text-lg italic bg-white/50 dark:bg-transparent border border-gray-200 dark:border-[#2a2a2a] border-dashed">
               No relevant documents found.
               {#if isFilterActive}
                 <div class="mt-2 text-xs not-italic font-sans font-semibold text-blue-500 dark:text-blue-400 uppercase tracking-widest">
                   Try clearing your active filters.
                 </div>
               {/if}
             </div>
          {/if}
        </div>
      </div>
    </div>
    
    <!-- Chat panel -->
    {#if showChat}
      <Chat
        contextFile={selectedFile}
        fileContent={textContent}
        on:close={() => showChat = false}
      />
    {/if}

    <!-- File preview — hidden when chat is open -->
    {#if selectedFile && !showChat}
      <aside
        transition:slide={{ axis: 'x', duration: 400 }}
        class="absolute inset-0 z-50 lg:relative lg:inset-auto lg:w-[40%] lg:min-w-[450px] bg-[#FAF9F6] dark:bg-[#111] border-l border-gray-200 dark:border-l-[#2a2a2a] flex flex-col shadow-2xl overflow-hidden"
      >
        <div class="flex-1 overflow-y-auto p-12 space-y-10 scrollbar-custom">
          <header class="space-y-6 relative">
            <!-- Close button serves as dismissal for overlay and sidebar -->
            <button 
              class="absolute top-0 right-0 p-2 text-gray-400 hover:text-slate-900 transition-colors bg-[#FAF9F6] dark:bg-[#111] dark:hover:text-gray-100 rounded-full shadow-sm"
              on:click={() => selectedFile = null}
              aria-label="Close Preview"
            >
              <X class="w-6 h-6" />
            </button>
            <div class="font-bold text-[9px] text-blue-600 dark:text-gray-300 uppercase tracking-widest">Key Match Found</div>
            <h2 class="text-4xl font-serif text-slate-900 dark:text-gray-100 font-bold leading-tight pr-12">{getFileName(selectedFile.Path)}</h2>
            
            <button 
              on:click={() => OpenFileNative(selectedFile.Path)}
              class="w-full py-4 bg-slate-900 dark:bg-white text-white dark:text-black font-bold uppercase tracking-[0.2em] text-xs hover:bg-slate-800 dark:hover:bg-gray-100 transition-all shadow-xl flex items-center justify-center gap-3"
            >
               <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                 <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14"></path>
               </svg>
               Open File
            </button>

            <div class="grid grid-cols-3 gap-6 border-y border-gray-200 dark:border-[#2a2a2a] py-6">
              <div class="space-y-1">
                <div class="text-[8px] text-gray-400 dark:text-gray-500 font-bold uppercase tracking-widest">Type</div>
                <div class="text-xs text-slate-800 dark:text-gray-100 font-semibold uppercase">{getFileType(selectedFile.Path)}</div>
              </div>
              <div class="space-y-1">
                <div class="text-[8px] text-gray-400 dark:text-gray-500 font-bold uppercase tracking-widest">Last Modified</div>
                <div class="text-xs text-slate-800 dark:text-gray-100 font-semibold">{formatDate(selectedFile.Modified)}</div>
              </div>
              <div class="space-y-1">
                <div class="text-[8px] text-gray-400 dark:text-gray-500 font-bold uppercase tracking-widest">Size</div>
                <div class="text-xs text-slate-800 dark:text-gray-100 font-semibold uppercase">{formatSize(selectedFile.Size)}</div>
              </div>
            </div>
          </header>
          
          <div class="flex-1 w-full h-full font-serif text-lg leading-relaxed text-slate-700 dark:text-gray-100 bg-white dark:bg-[#111] shadow-inner p-8 overflow-y-auto">
            {#if getFileCategory(selectedFile.Path) === 'image'}
              <div class="w-full h-full flex items-center justify-center bg-[#FAF9F6] dark:bg-[#111]">
                <!-- Task 4: Image Error Fallback -->
                {#if !previewImageError}
                  <img 
                    src={"/loadfile/" + encodeURIComponent(selectedFile.Path)} 
                    class="w-full h-full object-contain shadow-sm" 
                    alt={getFileName(selectedFile.Path)}
                    on:error={() => previewImageError = true}
                  />
                {:else}
                  <div class="flex flex-col items-center gap-4 text-gray-400">
                    <ImageIcon size={32} />
                    <p class="text-sm font-mono italic">Failed to load preview.</p>
                  </div>
                {/if}
              </div>
            {:else if getFileCategory(selectedFile.Path) === 'text'}
              <div class="max-w-prose mx-auto font-serif text-slate-800 dark:text-gray-100 leading-relaxed whitespace-pre-wrap">
                {textContent}
              </div>
            {:else if getFileCategory(selectedFile.Path) === 'pdf'}
              <iframe src={"/loadfile/" + encodeURIComponent(selectedFile.Path)} class="w-full h-full border-none bg-white dark:bg-[#111]" title="Document Preview"></iframe>
            {:else}
              <div class="w-full h-full flex flex-col items-center justify-center text-gray-400 dark:text-gray-500 font-mono text-sm space-y-4">
                <DocumentIcon size={32} />
                <p>Preview not available for this file type.</p>
                <div class="px-3 py-1 bg-gray-100 dark:bg-[#1a1a1a] rounded text-[10px] uppercase font-bold tracking-widest text-gray-500 dark:text-gray-400">
                  {getFileType(selectedFile.Path)}
                </div>
              </div>
            {/if}
          </div>
        </div>
      </aside>
    {/if}
    
  </main>
  
  <!-- Task 10: Persistent Engine Health Badge (UX Remediation H1) -->
  {#if searchErrorMsg !== 'backend engine not initialized'}
    <div 
      class="fixed bottom-6 right-6 z-50 flex flex-col items-end gap-2"
      transition:fade={{ duration: 300 }}
    >
      <div class="bg-white/80 dark:bg-black/50 backdrop-blur-md border border-gray-200/50 dark:border-white/10 px-4 py-2 rounded-2xl shadow-2xl flex items-center gap-3">
        <div class="relative flex items-center justify-center">
            <Activity class="w-4 h-4 {$indexingStatus.isIndexing ? 'text-blue-500 animate-pulse' : 'text-emerald-500 dark:text-emerald-400'}" />
            {#if $indexingStatus.isIndexing}
              <div class="absolute inset-0 bg-blue-500/20 blur-lg rounded-full animate-pulse"></div>
            {/if}
        </div>
        <div class="flex flex-col">
          <span class="text-[10px] font-black {$indexingStatus.isIndexing ? 'text-blue-600 dark:text-blue-400' : 'text-emerald-600 dark:text-emerald-500'} uppercase tracking-tighter leading-none mb-0.5">
            {$indexingStatus.isIndexing ? 'Backend Engine Busy' : 'Filosophy Engine'}
          </span>
          <span class="text-[11px] font-bold text-gray-900 dark:text-gray-100 tracking-tight leading-none">
            {$indexingStatus.isIndexing ? ($indexingStatus.statusMessage || 'Syncing...') : 'System Ready'}
          </span>
        </div>
      </div>
      
      <!-- Micro-progress bar (only visible when indexing) -->
      {#if $indexingStatus.isIndexing}
        <div 
            class="w-48 h-1 bg-gray-200/50 dark:bg-white/5 rounded-full overflow-hidden backdrop-blur-sm border border-white/5"
            transition:slide={{ axis: 'y' }}
        >
            <div 
            class="h-full bg-blue-500 shadow-[0_0_8px_rgba(59,130,246,0.6)] transition-all duration-500 ease-out"
            style="width: {$indexingStatus.progress}%"
            ></div>
        </div>
      {/if}
    </div>
  {/if}
</div>

<APIConfirmDialog request={apiConfirmRequest} on:reply={handleConfirmReply} />

<style>
  :global(.scrollbar-custom::-webkit-scrollbar) { width: 6px; }
  :global(.scrollbar-custom::-webkit-scrollbar-track) { background: transparent; }
  :global(.scrollbar-custom::-webkit-scrollbar-thumb) { background: #E2E8F0; border-radius: 4px; }
  :global(.dark .scrollbar-custom::-webkit-scrollbar-thumb) { background: #222; }
  :global(.scrollbar-custom::-webkit-scrollbar-thumb:hover) { background: #CBD5E1; }
  :global(.dark .scrollbar-custom::-webkit-scrollbar-thumb:hover) { background: #333; }

  @keyframes -global-slideFadeIn {
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }
</style>
