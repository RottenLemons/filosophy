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
      if (!hasGPU) gpuAcceleration = false;
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
    class="fixed inset-0 z-[1000] flex items-center justify-center p-4 bg-black/60 backdrop-blur-md"
    transition:fade={{ duration: 200 }}
    on:click|self={close}
  >
    <div 
      bind:this={modalElement}
      class="w-full max-w-4xl h-[640px] bg-[#0e0e0e] border border-[#474848]/20 shadow-[0_32px_64px_-12px_rgba(0,0,0,0.8)] flex overflow-hidden rounded-sm"
      transition:fly={{ y: 20, duration: 400, opacity: 0 }}
      role="dialog"
      aria-modal="true"
    >
      <!-- Sidebar Navigation -->
      <aside class="w-64 bg-[#131313] flex flex-col pt-12 border-r border-[#474848]/10">
        <div class="px-6 mb-8">
          <h2 class="text-[#acabab] text-xs uppercase tracking-widest font-sans font-bold">Settings</h2>
          <div class="mt-2 h-0.5 w-6 bg-[#bfc8ca]"></div>
        </div>

        <nav class="flex-1 px-3 space-y-1">
          <button 
            on:click={() => activeTab = 'folders'}
            class="w-full flex items-center gap-3 px-4 py-3 text-xs font-sans tracking-wide transition-all rounded-sm
                   {activeTab === 'folders' 
                     ? 'bg-[#252626] text-[#e7e5e5] shadow-inner' 
                     : 'text-[#acabab] hover:text-[#e7e5e5] hover:bg-[#1a1a1a]'}"
          >
            <Folder class="w-4 h-4" />
            <span class="uppercase tracking-widest text-[10px]">Indexed Folders</span>
          </button>

          <button 
            on:click={() => activeTab = 'hardware'}
            class="w-full flex items-center gap-3 px-4 py-3 text-xs font-sans tracking-wide transition-all rounded-sm
                   {activeTab === 'hardware' 
                     ? 'bg-[#252626] text-[#e7e5e5] shadow-inner' 
                     : 'text-[#acabab] hover:text-[#e7e5e5] hover:bg-[#1a1a1a]'}"
          >
            <Cpu class="w-4 h-4" />
            <span class="uppercase tracking-widest text-[10px]">Hardware</span>
          </button>
        </nav>

        <!-- Sidebar Footer Status -->
        <div class="p-6 space-y-4 bg-[#0e0e0e]">
          {#if $indexingStatus.isIndexing}
            <div class="space-y-3" transition:slide>
              <div class="flex items-center justify-between text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab]">
                <div class="flex items-center gap-2">
                  <Activity class="w-3 h-3 animate-pulse text-[#bfc8ca]" />
                  <span>Indexing</span>
                </div>
                <span class="text-[#e7e5e5]">{Math.round($indexingStatus.progress)}%</span>
              </div>
              <!-- TASK 2: Loading Bar -->
              <div class="h-1.5 w-full bg-[#252626] rounded-sm overflow-hidden">
                <div 
                  class="h-full bg-gradient-to-br from-[#bfc8ca] to-[#3f484a] transition-all duration-300 ease-out" 
                  style="width: {$indexingStatus.progress}%"
                ></div>
              </div>
              <div class="text-[9px] text-[#acabab] font-sans tracking-tight truncate opacity-60">
                {$indexingStatus.statusMessage}
              </div>
            </div>
          {/if}
          
          {#if engineError}
            <div class="p-3 bg-[#252626] rounded-sm space-y-2 border-l-2 border-[#bfc8ca]" transition:slide>
              <div class="flex items-center gap-1.5 text-[9px] font-bold uppercase tracking-widest text-[#bfc8ca]">
                <AlertCircle class="w-3 h-3" />
                <span>Backend Offline</span>
              </div>
              <div class="text-[9px] text-[#acabab] leading-tight">
                AI engine failed to initialize. Check logs.
              </div>
            </div>
          {/if}
          
          <div class="flex items-center gap-2 text-[9px] font-sans font-bold text-[#acabab] uppercase tracking-widest opacity-40">
            <Settings class="w-3 h-3" />
            <span>v1.2.0 stable</span>
          </div>
        </div>
      </aside>

      <!-- Main Content Area -->
      <main class="flex-1 flex flex-col relative bg-[#0e0e0e]">
        <!-- Header -->
        <header class="h-20 flex items-center justify-between px-10 shrink-0">
          <h3 class="text-[#acabab] text-xs uppercase tracking-widest font-sans font-bold">
            {activeTab === 'folders' ? 'Indexing Preferences' : 'System & Performance'}
          </h3>
          <button 
            on:click={close}
            class="p-2 text-[#acabab] hover:text-[#e7e5e5] transition-colors"
            aria-label="Close Settings"
          >
            <X class="w-5 h-5" />
          </button>
        </header>

        <!-- Body -->
        <div class="flex-1 overflow-y-auto px-10 pb-10 scrollbar-custom">
          {#if activeTab === 'folders'}
            <div in:fade={{ duration: 200 }} class="flex flex-col h-full space-y-8">
              <div class="shrink-0">
                <h4 class="text-3xl font-serif text-[#e7e5e5] tracking-[-0.02em] font-medium mb-3 text-pretty">Content Libraries</h4>
                <p class="text-xs text-[#acabab] font-sans tracking-tight leading-relaxed max-w-lg opacity-80">
                  Select the directories you want Filosophy to monitor. Folders are recursively indexed for semantic search and metadata extraction.
                </p>
              </div>

              {#if engineError}
                <div class="flex-1 flex flex-col items-center justify-center p-12 bg-[#131313] rounded-sm text-center space-y-6 border border-[#474848]/20">
                  <Activity class="w-12 h-12 text-[#bfc8ca] animate-pulse opacity-40" />
                  <div class="space-y-2">
                    <h5 class="text-xl font-serif text-[#e7e5e5] tracking-tight">Search Engine Offline</h5>
                    <p class="text-xs text-[#acabab] font-sans leading-relaxed max-w-xs opacity-60">
                      The AI backend failed to initialize. Indexing operations and vector search are currently disabled.
                    </p>
                  </div>
                  <button 
                    on:click={retryEngine}
                    disabled={retrying}
                    class="px-8 py-3 bg-[#252626] hover:bg-[#2b2c2c] text-[#e7e5e5] text-[10px] uppercase tracking-widest font-bold rounded-sm transition-all border border-[#474848]/30 disabled:opacity-50 flex items-center gap-3"
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
                <div class="flex-1 min-h-0 bg-[#131313] rounded-sm overflow-hidden p-8 border border-[#474848]/10 shadow-inner">
                  <Indexer flat={true} />
                </div>
              {/if}
            </div>
          {:else if activeTab === 'hardware'}
            <div in:fade={{ duration: 200 }} class="space-y-10">
              <section>
                <div class="mb-8">
                  <h4 class="text-3xl font-serif text-[#e7e5e5] tracking-[-0.02em] font-medium mb-3">GPU Acceleration</h4>
                  <p class="text-xs text-[#acabab] font-sans tracking-tight leading-relaxed max-w-lg opacity-80">
                    Leverage your dedicated graphics card to speed up vector embedding and AI-based reranking.
                  </p>
                </div>

                <div class="bg-[#131313] rounded-sm p-8 flex items-center justify-between border border-[#474848]/10 shadow-sm">
                  <div class="space-y-2">
                    <div class="flex items-center gap-3">
                       <span class="text-sm font-sans font-medium text-[#e7e5e5] uppercase tracking-widest">Hardware Acceleration</span>
                       {#if !hasGPU && !loading}
                         <span class="px-2 py-0.5 bg-[#252626] text-[#acabab] text-[9px] font-bold uppercase tracking-wider rounded-sm border border-[#474848]/30">Unavailable</span>
                       {:else if hasGPU}
                         <span class="px-2 py-0.5 bg-[#bfc8ca]/10 text-[#bfc8ca] text-[9px] font-bold uppercase tracking-wider rounded-sm border border-[#bfc8ca]/20">Compatible</span>
                       {/if}
                    </div>
                    <p class="text-[11px] font-sans text-[#acabab] italic opacity-60">
                      {#if loading}
                        Analyzing hardware components...
                      {:else if !hasGPU}
                        No compatible NVIDIA, AMD, or Apple Silicon GPU detected.
                      {:else}
                        Dedicated GPU detected. <span class="text-[#bfc8ca] font-semibold tracking-wide">Requires app restart.</span>
                      {/if}
                    </p>
                  </div>

                  <button 
                    on:click={toggleGPU}
                    disabled={!hasGPU || loading}
                    aria-label="Toggle GPU Acceleration"
                    class="relative inline-flex h-6 w-12 shrink-0 cursor-pointer rounded-sm border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none disabled:opacity-20 disabled:cursor-not-allowed
                           {hasGPU && gpuAcceleration ? 'bg-[#bfc8ca]' : 'bg-[#252626]'}"
                  >
                    <span 
                      class="pointer-events-none inline-block h-5 w-5 transform rounded-sm bg-[#0e0e0e] shadow-lg transition duration-200 ease-in-out
                             {hasGPU && gpuAcceleration ? 'translate-x-6' : 'translate-x-0'}"
                    ></span>
                  </button>
                </div>
              </section>

              <section class="p-8 bg-[#131313] rounded-sm border-l-2 border-[#bfc8ca] shadow-sm">
                 <h5 class="text-[#bfc8ca] text-[10px] font-bold uppercase tracking-widest mb-3">Performance Note</h5>
                 <p class="text-[11px] font-sans text-[#acabab] leading-relaxed opacity-80">
                   Enabling GPU acceleration can reduce search latency by up to 80% on large collections. This is highly recommended for users with dedicated NVIDIA or AMD hardware.
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
  :global(.scrollbar-custom::-webkit-scrollbar) { width: 4px; }
  :global(.scrollbar-custom::-webkit-scrollbar-track) { background: transparent; }
  :global(.scrollbar-custom::-webkit-scrollbar-thumb) { background: #252626; border-radius: 0px; }
  :global(.scrollbar-custom::-webkit-scrollbar-thumb:hover) { background: #474848; }
</style>
