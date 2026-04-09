<script lang="ts">
  import { onMount, createEventDispatcher } from 'svelte';
  import { fade, slide, fly } from 'svelte/transition';
  import { X, Folder, Cpu, Settings, Activity, AlertCircle } from 'lucide-svelte';
  import { themeStore } from '../../stores/theme';
  import { indexingStatus, initIndexerStore } from '../../stores/indexer';
  import { GetHomeFolders, GetEngineStatus, RetryEngineInit, CheckSystemGPU, GetGPUAcceleration, SetGPUAcceleration } from '$lib/wailsjs/go/main/App';
  import Indexer from './Indexer.svelte';

  const dispatch = createEventDispatcher();
  export let show = false;

  let activeTab = 'folders'; // 'folders' | 'hardware'
  let hasGPU = false;
  let gpuAcceleration = false;
  let loading = true;
  let engineError = false;
  let retrying = false;

  let modalElement: HTMLElement;

  onMount(async () => {
    initIndexerStore();
    try {
      hasGPU = await CheckSystemGPU();
      gpuAcceleration = await GetGPUAcceleration();
      // Check if engine is initialized
      const engineIsReady = await GetEngineStatus();
      if (!engineIsReady) {
        engineError = true;
      }
    } catch (e) {
      console.error('Settings initialization error:', e);
      if (String(e).includes('backend engine not initialized')) {
        engineError = true;
      }
    } finally {
      loading = false;
    }
  });

  async function retryEngine() {
    if (retrying) return;
    retrying = true;
    try {
      await RetryEngineInit();
      const ready = await GetEngineStatus();
      if (ready) {
        engineError = false;
        // Full refresh to re-init all stores and components correctly
        window.location.reload();
      }
    } catch (e) {
      console.error('Retry failed:', e);
    } finally {
      retrying = false;
    }
  }

  async function toggleGPU() {
    if (!hasGPU) return;
    gpuAcceleration = !gpuAcceleration;
    try {
      await SetGPUAcceleration(gpuAcceleration);
    } catch (e) {
      console.error('Failed to save GPU setting:', e);
      if (String(e).includes('backend engine not initialized')) {
        engineError = true;
      }
      gpuAcceleration = !gpuAcceleration; // revert
    }
  }

  function close() {
    dispatch('close');
  }

  /** @param {KeyboardEvent} e */
  function onKeyDown(e: KeyboardEvent) {
    if (e.key === 'Escape') close();
    if (e.key === 'Tab') handleTab(e);
  }

  function handleTab(e: KeyboardEvent) {
    if (!modalElement) return;

    const focusableElements = modalElement.querySelectorAll(
      'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
    );
    const firstElement = focusableElements[0] as HTMLElement;
    const lastElement = focusableElements[focusableElements.length - 1] as HTMLElement;

    if (e.shiftKey) {
      if (document.activeElement === firstElement) {
        e.preventDefault();
        lastElement.focus();
      }
    } else {
      if (document.activeElement === lastElement) {
        e.preventDefault();
        firstElement.focus();
      }
    }
  }
</script>

<svelte:window on:keydown={onKeyDown} />

{#if show}
  <!-- svelte-ignore a11y-click-events-have-key-events -->
  <!-- svelte-ignore a11y-no-static-element-interactions -->
  <div 
    class="fixed inset-0 z-[1000] flex items-center justify-center p-4 bg-black/40 backdrop-blur-sm"
    transition:fade={{ duration: 200 }}
    on:click|self={close}
  >
    <div 
      bind:this={modalElement}
      class="w-full max-w-4xl h-[640px] bg-[#FAF9F6] dark:bg-[#111] border border-gray-200 dark:border-[#2a2a2a] shadow-2xl flex overflow-hidden rounded-lg"
      transition:fly={{ y: 20, duration: 400, opacity: 0 }}
      role="dialog"
      aria-modal="true"
    >
      <!-- Sidebar Navigation -->
      <aside class="w-64 border-r border-gray-200 dark:border-[#2a2a2a] bg-gray-50/50 dark:bg-[#0a0a0a] flex flex-col pt-12">
        <div class="px-6 mb-8">
          <h2 class="text-[10px] font-bold uppercase tracking-[0.2em] text-gray-400 dark:text-gray-500">Settings</h2>
          <div class="mt-1 h-px w-8 bg-blue-500"></div>
        </div>

        <nav class="flex-1 px-3 space-y-1">
          <button 
            on:click={() => activeTab = 'folders'}
            class="w-full flex items-center gap-3 px-3 py-2.5 text-xs font-semibold rounded-md transition-all
                   {activeTab === 'folders' 
                     ? 'bg-white dark:bg-[#1a1a1a] text-blue-600 dark:text-white shadow-sm border border-gray-200 dark:border-[#333]' 
                     : 'text-gray-500 hover:text-gray-900 dark:hover:text-gray-200 hover:bg-gray-100/50 dark:hover:bg-[#151515]'}"
          >
            <Folder class="w-4 h-4" />
            <span>Indexed Folders</span>
          </button>

          <button 
            on:click={() => activeTab = 'hardware'}
            class="w-full flex items-center gap-3 px-3 py-2.5 text-xs font-semibold rounded-md transition-all
                   {activeTab === 'hardware' 
                     ? 'bg-white dark:bg-[#1a1a1a] text-blue-600 dark:text-white shadow-sm border border-gray-200 dark:border-[#333]' 
                     : 'text-gray-500 hover:text-gray-900 dark:hover:text-gray-200 hover:bg-gray-100/50 dark:hover:bg-[#151515]'}"
          >
            <Cpu class="w-4 h-4" />
            <span>Hardware</span>
          </button>
        </nav>

        <!-- Sidebar Footer Status -->
        <div class="p-6 space-y-4 border-t border-gray-100 dark:border-[#1a1a1a]">
          {#if $indexingStatus.isIndexing}
            <div class="space-y-2" transition:slide>
              <div class="flex items-center justify-between text-[9px] font-bold uppercase tracking-wider text-blue-600 dark:text-blue-400">
                <div class="flex items-center gap-1.5">
                  <Activity class="w-3 h-3 animate-pulse" />
                  <span>Indexing</span>
                </div>
                <span>{Math.round($indexingStatus.progress)}%</span>
              </div>
              <div class="h-1 w-full bg-gray-200 dark:bg-[#222] rounded-full overflow-hidden">
                <div class="h-full bg-blue-500 transition-all duration-300" style="width: {$indexingStatus.progress}%"></div>
              </div>
              <div class="text-[8px] text-gray-400 dark:text-gray-500 font-medium truncate">
                {$indexingStatus.statusMessage}
              </div>
            </div>
          {/if}
          
          {#if engineError}
            <div class="space-y-2" transition:slide>
              <div class="flex items-center gap-1.5 text-[9px] font-bold uppercase tracking-wider text-amber-600 dark:text-amber-400">
                <AlertCircle class="w-3 h-3" />
                <span>Backend Offline</span>
              </div>
              <div class="text-[8px] text-gray-400 dark:text-gray-500 font-medium leading-tight">
                AI search engine failed to initialize.
              </div>
            </div>
          {/if}
          
          <div class="flex items-center gap-2 text-[9px] font-medium text-gray-400 uppercase tracking-widest">
            <Settings class="w-3 h-3" />
            <span>v1.2.0 Stable</span>
          </div>
        </div>
      </aside>

      <!-- Main Content Area -->
      <main class="flex-1 flex flex-col relative bg-white dark:bg-[#111]">
        <!-- Header -->
        <header class="h-16 flex items-center justify-between px-8 border-b border-gray-100 dark:border-[#1a1a1a] shrink-0">
          <h3 class="text-sm font-bold text-gray-900 dark:text-white uppercase tracking-wider">
            {activeTab === 'folders' ? 'Indexing Preferences' : 'System & Performance'}
          </h3>
          <button 
            on:click={close}
            class="p-2 text-gray-400 hover:text-gray-900 dark:hover:text-white transition-colors"
            aria-label="Close Settings"
          >
            <X class="w-5 h-5" />
          </button>
        </header>

        <!-- Body -->
        <div class="flex-1 overflow-y-auto p-10 scrollbar-custom">
          {#if activeTab === 'folders'}
            <div in:fade={{ duration: 200 }} class="flex flex-col h-full">
              <div class="mb-6 shrink-0">
                <h4 class="text-xl font-serif text-slate-900 dark:text-gray-100 font-bold mb-2">Content Libraries</h4>
                <p class="text-xs text-gray-400 dark:text-gray-500 leading-relaxed max-w-lg">
                  Select the directories you want Filosophy to monitor. Folders are recursively indexed for semantic search and metadata extraction.
                </p>
              </div>

              {#if engineError}
                <div class="flex-1 flex flex-col items-center justify-center p-10 bg-amber-50/50 dark:bg-amber-950/10 border border-amber-100 dark:border-amber-900/30 rounded-xl text-center space-y-4">
                  <Activity class="w-12 h-12 text-amber-500 animate-pulse" />
                  <div class="space-y-2">
                    <h5 class="text-lg font-serif font-bold text-amber-900 dark:text-amber-400">Search Engine Offline</h5>
                    <p class="text-xs text-amber-700 dark:text-amber-500/80 max-w-xs leading-relaxed">
                      The AI backend failed to initialize. Indexing operations and vector search are currently disabled.
                    </p>
                    <p class="text-[10px] font-mono text-amber-600/60 dark:text-amber-500/40 pt-2">
                      Please check <code>filosophy.log</code> for errors and restart the application.
                    </p>
                  </div>
                  <button 
                    on:click={retryEngine}
                    disabled={retrying}
                    class="mt-2 px-6 py-2 bg-amber-600 hover:bg-amber-700 text-white text-xs font-bold rounded-full transition-all shadow-lg shadow-amber-900/20 disabled:opacity-50 flex items-center gap-2"
                  >
                    {#if retrying}
                      <Activity class="w-3 h-3 animate-spin" />
                      <span>Reconnecting...</span>
                    {:else}
                      <span>Retry Connection</span>
                    {/if}
                  </button>
                </div>
              {:else}
                <div class="flex-1 min-h-0 bg-gray-50/50 dark:bg-[#0a0a0a] rounded-xl border border-gray-100 dark:border-[#1a1a1a] overflow-hidden p-6">
                  <Indexer flat={true} />
                </div>
              {/if}
            </div>
          {:else if activeTab === 'hardware'}
            <div in:fade={{ duration: 200 }} class="space-y-12">
              <section>
                <div class="mb-6">
                  <h4 class="text-xl font-serif text-slate-900 dark:text-gray-100 font-bold mb-2">GPU Acceleration</h4>
                  <p class="text-xs text-gray-400 dark:text-gray-500 leading-relaxed max-w-lg">
                    Leverage your dedicated graphics card to speed up vector embedding and AI-based reranking.
                  </p>
                </div>

                <div class="bg-white dark:bg-[#161616] border border-gray-200 dark:border-[#222] rounded-xl p-6 flex items-center justify-between shadow-sm">
                  <div class="space-y-1">
                    <div class="flex items-center gap-2">
                       <span class="text-sm font-semibold text-gray-800 dark:text-gray-200">Hardware Acceleration</span>
                       {#if !hasGPU && !loading}
                         <span class="px-2 py-0.5 bg-red-100 dark:bg-red-950/30 text-red-600 dark:text-red-400 text-[9px] font-bold uppercase tracking-wider rounded">Unavailable</span>
                       {:else if hasGPU}
                         <span class="px-2 py-0.5 bg-green-100 dark:bg-green-950/30 text-green-600 dark:text-green-400 text-[9px] font-bold uppercase tracking-wider rounded">Compatible</span>
                       {/if}
                    </div>
                    <p class="text-[11px] text-gray-400 dark:text-gray-500 italic">
                      {#if loading}
                        Analyzing hardware components...
                      {:else if !hasGPU}
                        No compatible NVIDIA, AMD, or Apple Silicon GPU detected.
                      {:else}
                        Dedicated GPU detected. <span class="text-blue-500">(Requires app restart)</span>
                      {/if}
                    </p>
                  </div>

                  <button 
                    on:click={toggleGPU}
                    disabled={!hasGPU || loading}
                    aria-label="Toggle GPU Acceleration"
                    class="relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none disabled:opacity-30 disabled:cursor-not-allowed
                           {gpuAcceleration ? 'bg-blue-600' : 'bg-gray-200 dark:bg-[#333]'}"
                  >
                    <span 
                      class="pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out
                             {gpuAcceleration ? 'translate-x-5' : 'translate-x-0'}"
                    ></span>
                  </button>
                </div>
              </section>

              <section class="p-6 bg-blue-50/50 dark:bg-blue-950/10 border border-blue-100/50 dark:border-blue-900/20 rounded-xl">
                 <h5 class="text-[10px] font-bold text-blue-600 dark:text-blue-400 uppercase tracking-widest mb-2">Performance Tip</h5>
                 <p class="text-[11px] text-gray-500 dark:text-gray-400 leading-relaxed">
                   Enabling GPU acceleration can reduce search latency by up to 80% on large collections, but may increase power consumption on laptops.
                 </p>
              </section>
            </div>
          {/if}
        </div>
      </main>
    </div>
  </div>
{/if}

<style>
  :global(.scrollbar-custom::-webkit-scrollbar) { width: 6px; }
  :global(.scrollbar-custom::-webkit-scrollbar-track) { background: transparent; }
  :global(.scrollbar-custom::-webkit-scrollbar-thumb) { background: #E2E8F0; border-radius: 4px; }
  :global(.dark .scrollbar-custom::-webkit-scrollbar-thumb) { background: #222; }
</style>
