<script lang="ts">
  import { onMount, onDestroy, createEventDispatcher } from 'svelte';
  import { fade, slide, fly } from 'svelte/transition';
  import { X, Folder, Cpu, Settings, Activity, AlertCircle, Network, Plus, Trash2, CheckCircle, Circle, RefreshCw, Clock, Key, Copy, Globe, Lock, BarChart3 } from 'lucide-svelte';
  import { themeStore } from '../../stores/theme';
  import { indexingStatus, initIndexerStore } from '../../stores/indexer';
  import { GetHomeFolders, GetEngineStatus, RetryEngineInit, CheckSystemGPU, GetGPUAcceleration, SetGPUAcceleration, TestLLMEndpoint, GetAPIConfig, SetAPIEnabled, SetAPIPort, RegenerateAPIKey, SetMCPEnabled, SetMCPKey, GetOptimizerStatus } from '$lib/wailsjs/go/main/App';
  import Indexer from './Indexer.svelte';

  const dispatch = createEventDispatcher();
  export let show = false;
  export let embedded = false;

  let activeTab = 'folders'; // 'folders' | 'hardware' | 'connections' | 'access'
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
      const errMsg: string = await TestLLMEndpoint(url, llmConfig.apiKey);
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

  // ── Access (API / MCP server) state ────────────────────────────────────────

  let apiEnabled = false;
  let apiPort = 7700;
  let apiKey = '';
  let mcpEnabled = false;
  let mcpKey = '';
  let apiKeyCopied = false;
  let apiPortInput = '7700';
  let accessLoading = true;
  let accessSaving = false;

  async function loadAccess() {
    try {
      const cfg = await GetAPIConfig();
      apiEnabled = cfg.enabled;
      apiPort = cfg.port;
      apiKey = cfg.apiKey;
      mcpEnabled = cfg.mcpEnabled;
      mcpKey = cfg.mcpKey ?? '';
      apiPortInput = String(cfg.port);
    } catch {}
    accessLoading = false;
  }

  async function toggleAPI() {
    apiEnabled = !apiEnabled;
    try { await SetAPIEnabled(apiEnabled); } catch { apiEnabled = !apiEnabled; }
  }

  async function savePort() {
    const p = parseInt(apiPortInput, 10);
    if (isNaN(p) || p < 1 || p > 65535) { apiPortInput = String(apiPort); return; }
    if (p === apiPort) return;
    apiPort = p;
    try { await SetAPIPort(p); } catch {}
  }

  async function regenKey() {
    const newKey = await RegenerateAPIKey();
    apiKey = newKey;
  }

  async function copyKey() {
    if (!apiKey) return;
    await navigator.clipboard.writeText(apiKey);
    apiKeyCopied = true;
    setTimeout(() => apiKeyCopied = false, 2000);
  }

  async function toggleMCP() {
    mcpEnabled = !mcpEnabled;
    try { await SetMCPEnabled(mcpEnabled); } catch { mcpEnabled = !mcpEnabled; }
  }

  async function saveMCPKey() {
    try { await SetMCPKey(mcpKey); } catch {}
  }

  $: claudeDesktopSnippet = JSON.stringify({
    mcpServers: {
      filosophy: {
        url: `http://127.0.0.1:${apiPort}/mcp/sse`,
        ...(mcpKey ? { headers: { Authorization: `Bearer ${mcpKey}` } } : {}),
      }
    }
  }, null, 2);

  let snippetCopied = false;
  async function copySnippet() {
    await navigator.clipboard.writeText(claudeDesktopSnippet);
    snippetCopied = true;
    setTimeout(() => snippetCopied = false, 2000);
  }

  // ── Ranking / Optimizer state ──────────────────────────────────────────────

  let optimizerStatus: any = null;
  let optimizerLoading = true;
  let optimizerPollId: ReturnType<typeof setInterval> | null = null;

  async function loadOptimizer() {
    try {
      optimizerStatus = await GetOptimizerStatus();
    } catch {}
    optimizerLoading = false;
  }

  function formatMetric(value: number | undefined, digits = 2) {
    return typeof value === 'number' ? value.toFixed(digits) : '--';
  }

  function formatPreferenceScore(value: number | undefined) {
    return typeof value === 'number' ? value.toFixed(1) : '--';
  }

  // Poll every 15s while the ranking tab is visible.
  $: if (activeTab === 'ranking') {
    loadOptimizer();
    if (!optimizerPollId) {
      optimizerPollId = setInterval(loadOptimizer, 15000);
    }
  } else if (optimizerPollId) {
    clearInterval(optimizerPollId);
    optimizerPollId = null;
  }

  let engineStatusUnsubscribe: (() => void) | null = null;

  onMount(async () => {
    initIndexerStore();
    loadConnections();

    // Listen for async engine_status events (emitted when WAL recovery finishes)
    if (window.runtime?.EventsOn) {
      engineStatusUnsubscribe = window.runtime.EventsOn('engine_status', async (status: string) => {
        engineStatus = status as typeof engineStatus;
        if (status === 'ready') {
          initIndexerStore();
          try {
            hasGPU = await CheckSystemGPU();
            gpuAcceleration = await GetGPUAcceleration();
            if (!hasGPU) gpuAcceleration = false;
          } catch {}
        }
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
    loadAccess();
  });

  onDestroy(() => {
    engineStatusUnsubscribe?.();
    if (optimizerPollId) clearInterval(optimizerPollId);
  });

  async function retryEngine() {
    if (retrying) return;
    retrying = true;
    try {
      await RetryEngineInit();
      const status = await GetEngineStatus();
      engineStatus = status as typeof engineStatus;
      if (status === 'ready') {
        initIndexerStore();
        try {
          hasGPU = await CheckSystemGPU();
          gpuAcceleration = await GetGPUAcceleration();
          if (!hasGPU) gpuAcceleration = false;
        } catch {}
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
    class:settings-page-mode={embedded}
    class="settings-overlay fixed inset-0 z-[1000] flex items-center justify-center p-4 bg-black/60 backdrop-blur-md"
    transition:fade={{ duration: 200 }}
    on:click|self={close}
  >
    <div 
      bind:this={modalElement}
      class="settings-dialog w-full max-w-4xl h-[640px] bg-[#0e0e0e] border border-[#474848]/20 shadow-[0_32px_64px_-12px_rgba(0,0,0,0.8)] flex overflow-hidden rounded-sm"
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
            class="settings-nav-item"
            class:active={activeTab === 'folders'}
          >
            <Folder class="w-4 h-4" />
            <span class="uppercase tracking-widest text-[10px]">Indexed Folders</span>
          </button>

          <button
            on:click={() => activeTab = 'hardware'}
            class="settings-nav-item"
            class:active={activeTab === 'hardware'}
          >
            <Cpu class="w-4 h-4" />
            <span class="uppercase tracking-widest text-[10px]">Hardware</span>
          </button>

          <button
            on:click={() => activeTab = 'ranking'}
            class="settings-nav-item"
            class:active={activeTab === 'ranking'}
          >
            <BarChart3 class="w-4 h-4" />
            <span class="uppercase tracking-widest text-[10px]">Ranking</span>
          </button>

          <button
            on:click={() => activeTab = 'connections'}
            class="settings-nav-item"
            class:active={activeTab === 'connections'}
          >
            <Network class="w-4 h-4" />
            <span class="uppercase tracking-widest text-[10px]">Connections</span>
          </button>

          <button
            on:click={() => activeTab = 'access'}
            class="settings-nav-item"
            class:active={activeTab === 'access'}
          >
            <Key class="w-4 h-4" />
            <span class="uppercase tracking-widest text-[10px]">Access</span>
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
                {#if $indexingStatus.searchReady}
                  <span class="text-[#e7e5e5]">{Math.round($indexingStatus.progress)}%</span>
                {/if}
              </div>
              <!-- TASK 2: Loading Bar -->
              <div class="h-1.5 w-full bg-[#252626] rounded-sm overflow-hidden">
                <div 
                  class="h-full bg-gradient-to-br from-[#bfc8ca] to-[#3f484a] transition-all duration-300 ease-out" 
                  class:animate-pulse={!$indexingStatus.searchReady}
                  style="width: {$indexingStatus.searchReady ? $indexingStatus.progress : 30}%"
                ></div>
              </div>
              <div class="text-[10px] text-[#acabab] font-sans tracking-tight whitespace-pre-line leading-snug">
                {$indexingStatus.statusMessage}
              </div>
            </div>
          {/if}
          
          {#if engineInitializing}
            <div class="p-3 bg-[#252626] rounded-sm space-y-2 border-l-2 border-[#bfc8ca]" transition:slide>
              <div class="flex items-center gap-1.5 text-[9px] font-bold uppercase tracking-widest text-[#bfc8ca]">
                <Clock class="w-3 h-3 animate-pulse" />
                <span>Starting Up</span>
              </div>
              <div class="text-[9px] text-[#acabab] leading-tight">
                Recovering database, please wait...
              </div>
            </div>
          {:else if engineError}
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
            {activeTab === 'folders' ? 'Indexing Preferences' : activeTab === 'hardware' ? 'System & Performance' : activeTab === 'ranking' ? 'Ranking Intelligence' : activeTab === 'connections' ? 'LLM & MCP Connections' : 'API & MCP Access'}
          </h3>
          <button 
            on:click={close}
            class="settings-close p-2 text-[#acabab] hover:text-[#e7e5e5] transition-colors"
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
                  Filosophy indexes Desktop, Documents, and Downloads by default. Add or enable more folders here to include their paths and contents in search.
                </p>
              </div>

              {#if engineInitializing}
                <div class="flex-1 flex flex-col items-center justify-center p-12 bg-[#131313] rounded-sm text-center space-y-6 border border-[#474848]/20">
                  <Clock class="w-12 h-12 text-[#bfc8ca] animate-pulse opacity-40" />
                  <div class="space-y-2">
                    <h5 class="text-xl font-serif text-[#e7e5e5] tracking-tight">Engine Starting Up</h5>
                    <p class="text-xs text-[#acabab] font-sans leading-relaxed max-w-xs opacity-60">
                      The search engine is recovering the database from a previous session. This usually takes a few seconds but can take longer after an unexpected shutdown.
                    </p>
                    <p class="text-[10px] font-sans text-[#acabab] opacity-40 pt-1">
                      The page will reload automatically when ready.
                    </p>
                  </div>
                </div>
              {:else if engineError}
                <div class="flex-1 flex flex-col items-center justify-center p-12 bg-[#131313] rounded-sm text-center space-y-6 border border-[#474848]/20">
                  <Activity class="w-12 h-12 text-[#bfc8ca] animate-pulse opacity-40" />
                  <div class="space-y-2">
                    <h5 class="text-xl font-serif text-[#e7e5e5] tracking-tight">Search Engine Offline</h5>
                    <p class="text-xs text-[#acabab] font-sans leading-relaxed max-w-xs opacity-60">
                      The AI backend failed to initialize. Indexing operations and vector search are currently disabled.
                    </p>
                    <p class="text-[10px] font-mono text-[#acabab] opacity-40 pt-2">
                      Check <code>filosophy.log</code> for details.
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
          {:else if activeTab === 'connections'}
            <div in:fade={{ duration: 200 }} class="space-y-10">

              <!-- Local LLM -->
              <section>
                <div class="mb-8 flex items-center justify-between">
                  <div>
                    <h4 class="text-3xl font-serif text-[#e7e5e5] tracking-[-0.02em] font-medium mb-3">Local LLM</h4>
                    <p class="text-xs text-[#acabab] font-sans tracking-tight leading-relaxed max-w-lg opacity-80">
                      Connect Filosophy to a locally running language model for AI-powered search summaries and chat.
                    </p>
                  </div>
                </div>

                <!-- Provider selector -->
                <div class="flex flex-wrap gap-2 mb-8">
                  {#each [
                    { id: 'none', label: 'None' },
                    { id: 'ollama', label: 'Ollama' },
                    { id: 'lmstudio', label: 'LM Studio' },
                    { id: 'openai-compatible', label: 'OpenAI-compat.' },
                  ] as p}
                    <button
                      on:click={() => providerDefaults(p.id as LLMConfig['provider'])}
                      class="px-4 py-2 text-[10px] font-sans font-bold uppercase tracking-widest rounded-sm transition-all border
                             {llmConfig.provider === p.id
                               ? 'bg-[#252626] text-[#e7e5e5] border-[#474848]/30 shadow-inner'
                               : 'bg-[#131313] text-[#acabab] border-transparent hover:text-[#e7e5e5] hover:bg-[#1a1a1a]'}"
                    >
                      {p.label}
                    </button>
                  {/each}
                </div>

                {#if llmConfig.provider !== 'none'}
                  <div class="space-y-4 p-8 bg-[#131313] rounded-sm border border-[#474848]/10 shadow-inner" transition:slide={{ duration: 200 }}>
                    <div class="flex gap-4">
                      <div class="flex-1">
                        <label for="llm-base-url" class="block text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] mb-2 opacity-60">Base URL</label>
                        <input
                          id="llm-base-url"
                          type="text"
                          bind:value={llmConfig.baseUrl}
                          on:change={saveLLM}
                          placeholder="http://localhost:11434"
                          class="w-full px-4 py-3 text-xs font-sans rounded-sm bg-[#0e0e0e] text-[#e7e5e5] border-none focus:outline-none focus:ring-1 focus:ring-[#bfc8ca]/30 transition-all placeholder:text-[#acabab]/40"
                        />
                      </div>
                      <div class="w-48">
                        <label for="llm-model" class="block text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] mb-2 opacity-60">Model</label>
                        <input
                          id="llm-model"
                          type="text"
                          bind:value={llmConfig.model}
                          on:change={saveLLM}
                          placeholder="llama3"
                          class="w-full px-4 py-3 text-xs font-sans rounded-sm bg-[#0e0e0e] text-[#e7e5e5] border-none focus:outline-none focus:ring-1 focus:ring-[#bfc8ca]/30 transition-all placeholder:text-[#acabab]/40"
                        />
                      </div>
                    </div>

                    {#if llmConfig.provider === 'openai-compatible'}
                      <div transition:slide={{ duration: 150 }}>
                        <label for="llm-api-key" class="block text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] mb-2 opacity-60">API Key <span class="normal-case opacity-50">(optional)</span></label>
                        <input
                          id="llm-api-key"
                          type="password"
                          bind:value={llmConfig.apiKey}
                          on:change={saveLLM}
                          placeholder="sk-..."
                          class="w-full px-4 py-3 text-xs font-sans rounded-sm bg-[#0e0e0e] text-[#e7e5e5] border-none focus:outline-none focus:ring-1 focus:ring-[#bfc8ca]/30 transition-all placeholder:text-[#acabab]/40"
                        />
                      </div>
                    {/if}

                    <div class="flex items-center gap-4 pt-4 border-t border-[#474848]/10 mt-4">
                      <button
                        on:click={testLLMConnection}
                        disabled={llmTestStatus === 'testing'}
                        class="px-6 py-2.5 bg-[#252626] hover:bg-[#2b2c2c] text-[#e7e5e5] text-[10px] font-sans uppercase tracking-widest font-bold rounded-sm transition-all border border-[#474848]/30 hover:border-[#bfc8ca]/50 disabled:opacity-50 flex items-center gap-2"
                      >
                        {#if llmTestStatus === 'testing'}
                          <RefreshCw class="w-3.5 h-3.5 animate-spin text-[#bfc8ca]" />
                          <span>Testing...</span>
                        {:else}
                          <RefreshCw class="w-3.5 h-3.5" />
                          <span>Test Connection</span>
                        {/if}
                      </button>

                      {#if llmTestStatus === 'ok'}
                        <span class="flex items-center gap-1.5 text-[10px] font-sans font-bold uppercase tracking-widest text-[#bfc8ca]" transition:fade>
                          <CheckCircle class="w-3.5 h-3.5" />
                          {llmTestMessage}
                        </span>
                      {:else if llmTestStatus === 'error'}
                        <span class="flex items-center gap-1.5 text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab]" transition:fade>
                          <AlertCircle class="w-3.5 h-3.5" />
                          <span class="truncate max-w-xs">{llmTestMessage}</span>
                        </span>
                      {/if}
                    </div>
                  </div>
                {/if}
              </section>

              <!-- MCP Servers -->
              <section>
                <div class="mb-4">
                  <h4 class="text-3xl font-serif text-[#e7e5e5] tracking-[-0.02em] font-medium mb-3">MCP Servers</h4>
                  <p class="text-xs text-[#acabab] font-sans tracking-tight leading-relaxed max-w-lg opacity-80">
                    Register Model Context Protocol servers to extend Filosophy with tools, prompts, and data sources.
                  </p>
                </div>

                <div class="mb-8">
                  <button
                    on:click={() => showAddServer = !showAddServer}
                    class="flex items-center gap-2 px-6 py-2.5 bg-[#252626] hover:bg-[#2b2c2c] text-[#e7e5e5] text-[10px] font-sans uppercase tracking-widest font-bold rounded-sm border border-[#474848]/20 transition-all w-fit"
                  >
                    <Plus class="w-3.5 h-3.5" />
                    <span>Add Server</span>
                  </button>
                </div>

                <!-- Add server form -->
                {#if showAddServer}
                  <div class="mb-6 p-8 bg-[#131313] rounded-sm border border-[#474848]/10 shadow-inner space-y-4" transition:slide={{ duration: 200 }}>
                    <div class="flex gap-4">
                      <div class="flex-1">
                        <label for="mcp-name" class="block text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] mb-2 opacity-60">Name</label>
                        <input
                          id="mcp-name"
                          type="text"
                          bind:value={newServer.name}
                          placeholder="My MCP Server"
                          class="w-full px-4 py-3 text-xs font-sans rounded-sm bg-[#0e0e0e] text-[#e7e5e5] border-none focus:outline-none focus:ring-1 focus:ring-[#bfc8ca]/30 transition-all placeholder:text-[#acabab]/40"
                        />
                      </div>
                      <div class="w-40">
                        <label for="mcp-type" class="block text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] mb-2 opacity-60">Type</label>
                        <select
                          id="mcp-type"
                          bind:value={newServer.type}
                          class="w-full px-4 py-3 text-xs font-sans rounded-sm bg-[#0e0e0e] text-[#e7e5e5] border-none focus:outline-none focus:ring-1 focus:ring-[#bfc8ca]/30 transition-all appearance-none"
                        >
                          <option value="stdio">stdio</option>
                          <option value="sse">SSE</option>
                          <option value="http">HTTP</option>
                        </select>
                      </div>
                    </div>

                    {#if newServer.type === 'stdio'}
                      <div class="flex gap-4" transition:slide={{ duration: 150 }}>
                        <div class="flex-1">
                          <label for="mcp-command" class="block text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] mb-2 opacity-60">Command</label>
                          <input
                            id="mcp-command"
                            type="text"
                            bind:value={newServer.command}
                            placeholder="npx -y @modelcontextprotocol/server-filesystem"
                            class="w-full px-4 py-3 text-xs font-mono rounded-sm bg-[#0e0e0e] text-[#bfc8ca] border-none focus:outline-none focus:ring-1 focus:ring-[#bfc8ca]/30 transition-all placeholder:text-[#acabab]/40"
                          />
                        </div>
                        <div class="w-48">
                          <label for="mcp-args" class="block text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] mb-2 opacity-60">Args <span class="normal-case opacity-50">(optional)</span></label>
                          <input
                            id="mcp-args"
                            type="text"
                            bind:value={newServer.args}
                            placeholder="/path/to/dir"
                            class="w-full px-4 py-3 text-xs font-mono rounded-sm bg-[#0e0e0e] text-[#bfc8ca] border-none focus:outline-none focus:ring-1 focus:ring-[#bfc8ca]/30 transition-all placeholder:text-[#acabab]/40"
                          />
                        </div>
                      </div>
                    {:else}
                      <div transition:slide={{ duration: 150 }}>
                        <label for="mcp-url" class="block text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] mb-2 opacity-60">URL</label>
                        <input
                          id="mcp-url"
                          type="text"
                          bind:value={newServer.url}
                          placeholder="http://localhost:3000/mcp"
                          class="w-full px-4 py-3 text-xs font-mono rounded-sm bg-[#0e0e0e] text-[#bfc8ca] border-none focus:outline-none focus:ring-1 focus:ring-[#bfc8ca]/30 transition-all placeholder:text-[#acabab]/40"
                        />
                      </div>
                    {/if}

                    <div class="flex justify-end gap-3 pt-4 border-t border-[#474848]/10 mt-4">
                      <button
                        on:click={() => { showAddServer = false; newServer = { type: 'stdio', enabled: true }; }}
                        class="px-5 py-2 text-[10px] font-sans uppercase tracking-widest font-bold rounded-sm text-[#acabab] hover:text-[#e7e5e5] transition-colors"
                      >
                        Cancel
                      </button>
                      <button
                        on:click={addMCPServer}
                        disabled={!newServer.name}
                        class="px-6 py-2 bg-[#252626] hover:bg-[#2b2c2c] text-[#e7e5e5] text-[10px] font-sans uppercase tracking-widest font-bold rounded-sm border border-[#474848]/30 hover:border-[#bfc8ca]/50 transition-all disabled:opacity-50"
                      >
                        Save Server
                      </button>
                    </div>
                  </div>
                {/if}

                <!-- Server list -->
                {#if mcpServers.length === 0 && !showAddServer}
                  <div class="flex flex-col items-center justify-center p-12 bg-[#131313] rounded-sm text-center border border-[#474848]/10 shadow-inner">
                    <Network class="w-8 h-8 mb-4 text-[#acabab] opacity-40" />
                    <p class="text-sm font-sans font-medium text-[#e7e5e5]">No MCP servers configured</p>
                    <p class="text-[10px] font-sans text-[#acabab] mt-1 opacity-60">Use the button above to extend your AI tools</p>
                  </div>
                {:else}
                  <div class="space-y-2">
                    {#each mcpServers as server (server.id)}
                      <div class="flex items-center gap-4 px-5 py-4 bg-[#131313] rounded-sm group transition-all" transition:slide={{ duration: 150 }}>
                        <!-- Enable toggle -->
                        <button
                          on:click={() => toggleMCPServer(server.id)}
                          aria-label="{server.enabled ? 'Disable' : 'Enable'} {server.name}"
                          class="shrink-0 transition-colors {server.enabled ? 'text-[#bfc8ca]' : 'text-[#acabab] opacity-50'}"
                        >
                          {#if server.enabled}
                            <CheckCircle class="w-4 h-4" />
                          {:else}
                            <Circle class="w-4 h-4" />
                          {/if}
                        </button>

                        <!-- Info -->
                        <div class="flex-1 min-w-0">
                          <div class="flex items-center gap-3">
                            <span class="text-sm font-sans font-medium text-[#e7e5e5] truncate">{server.name}</span>
                            <span class="px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-widest rounded-sm bg-[#0e0e0e] text-[#acabab]">{server.type}</span>
                          </div>
                          <p class="text-[10px] font-mono text-[#bfc8ca] opacity-70 truncate mt-1.5">
                            {server.type === 'stdio' ? [server.command, server.args].filter(Boolean).join(' ') : server.url}
                          </p>
                        </div>

                        <!-- Delete -->
                        <button
                          on:click={() => removeMCPServer(server.id)}
                          aria-label="Remove {server.name}"
                          class="shrink-0 text-[#acabab] hover:text-red-400 opacity-0 group-hover:opacity-100 transition-all p-2"
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

          {:else if activeTab === 'ranking'}
            <div in:fade={{ duration: 200 }} class="space-y-10">
              <section>
                <div class="mb-8">
                  <h4 class="text-3xl font-serif text-[#e7e5e5] tracking-[-0.02em] font-medium mb-3">Telemetry Optimizer</h4>
                  <p class="text-xs text-[#acabab] font-sans tracking-tight leading-relaxed max-w-lg opacity-80">
                    The optimizer learns from your thumbs-up/down feedback and adjusts ranking weights to surface better results over time.
                  </p>
                </div>

                {#if optimizerLoading}
                  <div class="flex items-center gap-3 p-8 bg-[#131313] rounded-sm border border-[#474848]/10">
                    <RefreshCw class="w-4 h-4 animate-spin text-[#bfc8ca]" />
                    <span class="text-xs text-[#acabab]">Loading optimizer status...</span>
                  </div>
                {:else if !optimizerStatus}
                  <div class="flex flex-col items-center justify-center p-12 bg-[#131313] rounded-sm text-center border border-[#474848]/10 shadow-inner">
                    <BarChart3 class="w-8 h-8 mb-4 text-[#acabab] opacity-40" />
                    <p class="text-sm font-sans font-medium text-[#e7e5e5]">Optimizer not yet started</p>
                    <p class="text-[10px] font-sans text-[#acabab] mt-1 opacity-60">It begins after the initial indexing completes</p>
                  </div>
                {:else}
                  <!-- Last Action -->
                  <div class="bg-[#131313] rounded-sm p-8 border border-[#474848]/10 shadow-inner space-y-5">
                    <div class="flex items-center justify-between">
                      <span class="text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] opacity-60">Last Action</span>
                      {#if optimizerStatus.lastRunAt}
                        <span class="text-[10px] font-mono text-[#acabab] opacity-40">
                          {new Date(optimizerStatus.lastRunAt).toLocaleTimeString()}
                        </span>
                      {/if}
                    </div>
                    <p class="text-xs font-sans text-[#e7e5e5] leading-relaxed">
                      {optimizerStatus.lastAction || 'Waiting for first run...'}
                    </p>
                    <div class="flex items-center gap-6 pt-3 border-t border-[#474848]/10">
                      <div class="flex items-center gap-2">
                        <span class="text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] opacity-60">Feedback votes</span>
                        <span class="text-sm font-mono text-[#bfc8ca] font-bold">{optimizerStatus.feedbackCount}</span>
                      </div>
                      <div class="flex items-center gap-2">
                        <span class="text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] opacity-60">Preference score</span>
                        <span class="text-sm font-mono text-[#bfc8ca] font-bold">{formatPreferenceScore(optimizerStatus.preferenceScore)}</span>
                      </div>
                      <div class="flex items-center gap-2">
                        <span class="text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] opacity-60">Baseline</span>
                        <span class="text-sm font-mono text-[#acabab] font-bold opacity-70">{formatPreferenceScore(optimizerStatus.baselineScore)}</span>
                      </div>
                    </div>
                  </div>

                  <div class="grid grid-cols-2 md:grid-cols-4 gap-3 mt-4">
                    <div class="bg-[#131313] border border-[#474848]/10 rounded-sm p-4">
                      <div class="text-[9px] font-sans font-bold uppercase tracking-widest text-[#acabab] opacity-50 mb-2">NDCG@10</div>
                      <div class="text-lg font-mono text-[#e7e5e5]">{formatMetric(optimizerStatus.ndcg10)}</div>
                    </div>
                    <div class="bg-[#131313] border border-[#474848]/10 rounded-sm p-4">
                      <div class="text-[9px] font-sans font-bold uppercase tracking-widest text-[#acabab] opacity-50 mb-2">MRR@10</div>
                      <div class="text-lg font-mono text-[#e7e5e5]">{formatMetric(optimizerStatus.mrr10)}</div>
                    </div>
                    <div class="bg-[#131313] border border-[#474848]/10 rounded-sm p-4">
                      <div class="text-[9px] font-sans font-bold uppercase tracking-widest text-[#acabab] opacity-50 mb-2">Recall@{optimizerStatus.currentWeights?.RerankTopN ?? 8}</div>
                      <div class="text-lg font-mono text-[#e7e5e5]">{formatMetric(optimizerStatus.recallAtRerank)}</div>
                    </div>
                    <div class="bg-[#131313] border border-[#474848]/10 rounded-sm p-4">
                      <div class="text-[9px] font-sans font-bold uppercase tracking-widest text-[#acabab] opacity-50 mb-2">Downvote penalty</div>
                      <div class="text-lg font-mono text-red-400">{formatMetric(optimizerStatus.downvotePenalty)}</div>
                    </div>
                  </div>
                {/if}
              </section>

              <!-- Weights Table -->
              {#if optimizerStatus?.currentWeights}
                <section>
                  <div class="mb-6">
                    <h4 class="text-xl font-serif text-[#e7e5e5] tracking-[-0.02em] font-medium mb-2">Active Weights</h4>
                    <p class="text-[10px] text-[#acabab] font-sans tracking-tight leading-relaxed opacity-60">
                      Current values vs. defaults. Highlighted rows have been tuned by the optimizer.
                    </p>
                  </div>

                  <div class="bg-[#131313] rounded-sm border border-[#474848]/10 shadow-inner overflow-hidden">
                    <table class="w-full text-xs font-sans">
                      <thead>
                        <tr class="border-b border-[#474848]/10">
                          <th class="text-left px-6 py-3 text-[10px] font-bold uppercase tracking-widest text-[#acabab] opacity-60">Signal</th>
                          <th class="text-right px-6 py-3 text-[10px] font-bold uppercase tracking-widest text-[#acabab] opacity-60">Current</th>
                          <th class="text-right px-6 py-3 text-[10px] font-bold uppercase tracking-widest text-[#acabab] opacity-60">Default</th>
                          <th class="text-right px-6 py-3 text-[10px] font-bold uppercase tracking-widest text-[#acabab] opacity-60">Delta</th>
                        </tr>
                      </thead>
                      <tbody>
                        {#each [
                          { label: 'Path FTS', key: 'WPathFTS' },
                          { label: 'Path Prefix', key: 'WPathPrefix' },
                          { label: 'Content FTS', key: 'WContentFTS' },
                          { label: 'Semantic Text', key: 'WSemanticText' },
                          { label: 'Semantic Image', key: 'WSemanticImg' },
                          { label: 'Filename', key: 'WFilename' },
                          { label: 'Recency', key: 'WRecency' },
                          { label: 'Reranker Blend', key: 'WRerankerBlend' },
                        ] as row}
                          {@const current = optimizerStatus.currentWeights[row.key] ?? 0}
                          {@const def = optimizerStatus.defaultWeights[row.key] ?? 0}
                          {@const delta = current - def}
                          {@const changed = Math.abs(delta) > 0.001}
                          <tr class="border-b border-[#474848]/5 {changed ? 'bg-[#bfc8ca]/5' : ''}">
                            <td class="px-6 py-3 text-[#e7e5e5] {changed ? 'font-medium' : ''}">{row.label}</td>
                            <td class="px-6 py-3 text-right font-mono text-[#bfc8ca]">{current.toFixed(1)}</td>
                            <td class="px-6 py-3 text-right font-mono text-[#acabab] opacity-50">{def.toFixed(1)}</td>
                            <td class="px-6 py-3 text-right font-mono {delta > 0 ? 'text-green-400' : delta < 0 ? 'text-red-400' : 'text-[#acabab] opacity-30'}">
                              {changed ? (delta > 0 ? '+' : '') + delta.toFixed(1) : '--'}
                            </td>
                          </tr>
                        {/each}
                      </tbody>
                    </table>
                  </div>
                </section>
              {/if}

              <section class="p-6 bg-[#131313] border-l-2 border-[#bfc8ca] rounded-sm shadow-sm">
                <h5 class="text-[#bfc8ca] text-[10px] font-bold uppercase tracking-widest mb-3">How It Works</h5>
                <p class="text-[11px] font-sans text-[#acabab] leading-relaxed opacity-80">
                  Every 10 minutes the optimizer scores weight combinations with NDCG@10, MRR@10, and Recall@rerank-top-N. Thumbs up means the document was relevant; thumbs down applies a harsh top-rank penalty so unwanted topics stop winning.
                </p>
              </section>
            </div>

          {:else if activeTab === 'access'}
            <div in:fade={{ duration: 200 }} class="space-y-10">

              <!-- REST API -->
              <section>
                <div class="mb-8 flex items-center justify-between">
                  <div>
                    <h4 class="text-3xl font-serif text-[#e7e5e5] tracking-[-0.02em] font-medium mb-3">REST API</h4>
                    <p class="text-xs text-[#acabab] font-sans tracking-tight leading-relaxed max-w-lg opacity-80">
                      Expose a local HTTP endpoint so external tools and scripts can search your files programmatically.
                    </p>
                  </div>
                  <!-- Toggle -->
                  <button
                    on:click={toggleAPI}
                    disabled={accessLoading}
                    aria-label="Toggle REST API"
                    class="relative inline-flex h-6 w-12 shrink-0 cursor-pointer rounded-sm border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none disabled:opacity-30
                           {apiEnabled ? 'bg-[#bfc8ca]' : 'bg-[#252626]'}"
                  >
                    <span class="pointer-events-none inline-block h-5 w-5 transform rounded-sm bg-[#0e0e0e] shadow-lg transition duration-200 ease-in-out {apiEnabled ? 'translate-x-6' : 'translate-x-0'}"></span>
                  </button>
                </div>

                {#if apiEnabled}
                  <div class="space-y-4" transition:slide={{ duration: 200 }}>
                    <!-- Port -->
                    <div class="bg-[#131313] p-8 rounded-sm border border-[#474848]/10 shadow-inner space-y-6">
                      <div class="flex items-end gap-6">
                        <div>
                          <label for="api-port" class="block text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] mb-2 opacity-60">Port</label>
                          <input
                            id="api-port"
                            type="number"
                            min="1"
                            max="65535"
                            bind:value={apiPortInput}
                            on:blur={savePort}
                            on:keydown={e => e.key === 'Enter' && savePort()}
                            class="w-32 px-4 py-3 text-xs rounded-sm bg-[#0e0e0e] text-[#e7e5e5] border-none focus:outline-none focus:ring-1 focus:ring-[#bfc8ca]/30 font-mono transition-all"
                          />
                        </div>
                        <p class="text-[10px] text-[#acabab] pb-3 opacity-60 flex items-center gap-2">
                          Listening on <span class="font-mono text-[#bfc8ca] bg-[#0e0e0e] px-2 py-1 rounded-sm">http://127.0.0.1:{apiPort}</span>
                        </p>
                      </div>

                      <!-- API Key -->
                      <div>
                        <label for="api-key-display" class="block text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] mb-2 opacity-60">
                          API Key <span class="normal-case opacity-50">(empty = no auth)</span>
                        </label>
                        <div class="flex items-center gap-3">
                          <div id="api-key-display" class="flex-1 px-4 py-3 text-xs bg-[#0e0e0e] text-[#bfc8ca] font-mono rounded-sm truncate select-all">
                            {apiKey || '(no key — open access)'}
                          </div>
                          {#if apiKey}
                            <button
                              on:click={copyKey}
                              title="Copy key"
                              class="shrink-0 p-3 bg-[#252626] hover:bg-[#2b2c2c] rounded-sm transition-colors text-[#e7e5e5]"
                            >
                              {#if apiKeyCopied}
                                <CheckCircle class="w-4 h-4 text-[#bfc8ca]" />
                              {:else}
                                <Copy class="w-4 h-4" />
                              {/if}
                            </button>
                          {/if}
                          <button
                            on:click={regenKey}
                            title="{apiKey ? 'Regenerate key' : 'Generate key'}"
                            class="shrink-0 p-3 bg-[#252626] hover:bg-[#2b2c2c] rounded-sm transition-colors text-[#e7e5e5]"
                          >
                            <RefreshCw class="w-4 h-4" />
                          </button>
                        </div>
                        <p class="text-[10px] text-[#acabab] mt-2 opacity-60">
                          Pass as <span class="font-mono text-[#bfc8ca]">Authorization: Bearer &lt;key&gt;</span>
                        </p>
                      </div>
                    </div>

                    <!-- CLI hint -->
                    <div class="p-6 bg-[#131313] border-l-2 border-[#bfc8ca] rounded-sm shadow-sm">
                      <p class="text-[10px] font-bold uppercase tracking-widest text-[#bfc8ca] mb-2">CLI Usage</p>
                      <code class="text-xs font-mono text-[#acabab] bg-[#0e0e0e] px-3 py-2 rounded-sm block">
                        filo --key $FILOSOPHY_API_KEY search "books by camus"
                      </code>
                    </div>
                  </div>
                {:else}
                  <div class="flex items-center gap-3 p-6 bg-[#131313] rounded-sm text-[#acabab] opacity-80" transition:slide={{ duration: 150 }}>
                    <Globe class="w-4 h-4 shrink-0 opacity-60" />
                    <span class="text-[11px] font-sans">Enable to expose a local HTTP API on port <span class="font-mono text-[#bfc8ca] bg-[#0e0e0e] px-1.5 py-0.5 rounded-sm">{apiPort}</span></span>
                  </div>
                {/if}
              </section>

              <!-- MCP Server -->
              <section>
                <div class="mb-8 flex items-center justify-between">
                  <div>
                    <h4 class="text-3xl font-serif text-[#e7e5e5] tracking-[-0.02em] font-medium mb-3">MCP Server</h4>
                    <p class="text-xs text-[#acabab] font-sans tracking-tight leading-relaxed max-w-lg opacity-80">
                      Serve Filosophy as an MCP tool over HTTP+SSE so Claude Desktop and other agents can call <code class="font-mono text-[#bfc8ca] bg-[#131313] px-1.5 py-0.5 rounded-sm">search_files</code> natively.
                    </p>
                  </div>
                  <button
                    on:click={toggleMCP}
                    disabled={accessLoading}
                    aria-label="Toggle MCP Server"
                    class="relative inline-flex h-6 w-12 shrink-0 cursor-pointer rounded-sm border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none disabled:opacity-30
                           {mcpEnabled ? 'bg-[#bfc8ca]' : 'bg-[#252626]'}"
                  >
                    <span class="pointer-events-none inline-block h-5 w-5 transform rounded-sm bg-[#0e0e0e] shadow-lg transition duration-200 ease-in-out {mcpEnabled ? 'translate-x-6' : 'translate-x-0'}"></span>
                  </button>
                </div>

                {#if mcpEnabled}
                  <div class="space-y-4" transition:slide={{ duration: 200 }}>
                    <div class="bg-[#131313] p-8 rounded-sm border border-[#474848]/10 shadow-inner space-y-6">
                      <!-- MCP Key -->
                      <div>
                        <label for="mcp-server-key" class="block text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] mb-2 opacity-60">
                          MCP Key <span class="normal-case opacity-50">(empty = no auth)</span>
                        </label>
                        <div class="flex items-center gap-2">
                          <input
                            id="mcp-server-key"
                            type="text"
                            bind:value={mcpKey}
                            on:blur={saveMCPKey}
                            on:keydown={e => e.key === 'Enter' && saveMCPKey()}
                            placeholder="Leave empty to disable auth"
                            class="flex-1 px-4 py-3 text-xs bg-[#0e0e0e] text-[#bfc8ca] font-mono rounded-sm border-none focus:outline-none focus:ring-1 focus:ring-[#bfc8ca]/30 transition-all placeholder:text-[#acabab]/40"
                          />
                        </div>
                        <p class="text-[10px] text-[#acabab] mt-2 opacity-60">
                          Independent from the REST API key. Pass as <span class="font-mono text-[#bfc8ca]">Authorization: Bearer &lt;key&gt;</span>
                        </p>
                      </div>

                      <!-- Endpoint info -->
                      <div>
                        <p class="text-[10px] font-sans font-bold uppercase tracking-widest text-[#acabab] mb-2 opacity-60">SSE Endpoint</p>
                        <p class="text-xs font-mono text-[#bfc8ca] bg-[#0e0e0e] px-4 py-3 rounded-sm inline-block">http://127.0.0.1:{apiPort}/mcp/sse</p>
                      </div>
                    </div>

                    <!-- Claude Desktop snippet -->
                    <div class="p-6 bg-[#131313] rounded-sm space-y-3">
                      <div class="flex items-center justify-between">
                        <p class="text-[10px] font-bold uppercase tracking-widest text-[#bfc8ca]">Claude Desktop Config</p>
                        <button
                          on:click={copySnippet}
                          class="flex items-center gap-2 px-3 py-1.5 bg-[#252626] hover:bg-[#2b2c2c] text-[#e7e5e5] text-[10px] font-sans uppercase tracking-widest font-bold rounded-sm transition-colors"
                        >
                          {#if snippetCopied}
                            <CheckCircle class="w-3.5 h-3.5 text-[#bfc8ca]" />
                            <span class="text-[#bfc8ca]">Copied</span>
                          {:else}
                            <Copy class="w-3.5 h-3.5" />
                            <span>Copy</span>
                          {/if}
                        </button>
                      </div>
                      <pre class="px-4 py-4 text-xs font-mono text-[#bfc8ca] bg-[#0e0e0e] rounded-sm overflow-x-auto whitespace-pre leading-relaxed">{claudeDesktopSnippet}</pre>
                      <p class="text-[10px] text-[#acabab] opacity-60 pt-1">
                        Add this to your <span class="font-mono text-[#bfc8ca]">claude_desktop_config.json</span>
                      </p>
                    </div>
                  </div>
                {:else}
                  <div class="flex items-center gap-3 p-6 bg-[#131313] rounded-sm text-[#acabab] opacity-80" transition:slide={{ duration: 150 }}>
                    <Lock class="w-4 h-4 shrink-0 opacity-60" />
                    <span class="text-[11px] font-sans">Enable to serve Filosophy as an MCP tool on <span class="font-mono text-[#bfc8ca] bg-[#0e0e0e] px-1.5 py-0.5 rounded-sm">http://127.0.0.1:{apiPort}/mcp/sse</span></span>
                  </div>
                {/if}
              </section>

            </div>

          {/if}
        </div>
      </main>
    </div>
  </div>
{/if}

<style>
  .settings-nav-item { width: 100%; min-height: 40px; display: flex; align-items: center; gap: 10px; padding: 0 11px; border: 0; border-radius: 4px; color: #bdcbc4; background: transparent; text-align: left; cursor: pointer; transition: background 120ms ease, color 120ms ease; }
  .settings-nav-item:hover { color: #fff; background: #243a34; }
  .settings-nav-item.active { color: #fff; background: #2b443a; box-shadow: inset 2px 0 #79b899; }
  .settings-nav-item :global(svg) { width: 16px; height: 16px; flex: 0 0 auto; color: #9db0a6; }
  .settings-nav-item.active :global(svg) { color: #91c9aa; }
  .settings-nav-item :global(span) { font-size: 11px; letter-spacing: 0; text-transform: none; }
  :global(.settings-dialog) { width: 100%; height: min(760px, calc(100dvh - 40px)); max-width: 980px; color: var(--f-text) !important; background: var(--f-surface) !important; border: 1px solid var(--f-border) !important; border-radius: 6px !important; box-shadow: 0 24px 80px rgb(9 24 17 / 30%) !important; }
  :global(.settings-dialog > aside) { width: 216px !important; flex: 0 0 216px !important; padding: 24px 12px 14px !important; color: var(--f-sidebar-text) !important; background: var(--f-sidebar) !important; border-color: #30413a !important; }
  :global(.settings-dialog > aside > div:first-child) { margin: 0 8px 22px !important; padding: 0 !important; }
  :global(.settings-dialog > aside > div:first-child h2) { color: #e1eae5 !important; font-size: 13px !important; letter-spacing: 0 !important; text-transform: none !important; }
  :global(.settings-dialog > aside nav) { padding: 0 !important; }
  :global(.settings-dialog > aside > div:last-child) { padding: 16px 8px 4px !important; background: var(--f-sidebar) !important; border-top: 1px solid #30413a; }
  :global(.settings-dialog main), :global(.settings-dialog main > header) { background: var(--f-surface) !important; }
  :global(.settings-dialog main > header) { height: 60px !important; padding: 0 30px !important; border-bottom: 1px solid var(--f-border); }
  :global(.settings-dialog main > header h3) { color: var(--f-text-2) !important; font-size: 11px !important; letter-spacing: 0 !important; text-transform: none !important; }
  :global(.settings-dialog main > div) { padding: 25px 30px 28px !important; }
  :global(.settings-dialog main [class*="bg-[#0e0e0e]"], .settings-dialog main [class*="bg-[#131313]"], .settings-dialog main [class*="bg-[#252626]"], .settings-dialog main [class*="bg-[#1a1a1a]"]) { background: var(--f-surface-2) !important; }
  :global(.settings-dialog main input:not(.folder-search-input):not(.folder-checkbox):not([type="checkbox"]):not([type="radio"])), :global(.settings-dialog main select), :global(.settings-dialog main textarea) { border: 1px solid var(--f-border-strong) !important; border-radius: 4px !important; background: var(--f-surface) !important; }
  :global(.settings-dialog [class*="text-[#e7e5e5]"]) { color: var(--f-text) !important; }
  :global(.settings-dialog [class*="text-[#acabab]"]) { color: var(--f-text-2) !important; }
  :global(.settings-dialog [class*="text-[#bfc8ca]"]) { color: var(--f-accent) !important; }
  :global(.settings-dialog [class*="border-[#474848]"]) { border-color: var(--f-border) !important; }
  :global(.settings-dialog h4) { font-family: Inter, "Segoe UI", sans-serif !important; font-size: 20px !important; line-height: 1.3 !important; letter-spacing: 0 !important; }
  :global(.settings-dialog .font-serif) { font-family: Inter, "Segoe UI", sans-serif !important; color: var(--f-text) !important; }
  :global(.settings-dialog main p[class*="opacity-"]) { opacity: 1 !important; }
  :global(.settings-dialog button) { border-radius: 4px; }
  :global(.settings-dialog input:not(.folder-search-input):not(.folder-checkbox):not([type="checkbox"]):not([type="radio"])), :global(.settings-dialog select), :global(.settings-dialog textarea) { color: var(--f-text) !important; }
  :global(.settings-dialog .folder-checkbox) { background: var(--f-surface) !important; border: 1.5px solid var(--f-border-strong) !important; }
  :global(.settings-dialog .folder-checkbox:checked) { background: var(--f-accent) !important; border-color: var(--f-accent) !important; }
  :global(.settings-dialog .folder-checkbox:checked::after) { content: '' !important; position: absolute !important; left: 4px !important; top: 1px !important; width: 5px !important; height: 9px !important; border: solid #ffffff !important; border-width: 0 2px 2px 0 !important; transform: rotate(45deg) !important; display: block !important; }
  :global(.settings-page-mode.settings-overlay) { position: static !important; z-index: auto !important; display: block !important; min-height: 0; padding: 0 !important; background: transparent !important; backdrop-filter: none !important; }
  :global(.settings-page-mode .settings-dialog) { height: auto !important; min-height: 0; max-width: none; flex-direction: column; overflow: visible; margin: 0 auto; border: 0 !important; border-radius: 0 !important; background: transparent !important; box-shadow: none !important; }
  :global(.settings-page-mode .settings-dialog > aside) { width: 100% !important; flex: 0 0 auto !important; padding: 0 !important; background: transparent !important; border: 0 !important; border-bottom: 1px solid var(--f-border) !important; }
  :global(.settings-page-mode .settings-dialog > aside > div:first-child), :global(.settings-page-mode .settings-dialog > aside > div:last-child) { display: none !important; }
  :global(.settings-page-mode .settings-dialog > aside nav) { display: flex; gap: 4px; overflow-x: auto; padding: 0 0 8px !important; scrollbar-width: none; }
  :global(.settings-page-mode .settings-dialog > aside nav::-webkit-scrollbar) { display: none; }
  :global(.settings-page-mode .settings-nav-item) { width: auto; min-width: max-content; min-height: 38px; padding: 0 12px; color: var(--f-text-2); }
  :global(.settings-page-mode .settings-nav-item:hover) { color: var(--f-text); background: var(--f-surface-2); }
  :global(.settings-page-mode .settings-nav-item.active) { color: var(--f-accent); background: var(--f-accent-soft); box-shadow: inset 0 -2px var(--f-accent); }
  :global(.settings-page-mode .settings-nav-item svg), :global(.settings-page-mode .settings-nav-item.active svg) { color: currentColor; }
  :global(.settings-page-mode .settings-dialog main), :global(.settings-page-mode .settings-dialog main > header) { background: transparent !important; }
  :global(.settings-page-mode .settings-dialog main > header) { height: auto !important; min-height: 50px; padding: 18px 0 10px !important; border: 0; }
  :global(.settings-page-mode .settings-dialog main > div) { overflow: visible !important; padding: 8px 0 28px !important; }
  :global(.settings-page-mode .settings-dialog main > div > div > div.bg-\[\#131313\]) { padding: 0 !important; border: 0 !important; background: transparent !important; box-shadow: none !important; }
  :global(.settings-page-mode .settings-close) { display: none !important; }
  :global(.settings-page-mode .settings-dialog main button) { letter-spacing: 0 !important; text-transform: none !important; }
  :global(.settings-page-mode .settings-dialog main label) { letter-spacing: 0 !important; text-transform: none !important; opacity: 1 !important; }
  @media (max-width: 640px) {
    :global(.settings-overlay) { padding: 0 !important; }
    :global(.settings-dialog) { width: 100% !important; height: 100dvh !important; max-width: none !important; border-radius: 0 !important; flex-direction: column !important; }
    :global(.settings-dialog > aside) { width: 100% !important; flex: 0 0 auto !important; padding-top: 12px !important; }
    :global(.settings-dialog > aside > div:first-child) { display: none; }
    :global(.settings-dialog > aside nav) { display: flex; gap: 4px; overflow-x: auto; padding: 0 8px 8px !important; }
    :global(.settings-dialog > aside nav) { scrollbar-width: none; }
    :global(.settings-dialog > aside nav::-webkit-scrollbar) { display: none; }
    :global(.settings-dialog > aside nav button) { width: auto !important; min-width: max-content; padding: 8px 10px !important; }
    :global(.settings-dialog > aside nav button span) { font-size: 9px !important; }
    :global(.settings-dialog > aside > div:last-child) { display: none; }
    :global(.settings-dialog main > header) { height: 47px !important; padding: 0 16px !important; }
    :global(.settings-dialog main > div) { padding: 0 16px 22px !important; }
    :global(.settings-page-mode .settings-dialog) { height: auto !important; min-height: 0; }
  }
  :global(.scrollbar-custom::-webkit-scrollbar) { width: 4px; }
  :global(.scrollbar-custom::-webkit-scrollbar-track) { background: transparent; }
  :global(.scrollbar-custom::-webkit-scrollbar-thumb) { background: #252626; border-radius: 0px; }
  :global(.scrollbar-custom::-webkit-scrollbar-thumb:hover) { background: #474848; }
</style>
