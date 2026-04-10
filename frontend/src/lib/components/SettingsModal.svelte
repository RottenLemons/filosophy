<script lang="ts">
  import { onMount, onDestroy, createEventDispatcher } from 'svelte';
  import { fade, slide, fly } from 'svelte/transition';
  import { X, Folder, Cpu, Settings, Activity, AlertCircle, Network, Plus, Trash2, CheckCircle, Circle, RefreshCw, Clock } from 'lucide-svelte';
  import { themeStore } from '../../stores/theme';
  import { indexingStatus, initIndexerStore } from '../../stores/indexer';
  import { GetHomeFolders, GetEngineStatus, RetryEngineInit, CheckSystemGPU, GetGPUAcceleration, SetGPUAcceleration, TestLLMEndpoint } from '$lib/wailsjs/go/main/App';
  import Indexer from './Indexer.svelte';

  const dispatch = createEventDispatcher();
  export let show = false;

  let activeTab = 'folders'; // 'folders' | 'hardware' | 'connections'
  let hasGPU = false;
  let gpuAcceleration = false;
  let loading = true;
  let engineStatus: 'ready' | 'initializing' | 'offline' = 'ready';
  let retrying = false;

  $: engineError = engineStatus === 'offline';
  $: engineInitializing = engineStatus === 'initializing';

  let modalElement: HTMLElement;

  // ── Connections state ───────────────────────────────────────────────────────

  interface MCPServer {
    id: string;
    name: string;
    type: 'stdio' | 'sse' | 'http';
    command: string;
    url: string;
    args: string;
    enabled: boolean;
  }

  interface LLMConfig {
    provider: 'none' | 'ollama' | 'lmstudio' | 'openai-compatible';
    baseUrl: string;
    model: string;
    apiKey: string;
  }

  const LLM_STORAGE_KEY = 'filosophy_llm_config';
  const MCP_STORAGE_KEY = 'filosophy_mcp_servers';

  let llmConfig: LLMConfig = {
    provider: 'none',
    baseUrl: '',
    model: '',
    apiKey: '',
  };

  let mcpServers: MCPServer[] = [];
  let newServer: Partial<MCPServer> = { type: 'stdio', enabled: true };
  let showAddServer = false;
  let llmTestStatus: 'idle' | 'testing' | 'ok' | 'error' = 'idle';
  let llmTestMessage = '';

  function loadConnections() {
    try {
      const stored = localStorage.getItem(LLM_STORAGE_KEY);
      if (stored) llmConfig = { ...llmConfig, ...JSON.parse(stored) };
    } catch {}
    try {
      const stored = localStorage.getItem(MCP_STORAGE_KEY);
      if (stored) mcpServers = JSON.parse(stored);
    } catch {}
  }

  function saveLLM() {
    localStorage.setItem(LLM_STORAGE_KEY, JSON.stringify(llmConfig));
  }

  function saveMCP() {
    localStorage.setItem(MCP_STORAGE_KEY, JSON.stringify(mcpServers));
  }

  function providerDefaults(provider: LLMConfig['provider']) {
    llmConfig.provider = provider;
    if (provider === 'ollama') {
      llmConfig.baseUrl = llmConfig.baseUrl || 'http://localhost:11434';
      llmConfig.model = llmConfig.model || 'llama3';
    } else if (provider === 'lmstudio') {
      llmConfig.baseUrl = llmConfig.baseUrl || 'http://localhost:1234/v1';
      llmConfig.model = llmConfig.model || '';
    }
    llmTestStatus = 'idle';
    saveLLM();
  }

  async function testLLMConnection() {
    if (!llmConfig.baseUrl) return;
    llmTestStatus = 'testing';
    llmTestMessage = '';
    try {
      const base = llmConfig.baseUrl.replace(/\/$/, '');
      const url = llmConfig.provider === 'ollama'
        ? `${base}/api/tags`
        : `${base}/models`;
      // Route through Go to avoid WebView2 loopback network restrictions
      const errMsg: string = await TestLLMEndpoint(url);
      if (!errMsg) {
        llmTestStatus = 'ok';
        llmTestMessage = 'Connected successfully';
      } else {
        llmTestStatus = 'error';
        llmTestMessage = errMsg;
      }
    } catch (e: any) {
      llmTestStatus = 'error';
      llmTestMessage = e?.message ?? 'Connection failed';
    }
  }

  function addMCPServer() {
    if (!newServer.name) return;
    mcpServers = [
      ...mcpServers,
      {
        id: crypto.randomUUID(),
        name: newServer.name ?? '',
        type: newServer.type ?? 'stdio',
        command: newServer.command ?? '',
        url: newServer.url ?? '',
        args: newServer.args ?? '',
        enabled: newServer.enabled ?? true,
      },
    ];
    saveMCP();
    newServer = { type: 'stdio', enabled: true };
    showAddServer = false;
  }

  function removeMCPServer(id: string) {
    mcpServers = mcpServers.filter(s => s.id !== id);
    saveMCP();
  }

  function toggleMCPServer(id: string) {
    mcpServers = mcpServers.map(s => s.id === id ? { ...s, enabled: !s.enabled } : s);
    saveMCP();
  }

  let engineStatusUnsubscribe: (() => void) | null = null;

  onMount(async () => {
    initIndexerStore();
    loadConnections();

    // Listen for async engine_status events (emitted when WAL recovery finishes)
    if (window['runtime']?.EventsOn) {
      engineStatusUnsubscribe = window['runtime'].EventsOn('engine_status', (status: string) => {
        engineStatus = status as typeof engineStatus;
        if (status === 'ready') window.location.reload();
      });
    }

    try {
      hasGPU = await CheckSystemGPU();
      gpuAcceleration = await GetGPUAcceleration();
      if (!hasGPU) gpuAcceleration = false;
      const status = await GetEngineStatus();
      engineStatus = status as typeof engineStatus;
    } catch (e) {
      console.error('Settings initialization error:', e);
      engineStatus = 'offline';
    } finally {
      loading = false;
    }
  });

  onDestroy(() => {
    engineStatusUnsubscribe?.();
  });

  async function retryEngine() {
    if (retrying) return;
    retrying = true;
    try {
      await RetryEngineInit();
      const status = await GetEngineStatus();
      engineStatus = status as typeof engineStatus;
      if (status === 'ready') window.location.reload();
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

          <button
            on:click={() => activeTab = 'connections'}
            class="w-full flex items-center gap-3 px-3 py-2.5 text-xs font-semibold rounded-md transition-all
                   {activeTab === 'connections'
                     ? 'bg-white dark:bg-[#1a1a1a] text-blue-600 dark:text-white shadow-sm border border-gray-200 dark:border-[#333]'
                     : 'text-gray-500 hover:text-gray-900 dark:hover:text-gray-200 hover:bg-gray-100/50 dark:hover:bg-[#151515]'}"
          >
            <Network class="w-4 h-4" />
            <span>Connections</span>
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
          
          {#if engineInitializing}
            <div class="space-y-2" transition:slide>
              <div class="flex items-center gap-1.5 text-[9px] font-bold uppercase tracking-wider text-blue-600 dark:text-blue-400">
                <Clock class="w-3 h-3 animate-pulse" />
                <span>Starting Up</span>
              </div>
              <div class="text-[8px] text-gray-400 dark:text-gray-500 font-medium leading-tight">
                Recovering database, please wait…
              </div>
            </div>
          {:else if engineError}
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
            {activeTab === 'folders' ? 'Indexing Preferences' : activeTab === 'hardware' ? 'System & Performance' : 'LLM & MCP Connections'}
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

              {#if engineInitializing}
                <div class="flex-1 flex flex-col items-center justify-center p-10 bg-blue-50/50 dark:bg-blue-950/10 border border-blue-100 dark:border-blue-900/30 rounded-xl text-center space-y-4">
                  <Clock class="w-12 h-12 text-blue-500 animate-pulse" />
                  <div class="space-y-2">
                    <h5 class="text-lg font-serif font-bold text-blue-900 dark:text-blue-300">Engine Starting Up</h5>
                    <p class="text-xs text-blue-700 dark:text-blue-400/80 max-w-xs leading-relaxed">
                      The search engine is recovering the database from a previous session. This usually takes a few seconds but can take longer after an unexpected shutdown.
                    </p>
                    <p class="text-[10px] text-blue-500/60 dark:text-blue-400/40 pt-1">
                      The page will reload automatically when ready.
                    </p>
                  </div>
                </div>
              {:else if engineError}
                <div class="flex-1 flex flex-col items-center justify-center p-10 bg-amber-50/50 dark:bg-amber-950/10 border border-amber-100 dark:border-amber-900/30 rounded-xl text-center space-y-4">
                  <Activity class="w-12 h-12 text-amber-500 animate-pulse" />
                  <div class="space-y-2">
                    <h5 class="text-lg font-serif font-bold text-amber-900 dark:text-amber-400">Search Engine Offline</h5>
                    <p class="text-xs text-amber-700 dark:text-amber-500/80 max-w-xs leading-relaxed">
                      The AI backend failed to initialize. Indexing and vector search are disabled.
                    </p>
                    <p class="text-[10px] font-mono text-amber-600/60 dark:text-amber-500/40 pt-2">
                      Check <code>filosophy.log</code> for details.
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
          {:else if activeTab === 'connections'}
            <div in:fade={{ duration: 200 }} class="space-y-10">

              <!-- Local LLM -->
              <section>
                <div class="mb-6">
                  <h4 class="text-xl font-serif text-slate-900 dark:text-gray-100 font-bold mb-2">Local LLM</h4>
                  <p class="text-xs text-gray-400 dark:text-gray-500 leading-relaxed max-w-lg">
                    Connect Filosophy to a locally running language model for AI-powered search summaries and chat.
                  </p>
                </div>

                <!-- Provider selector -->
                <div class="grid grid-cols-4 gap-2 mb-6">
                  {#each [
                    { id: 'none', label: 'None' },
                    { id: 'ollama', label: 'Ollama' },
                    { id: 'lmstudio', label: 'LM Studio' },
                    { id: 'openai-compatible', label: 'OpenAI-compat.' },
                  ] as p}
                    <button
                      on:click={() => providerDefaults(p.id as LLMConfig['provider'])}
                      class="py-2 px-3 text-[11px] font-semibold rounded-lg border transition-all
                             {llmConfig.provider === p.id
                               ? 'bg-blue-600 text-white border-blue-600 shadow'
                               : 'bg-white dark:bg-[#161616] text-gray-600 dark:text-gray-400 border-gray-200 dark:border-[#2a2a2a] hover:border-blue-400'}"
                    >
                      {p.label}
                    </button>
                  {/each}
                </div>

                {#if llmConfig.provider !== 'none'}
                  <div class="space-y-3" transition:slide={{ duration: 200 }}>
                    <div class="flex gap-3">
                      <div class="flex-1">
                        <label class="block text-[10px] font-bold uppercase tracking-widest text-gray-400 dark:text-gray-500 mb-1">Base URL</label>
                        <input
                          type="text"
                          bind:value={llmConfig.baseUrl}
                          on:change={saveLLM}
                          placeholder="http://localhost:11434"
                          class="w-full px-3 py-2 text-xs rounded-lg border border-gray-200 dark:border-[#2a2a2a] bg-white dark:bg-[#0a0a0a] text-gray-800 dark:text-gray-200 focus:outline-none focus:ring-1 focus:ring-blue-500"
                        />
                      </div>
                      <div class="w-40">
                        <label class="block text-[10px] font-bold uppercase tracking-widest text-gray-400 dark:text-gray-500 mb-1">Model</label>
                        <input
                          type="text"
                          bind:value={llmConfig.model}
                          on:change={saveLLM}
                          placeholder="llama3"
                          class="w-full px-3 py-2 text-xs rounded-lg border border-gray-200 dark:border-[#2a2a2a] bg-white dark:bg-[#0a0a0a] text-gray-800 dark:text-gray-200 focus:outline-none focus:ring-1 focus:ring-blue-500"
                        />
                      </div>
                    </div>

                    {#if llmConfig.provider === 'openai-compatible'}
                      <div transition:slide={{ duration: 150 }}>
                        <label class="block text-[10px] font-bold uppercase tracking-widest text-gray-400 dark:text-gray-500 mb-1">API Key <span class="normal-case font-normal">(optional)</span></label>
                        <input
                          type="password"
                          bind:value={llmConfig.apiKey}
                          on:change={saveLLM}
                          placeholder="sk-..."
                          class="w-full px-3 py-2 text-xs rounded-lg border border-gray-200 dark:border-[#2a2a2a] bg-white dark:bg-[#0a0a0a] text-gray-800 dark:text-gray-200 focus:outline-none focus:ring-1 focus:ring-blue-500"
                        />
                      </div>
                    {/if}

                    <div class="flex items-center gap-3 pt-1">
                      <button
                        on:click={testLLMConnection}
                        disabled={llmTestStatus === 'testing'}
                        class="flex items-center gap-2 px-4 py-1.5 text-[11px] font-semibold rounded-full border border-gray-200 dark:border-[#2a2a2a] bg-white dark:bg-[#161616] text-gray-700 dark:text-gray-300 hover:border-blue-400 transition-all disabled:opacity-50"
                      >
                        {#if llmTestStatus === 'testing'}
                          <RefreshCw class="w-3 h-3 animate-spin" />
                          <span>Testing…</span>
                        {:else}
                          <RefreshCw class="w-3 h-3" />
                          <span>Test Connection</span>
                        {/if}
                      </button>

                      {#if llmTestStatus === 'ok'}
                        <span class="flex items-center gap-1.5 text-[11px] text-green-600 dark:text-green-400" transition:fade>
                          <CheckCircle class="w-3.5 h-3.5" />
                          {llmTestMessage}
                        </span>
                      {:else if llmTestStatus === 'error'}
                        <span class="flex items-center gap-1.5 text-[11px] text-red-500" transition:fade>
                          <AlertCircle class="w-3.5 h-3.5" />
                          {llmTestMessage}
                        </span>
                      {/if}
                    </div>
                  </div>
                {/if}
              </section>

              <!-- MCP Servers -->
              <section>
                <div class="mb-6 flex items-center justify-between">
                  <div>
                    <h4 class="text-xl font-serif text-slate-900 dark:text-gray-100 font-bold mb-2">MCP Servers</h4>
                    <p class="text-xs text-gray-400 dark:text-gray-500 leading-relaxed max-w-lg">
                      Register Model Context Protocol servers to extend Filosophy with tools, prompts, and data sources.
                    </p>
                  </div>
                  <button
                    on:click={() => showAddServer = !showAddServer}
                    class="flex items-center gap-1.5 px-3 py-1.5 text-[11px] font-semibold rounded-full bg-blue-600 text-white hover:bg-blue-700 transition-colors shrink-0"
                  >
                    <Plus class="w-3.5 h-3.5" />
                    <span>Add Server</span>
                  </button>
                </div>

                <!-- Add server form -->
                {#if showAddServer}
                  <div class="mb-4 p-4 bg-blue-50/40 dark:bg-blue-950/10 border border-blue-100 dark:border-blue-900/30 rounded-xl space-y-3" transition:slide={{ duration: 200 }}>
                    <div class="flex gap-3">
                      <div class="flex-1">
                        <label class="block text-[10px] font-bold uppercase tracking-widest text-gray-400 dark:text-gray-500 mb-1">Name</label>
                        <input
                          type="text"
                          bind:value={newServer.name}
                          placeholder="My MCP Server"
                          class="w-full px-3 py-2 text-xs rounded-lg border border-gray-200 dark:border-[#2a2a2a] bg-white dark:bg-[#0a0a0a] text-gray-800 dark:text-gray-200 focus:outline-none focus:ring-1 focus:ring-blue-500"
                        />
                      </div>
                      <div class="w-36">
                        <label class="block text-[10px] font-bold uppercase tracking-widest text-gray-400 dark:text-gray-500 mb-1">Type</label>
                        <select
                          bind:value={newServer.type}
                          class="w-full px-3 py-2 text-xs rounded-lg border border-gray-200 dark:border-[#2a2a2a] bg-white dark:bg-[#0a0a0a] text-gray-800 dark:text-gray-200 focus:outline-none focus:ring-1 focus:ring-blue-500"
                        >
                          <option value="stdio">stdio</option>
                          <option value="sse">SSE</option>
                          <option value="http">HTTP</option>
                        </select>
                      </div>
                    </div>

                    {#if newServer.type === 'stdio'}
                      <div class="flex gap-3" transition:slide={{ duration: 150 }}>
                        <div class="flex-1">
                          <label class="block text-[10px] font-bold uppercase tracking-widest text-gray-400 dark:text-gray-500 mb-1">Command</label>
                          <input
                            type="text"
                            bind:value={newServer.command}
                            placeholder="npx -y @modelcontextprotocol/server-filesystem"
                            class="w-full px-3 py-2 text-xs rounded-lg border border-gray-200 dark:border-[#2a2a2a] bg-white dark:bg-[#0a0a0a] text-gray-800 dark:text-gray-200 font-mono focus:outline-none focus:ring-1 focus:ring-blue-500"
                          />
                        </div>
                        <div class="w-40">
                          <label class="block text-[10px] font-bold uppercase tracking-widest text-gray-400 dark:text-gray-500 mb-1">Args <span class="normal-case font-normal">(optional)</span></label>
                          <input
                            type="text"
                            bind:value={newServer.args}
                            placeholder="/path/to/dir"
                            class="w-full px-3 py-2 text-xs rounded-lg border border-gray-200 dark:border-[#2a2a2a] bg-white dark:bg-[#0a0a0a] text-gray-800 dark:text-gray-200 font-mono focus:outline-none focus:ring-1 focus:ring-blue-500"
                          />
                        </div>
                      </div>
                    {:else}
                      <div transition:slide={{ duration: 150 }}>
                        <label class="block text-[10px] font-bold uppercase tracking-widest text-gray-400 dark:text-gray-500 mb-1">URL</label>
                        <input
                          type="text"
                          bind:value={newServer.url}
                          placeholder="http://localhost:3000/mcp"
                          class="w-full px-3 py-2 text-xs rounded-lg border border-gray-200 dark:border-[#2a2a2a] bg-white dark:bg-[#0a0a0a] text-gray-800 dark:text-gray-200 font-mono focus:outline-none focus:ring-1 focus:ring-blue-500"
                        />
                      </div>
                    {/if}

                    <div class="flex justify-end gap-2 pt-1">
                      <button
                        on:click={() => { showAddServer = false; newServer = { type: 'stdio', enabled: true }; }}
                        class="px-4 py-1.5 text-[11px] font-semibold rounded-full border border-gray-200 dark:border-[#2a2a2a] text-gray-500 hover:text-gray-800 dark:hover:text-gray-200 transition-colors"
                      >
                        Cancel
                      </button>
                      <button
                        on:click={addMCPServer}
                        disabled={!newServer.name}
                        class="px-4 py-1.5 text-[11px] font-semibold rounded-full bg-blue-600 text-white hover:bg-blue-700 transition-colors disabled:opacity-40"
                      >
                        Add
                      </button>
                    </div>
                  </div>
                {/if}

                <!-- Server list -->
                {#if mcpServers.length === 0 && !showAddServer}
                  <div class="flex flex-col items-center justify-center py-10 text-center text-gray-400 dark:text-gray-600 border border-dashed border-gray-200 dark:border-[#2a2a2a] rounded-xl">
                    <Network class="w-8 h-8 mb-3 opacity-40" />
                    <p class="text-xs font-medium">No MCP servers configured</p>
                    <p class="text-[10px] mt-1">Add a server above to get started</p>
                  </div>
                {:else}
                  <div class="space-y-2">
                    {#each mcpServers as server (server.id)}
                      <div class="flex items-center gap-3 px-4 py-3 bg-white dark:bg-[#161616] border border-gray-200 dark:border-[#222] rounded-xl group transition-all" transition:slide={{ duration: 150 }}>
                        <!-- Enable toggle -->
                        <button
                          on:click={() => toggleMCPServer(server.id)}
                          aria-label="{server.enabled ? 'Disable' : 'Enable'} {server.name}"
                          class="shrink-0 transition-colors {server.enabled ? 'text-blue-500' : 'text-gray-300 dark:text-gray-600'}"
                        >
                          {#if server.enabled}
                            <CheckCircle class="w-4 h-4" />
                          {:else}
                            <Circle class="w-4 h-4" />
                          {/if}
                        </button>

                        <!-- Info -->
                        <div class="flex-1 min-w-0">
                          <div class="flex items-center gap-2">
                            <span class="text-xs font-semibold text-gray-800 dark:text-gray-200 truncate">{server.name}</span>
                            <span class="px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-wider rounded bg-gray-100 dark:bg-[#222] text-gray-500 dark:text-gray-400">{server.type}</span>
                          </div>
                          <p class="text-[10px] font-mono text-gray-400 dark:text-gray-500 truncate mt-0.5">
                            {server.type === 'stdio' ? [server.command, server.args].filter(Boolean).join(' ') : server.url}
                          </p>
                        </div>

                        <!-- Delete -->
                        <button
                          on:click={() => removeMCPServer(server.id)}
                          aria-label="Remove {server.name}"
                          class="shrink-0 text-gray-300 dark:text-gray-600 hover:text-red-500 dark:hover:text-red-400 opacity-0 group-hover:opacity-100 transition-all"
                        >
                          <Trash2 class="w-4 h-4" />
                        </button>
                      </div>
                    {/each}
                  </div>
                {/if}
              </section>

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
                           {hasGPU && gpuAcceleration ? 'bg-blue-600' : 'bg-gray-200 dark:bg-[#333]'}"
                  >
                    <span 
                      class="pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out
                             {hasGPU && gpuAcceleration ? 'translate-x-5' : 'translate-x-0'}"
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
