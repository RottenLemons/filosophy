<script lang="ts">
  import { afterUpdate, createEventDispatcher } from 'svelte';
  import { fade } from 'svelte/transition';
  import { X, Trash2, AlertCircle, Search as SearchIcon, Send } from 'lucide-svelte';
  import { Search } from '$lib/wailsjs/go/main/App';

  const dispatch = createEventDispatcher();

  export let contextFile: any = null;
  export let fileContent: string = '';
  export let embedded = false;

  interface Message {
    role: 'user' | 'assistant' | 'tool';
    content: string;
    tool_call_id?: string;
    // display-only fields (not sent to API)
    _searchQuery?: string;
    _searchResults?: string[];
    _pendingConfirm?: boolean;
    _denied?: boolean;
  }

  interface LLMConfig {
    provider: 'none' | 'ollama' | 'lmstudio' | 'openai-compatible';
    baseUrl: string;
    model: string;
    apiKey: string;
  }

  const LLM_STORAGE_KEY = 'filosophy_llm_config';

  let messages: Message[] = [];
  let input = '';
  let streaming = false;
  let error = '';
  let messagesEl: HTMLElement;
  let textareaEl: HTMLTextAreaElement;
  let abortController: AbortController | null = null;

  // Privacy confirm gate
  let alwaysAllow = false;
  interface ConfirmState {
    query: string;
    results: string[];
    resolve: (allowed: boolean) => void;
  }
  let pendingConfirm: ConfirmState | null = null;

  function confirmAllow() {
    if (!pendingConfirm) return;
    pendingConfirm.resolve(true);
    pendingConfirm = null;
  }

  function confirmDeny() {
    if (!pendingConfirm) return;
    pendingConfirm.resolve(false);
    pendingConfirm = null;
  }

  function confirmAlwaysAllow() {
    alwaysAllow = true;
    confirmAllow();
  }

  function requestConfirm(query: string, results: string[]): Promise<boolean> {
    if (alwaysAllow) return Promise.resolve(true);
    return new Promise<boolean>(resolve => {
      pendingConfirm = { query, results, resolve };
    });
  }

  function getLLMConfig(): LLMConfig {
    try {
      const stored = localStorage.getItem(LLM_STORAGE_KEY);
      if (stored) return JSON.parse(stored);
    } catch {}
    return { provider: 'none', baseUrl: '', model: '', apiKey: '' };
  }

  afterUpdate(() => {
    if (messagesEl) messagesEl.scrollTop = messagesEl.scrollHeight;
  });

  function resizeTextarea() {
    if (!textareaEl) return;
    textareaEl.style.height = 'auto';
    textareaEl.style.height = Math.min(textareaEl.scrollHeight, 160) + 'px';
  }

  const SEARCH_TOOL = {
    type: 'function',
    function: {
      name: 'search_files',
      description: 'Search the local file index for files relevant to a query. Returns up to ten matching file paths, not file contents.',
      parameters: {
        type: 'object',
        properties: { query: { type: 'string' } },
        required: ['query'],
      },
    },
  };

  async function callLLM(apiMessages: any[], stream: boolean, signal: AbortSignal): Promise<Response> {
    const cfg = getLLMConfig();
    let llmEndpoint: string;
    if (cfg.provider === 'ollama') {
      llmEndpoint = `${cfg.baseUrl.replace(/\/$/, '')}/v1/chat/completions`;
    } else {
      llmEndpoint = `${cfg.baseUrl.replace(/\/$/, '')}/chat/completions`;
    }
    const endpoint = `/llmproxy/?url=${encodeURIComponent(llmEndpoint)}`;
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    if (cfg.apiKey) headers['Authorization'] = `Bearer ${cfg.apiKey}`;
    return fetch(endpoint, {
      method: 'POST',
      headers,
      signal,
      body: JSON.stringify({
        model: cfg.model || undefined,
        messages: apiMessages,
        tools: [SEARCH_TOOL],
        tool_choice: 'auto',
        stream,
      }),
    });
  }

  async function responseError(res: Response): Promise<Error> {
    let body = '';
    try {
      body = (await res.text()).trim().slice(0, 500);
    } catch {}
    const message = body ? `HTTP ${res.status} ${res.statusText}: ${body}` : `HTTP ${res.status} ${res.statusText}`;
    return new Error(message);
  }

  async function streamResponse(res: Response, assistantMsg: Message): Promise<{ toolCalls: any[] }> {
    if (!res.body) throw new Error('The assistant response did not include a readable stream.');
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    const toolCallAccum: Record<number, { id: string; name: string; args: string }> = {};

    const consumeLine = (line: string) => {
      if (!line.startsWith('data:')) return;
      const data = line.slice(5).trimStart();
      if (!data || data === '[DONE]') return;
      try {
        const json = JSON.parse(data);
        const delta = json.choices?.[0]?.delta;
        if (!delta) return;
        if (delta.content) {
          assistantMsg.content += delta.content;
          messages = [...messages.slice(0, -1), { ...assistantMsg }];
        }
        if (delta.tool_calls) {
          for (const tc of delta.tool_calls) {
            const idx = tc.index ?? 0;
            if (!toolCallAccum[idx]) toolCallAccum[idx] = { id: '', name: '', args: '' };
            if (tc.id) toolCallAccum[idx].id = tc.id;
            if (tc.function?.name) toolCallAccum[idx].name += tc.function.name;
            if (tc.function?.arguments) toolCallAccum[idx].args += tc.function.arguments;
          }
        }
      } catch {
        // Ignore non-JSON SSE data such as provider comments.
      }
    };

    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split(/\r?\n/);
        buffer = lines.pop() ?? '';
        for (const line of lines) consumeLine(line);
      }
      buffer += decoder.decode();
      if (buffer) consumeLine(buffer);
    } finally {
      reader.releaseLock();
    }
    return { toolCalls: Object.values(toolCallAccum) };
  }

  async function send() {
    const text = input.trim();
    if (!text || streaming) return;

    const cfg = getLLMConfig();
    if (cfg.provider === 'none' || !cfg.baseUrl) {
      error = 'Configuration Missing.';
      return;
    }

    error = '';
    messages = [...messages, { role: 'user', content: text }];
    input = '';
    if (textareaEl) textareaEl.style.height = 'auto';
    streaming = true;
    abortController = new AbortController();

    let systemContent = `You are a professional file search assistant. Summarize results concisely.`;
    if (contextFile) {
      systemContent += `\n\nContext: ${contextFile.Path}.`;
      if (fileContent) { systemContent += `\nContent: ${fileContent.slice(0, 4000)}`; }
    }

    const apiMessages: any[] = [
      { role: 'system', content: systemContent },
      ...messages.filter(m => m.role !== 'tool' || m.tool_call_id).map(m => {
        if (m.role === 'tool') return { role: 'tool', content: m.content, tool_call_id: m.tool_call_id };
        return { role: m.role, content: m.content };
      }),
    ];

    try {
      while (true) {
        const assistantMsg: Message = { role: 'assistant', content: '' };
        messages = [...messages, assistantMsg];
        const res = await callLLM(apiMessages, true, abortController.signal);
        if (!res.ok) throw await responseError(res);
        const { toolCalls } = await streamResponse(res, assistantMsg);
        apiMessages.push({ role: 'assistant', content: assistantMsg.content || null, tool_calls: toolCalls.length ? toolCalls.map(tc => ({ id: tc.id, type: 'function', function: { name: tc.name, arguments: tc.args } })) : undefined });
        if (!toolCalls.length) break;
        if (!assistantMsg.content) messages = messages.slice(0, -1);

        for (const tc of toolCalls) {
          let query = '';
          try { query = JSON.parse(tc.args).query ?? ''; } catch {}
          const searchMsg: Message = { role: 'tool', content: '', tool_call_id: tc.id, _searchQuery: query, _searchResults: [] };
          messages = [...messages, searchMsg];
          const results = await Search(query);
          const resultPaths = results?.slice(0, 10).map(r => r.Path) || [];
          searchMsg._searchResults = resultPaths;
          searchMsg._pendingConfirm = resultPaths.length > 0;
          messages = [...messages.slice(0, -1), { ...searchMsg }];

          let finalResult = 'No results.';
          if (resultPaths.length > 0) {
            const allowed = await requestConfirm(query, resultPaths);
            finalResult = allowed ? resultPaths.join('\n') : 'Denied.';
            searchMsg._pendingConfirm = false;
            searchMsg._denied = !allowed;
            messages = [...messages.slice(0, -1), { ...searchMsg }];
          }
          apiMessages.push({ role: 'tool', tool_call_id: tc.id, content: finalResult });
        }
      }
    } catch (e: any) {
      if (e?.name !== 'AbortError') {
        messages = messages.slice(0, -1);
        error = e?.message || 'Error occurred.';
      }
    } finally {
      streaming = false;
      abortController = null;
    }
  }

  function cancel() {
    abortController?.abort();
    confirmDeny();
  }
  function onKeyDown(e: KeyboardEvent) { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); } }
  function clearChat() { if (streaming) cancel(); messages = []; error = ''; }

  function formatContent(text: string): string {
    const esc = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    let out = esc.replace(/```[\w]*\n?([\s\S]*?)```/g, (_, code) =>
      `<pre class="my-4 p-4 bg-[#000000] border border-[#474848]/20 rounded-sm font-mono text-xs text-[#acabab] overflow-x-auto whitespace-pre">${code.trimEnd()}</pre>`
    );
    out = out.replace(/`([^`]+)`/g, (_, c) =>
      `<code class="chat-code">${c}</code>`
    );
    out = out.replace(/\*\*(.+?)\*\*/g, '<strong class="chat-strong">$1</strong>');
    out = out.replace(/\n/g, '<br>');
    return out;
  }

  $: cfg = getLLMConfig();
  $: providerLabel = cfg.provider !== 'none' && cfg.baseUrl ? cfg.model : null;
</script>

<aside class="chat-panel {embedded ? 'embedded-chat' : 'absolute inset-0 z-50 lg:relative lg:inset-auto lg:w-[40%] lg:min-w-[450px]'} bg-[#0e0e0e] border-l border-[#1a1a1a] flex flex-col shadow-2xl overflow-hidden">
  <!-- Header -->
  <header class="h-14 shrink-0 flex items-center justify-between px-6 bg-[#0e0e0e] border-b border-[#1a1a1a]">
    <div class="flex items-center gap-3">
      <span class="text-[10px] font-bold uppercase tracking-[0.2em] text-[#acabab]">Session</span>
      {#if providerLabel}
        <span class="text-[9px] font-medium text-[#474848] uppercase tracking-widest">{providerLabel}</span>
      {/if}
    </div>
    <div class="flex items-center gap-1">
      {#if messages.length > 0}
        <button on:click={clearChat} class="p-2 text-[#474848] hover:text-[#acabab] transition-colors"><Trash2 class="w-4 h-4" /></button>
      {/if}
      {#if !embedded}
        <button on:click={() => dispatch('close')} class="p-2 text-[#474848] hover:text-[#acabab] transition-colors" aria-label="Close assistant"><X class="w-5 h-5" /></button>
      {/if}
    </div>
  </header>

  <!-- Message Feed -->
  <div bind:this={messagesEl} class="flex-1 overflow-y-auto px-6 py-6 space-y-6 scrollbar-custom pb-28">
    {#if !cfg.provider || cfg.provider === 'none'}
       <div class="h-full flex flex-col items-center justify-center text-center px-12 gap-4">
          <p class="text-sm font-serif text-[#e7e5e5]">Inference Engine Offline</p>
          <p class="text-[11px] text-[#474848] leading-relaxed">Configure a local provider in settings to enable assistant responses.</p>
       </div>
    {:else if messages.length === 0}
       <div class="h-full flex flex-col items-center justify-center text-center px-12 gap-2">
          <p class="text-xl font-serif text-[#e7e5e5] tracking-tight">Filosophy Ready.</p>
          <p class="text-[10px] text-[#474848] uppercase tracking-[0.2em]">Idle // Awaiting Input</p>
       </div>
    {:else}
      {#each messages as msg, i (i)}
        {#if msg.role === 'user'}
          <!-- Task 1: User Message Block -->
          <div class="flex justify-end" in:fade={{ duration: 150 }}>
            <div class="max-w-[85%] px-4 py-3 bg-[#252626] rounded-md text-sm font-sans text-[#e7e5e5] leading-relaxed shadow-sm">
              {msg.content}
            </div>
          </div>
        {:else if msg.role === 'tool'}
          <div class="p-5 bg-[#131313] border border-[#2a2a2a]/10 rounded-md" in:fade={{ duration: 150 }}>
             <div class="flex items-center gap-2 mb-3">
                <SearchIcon class="w-3.5 h-3.5 text-[#474848]" />
                <span class="text-[10px] font-bold text-[#474848] uppercase tracking-widest">Index Search // {msg._searchQuery}</span>
             </div>
             {#if msg._searchResults?.length}
               <div class="space-y-1">
                 {#each msg._searchResults as path}
                   <div class="text-[10px] font-mono text-[#acabab]/60 truncate">» {path.split(/[\\/]/).pop()}</div>
                 {/each}
               </div>
               {#if msg._pendingConfirm}
                  <div class="mt-4 pt-4 border-t border-[#474848]/10 flex flex-col gap-3">
                    <p class="text-[9px] font-bold text-[#bfc8ca] uppercase tracking-widest">Approve Context Exposure</p>
                    <div class="flex gap-2">
                      <button on:click={confirmAllow} class="px-4 py-1.5 bg-[#252626] text-[#e7e5e5] text-[9px] font-bold uppercase tracking-widest rounded-sm border border-[#474848]/20">Allow</button>
                      <button on:click={confirmDeny} class="px-4 py-1.5 border border-[#474848]/20 text-[#acabab] text-[9px] font-bold uppercase tracking-widest rounded-sm">Deny</button>
                    </div>
                  </div>
               {/if}
             {/if}
          </div>
        {:else}
          <!-- Task 1: AI Response Block -->
          <div class="flex justify-start" in:fade={{ duration: 150 }}>
            <div class="max-w-full w-full px-5 py-5 bg-[#131313] rounded-md shadow-sm">
              {#if msg.content}
                <div class="text-base font-serif text-[#e7e5e5] leading-relaxed">
                  {@html formatContent(msg.content)}
                </div>
              {:else if streaming && i === messages.length - 1}
                <div class="flex items-center gap-1.5 py-1">
                  <div class="w-1.5 h-1.5 bg-[#bfc8ca] rounded-full animate-pulse"></div>
                  <span class="text-[9px] font-bold uppercase tracking-widest text-[#474848]">Thinking</span>
                </div>
              {/if}
            </div>
          </div>
        {/if}
      {/each}
    {/if}
    {#if error}
      <div class="p-3 bg-red-950/10 border border-red-900/30 rounded-md flex items-center gap-3">
        <AlertCircle class="w-4 h-4 text-red-500 shrink-0" />
        <p class="text-[10px] font-mono text-red-400 uppercase">{error}</p>
      </div>
    {/if}
  </div>

  <!-- Task 2: The Input Bar -->
  {#if cfg.provider && cfg.provider !== 'none'}
    <div class="absolute bottom-0 left-0 right-0 p-6 bg-gradient-to-t from-[#0e0e0e] via-[#0e0e0e] to-transparent">
      <div class="bg-[#131313] rounded-md px-4 py-3 flex items-end gap-3 shadow-xl border border-[#2a2a2a]/10">
        <textarea
          bind:this={textareaEl}
          bind:value={input}
          on:keydown={onKeyDown}
          on:input={resizeTextarea}
          placeholder="Message engine…"
          rows="1"
          disabled={streaming}
          class="flex-1 bg-transparent border-none outline-none resize-none text-sm font-sans text-white placeholder:text-[#acabab]/50 leading-relaxed disabled:opacity-50 max-h-40 scrollbar-custom"
        ></textarea>
        
        <div class="flex pb-0.5">
          {#if streaming}
            <button on:click={cancel} class="group p-1" aria-label="Cancel response" title="Cancel response">
              <div class="w-4 h-4 bg-[#474848] group-hover:bg-red-500 transition-colors"></div>
            </button>
          {:else}
            <button on:click={send} disabled={!input.trim()} class="p-1 transition-colors text-[#474848] hover:text-[#bfc8ca] disabled:opacity-10 cursor-pointer" aria-label="Send message" title="Send message">
              <Send class="w-5 h-5" />
            </button>
          {/if}
        </div>
      </div>
    </div>
  {/if}
</aside>

<style>
  :global(.chat-panel) { background: var(--f-surface) !important; border-color: var(--f-border) !important; color: var(--f-text) !important; box-shadow: -8px 0 28px rgb(21 42 32 / 7%) !important; }
  :global(.chat-panel header) { background: var(--f-surface) !important; border-color: var(--f-border) !important; }
  :global(.chat-panel [class*="bg-[#0e0e0e]"], .chat-panel [class*="bg-[#131313]"], .chat-panel [class*="bg-[#252626]"]) { background: var(--f-surface-2) !important; }
  :global(.chat-panel [class*="text-[#e7e5e5]"]) { color: var(--f-text) !important; }
  :global(.chat-panel [class*="text-[#acabab]"], .chat-panel [class*="text-[#474848]"]) { color: var(--f-text-2) !important; }
  :global(.chat-panel [class*="text-[#bfc8ca]"]) { color: var(--f-accent) !important; }
  :global(.chat-panel [class*="border-[#1a1a1a]"], .chat-panel [class*="border-[#474848]"]) { border-color: var(--f-border) !important; }
  :global(.chat-panel textarea) { color: var(--f-text) !important; }
  :global(.chat-panel textarea::placeholder) { color: var(--f-text-3) !important; }
  :global(.chat-panel .font-serif) { color: var(--f-text) !important; font-family: Inter, "Segoe UI", sans-serif !important; }
  :global(.chat-panel .h-full) { height: 100%; min-height: 100%; }
  :global(.chat-code) { padding: 2px 5px; border: 1px solid var(--f-border); border-radius: 3px; background: var(--f-surface-2); color: var(--f-accent); font: 11px ui-monospace, monospace; }
  :global(.chat-strong) { color: var(--f-text); }
  :global(.embedded-chat) { position: relative !important; inset: auto !important; width: 100% !important; min-width: 0 !important; min-height: min(700px, calc(100dvh - 220px)); border: 1px solid var(--f-border) !important; border-radius: 6px; box-shadow: none !important; }
  :global(.embedded-chat > div:last-child) { background: linear-gradient(to top, var(--f-surface) 70%, transparent) !important; }
  @media (max-width: 760px) { :global(.embedded-chat) { min-height: calc(100dvh - 180px); } }
  :global(.scrollbar-custom::-webkit-scrollbar) { width: 3px; }
  :global(.scrollbar-custom::-webkit-scrollbar-track) { background: transparent; }
  :global(.scrollbar-custom::-webkit-scrollbar-thumb) { background: #1a1a1a; }
</style>
