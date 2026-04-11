<script lang="ts">
  import { afterUpdate, createEventDispatcher } from 'svelte';
  import { fade, slide } from 'svelte/transition';
  import { X, Trash2, AlertCircle, Send, FileText, Settings, Search as SearchIcon, Shield, Check } from 'lucide-svelte';
  import { Search } from '$lib/wailsjs/go/main/App';

  const dispatch = createEventDispatcher();

  export let contextFile: any = null;
  export let fileContent: string = '';

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

  // Pause the agentic loop until user approves/denies sharing results with the LLM
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
      description: `Search the user's locally indexed files using semantic + keyword search.
Use this whenever the user asks to find, look for, or search files.

QUERY CONSTRUCTION RULES — follow these carefully:
- Use SHORT, SPECIFIC queries: 1–4 keywords max. Never pass the full user sentence.
- For author searches: search the author's last name or full name (e.g. "Camus", "Albert Camus").
- For topic searches: use the core noun/concept (e.g. "existentialism", "climate change").
- For known titles: search the exact title (e.g. "The Stranger", "The Plague").
- You can call this tool MULTIPLE TIMES with different queries to improve coverage.
  Example for "books by Albert Camus": call once with "Albert Camus", then again with "The Stranger" and "The Plague" if needed.
- The index contains file paths and content — filename matches are strong signals.`,
      parameters: {
        type: 'object',
        properties: {
          query: {
            type: 'string',
            description: 'Short, specific search query (1–4 keywords). NOT the full user message.',
          },
        },
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
    const cfg2 = getLLMConfig();
    return fetch(endpoint, {
      method: 'POST',
      headers,
      signal,
      body: JSON.stringify({
        model: cfg2.model || undefined,
        messages: apiMessages,
        tools: [SEARCH_TOOL],
        tool_choice: 'auto',
        stream,
      }),
    });
  }

  async function streamResponse(res: Response, assistantMsg: Message): Promise<{ toolCalls: any[] }> {
    const reader = res.body!.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    // Accumulate tool call deltas keyed by index
    const toolCallAccum: Record<number, { id: string; name: string; args: string }> = {};

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split('\n');
      buffer = lines.pop() ?? '';
      for (const line of lines) {
        if (!line.startsWith('data: ')) continue;
        const data = line.slice(6).trim();
        if (data === '[DONE]') continue;
        try {
          const json = JSON.parse(data);
          const delta = json.choices?.[0]?.delta;
          if (!delta) continue;
          // Content token
          if (delta.content) {
            assistantMsg.content += delta.content;
            messages = [...messages.slice(0, -1), { ...assistantMsg }];
          }
          // Tool call deltas
          if (delta.tool_calls) {
            for (const tc of delta.tool_calls) {
              const idx = tc.index ?? 0;
              if (!toolCallAccum[idx]) toolCallAccum[idx] = { id: '', name: '', args: '' };
              if (tc.id) toolCallAccum[idx].id = tc.id;
              if (tc.function?.name) toolCallAccum[idx].name += tc.function.name;
              if (tc.function?.arguments) toolCallAccum[idx].args += tc.function.arguments;
            }
          }
        } catch {}
      }
    }
    return { toolCalls: Object.values(toolCallAccum) };
  }

  async function send() {
    const text = input.trim();
    if (!text || streaming) return;

    const cfg = getLLMConfig();
    if (cfg.provider === 'none' || !cfg.baseUrl) {
      error = 'No LLM configured. Open Settings → Connections.';
      return;
    }

    error = '';
    messages = [...messages, { role: 'user', content: text }];
    input = '';
    if (textareaEl) { textareaEl.style.height = 'auto'; }
    streaming = true;
    abortController = new AbortController();

    // Build system prompt
    let systemContent = `You are a helpful file search assistant embedded in Filosophy, a local AI-powered file search app running entirely on the user's machine.

You have access to a search_files tool that searches the user's locally indexed files.

HOW TO USE search_files:
- Always decompose the user's request into focused, short queries before searching.
- For "books by Albert Camus": search "Albert Camus" first. If few results, also try known titles like "The Stranger", "The Plague", "The Fall".
- For "my tax documents from 2023": search "tax 2023", then "invoice 2023" if needed.
- Never pass the raw user message as the query — extract the key terms.
- Call the tool multiple times if one query isn't enough.
- After getting results, tell the user what you found with file names (not full paths). If nothing was found, say so clearly.
- Be concise. Don't make up files that weren't in the search results.`;
    if (contextFile) {
      systemContent += `\n\nContext: the user has selected the file "${contextFile.Path}".`;
      if (fileContent) {
        const preview = fileContent.slice(0, 6000);
        systemContent += `\n\nFile content:\n---\n${preview}${fileContent.length > 6000 ? '\n[content truncated]' : '\n---'}`;
      }
    }

    // apiMessages mirrors what we send to the LLM (includes tool messages not shown in UI)
    const apiMessages: any[] = [
      { role: 'system', content: systemContent },
      ...messages.filter(m => m.role !== 'tool' || m.tool_call_id).map(m => {
        if (m.role === 'tool') return { role: 'tool', content: m.content, tool_call_id: m.tool_call_id };
        return { role: m.role, content: m.content };
      }),
    ];

    try {
      // Agentic loop: keep going until LLM stops calling tools
      while (true) {
        const assistantMsg: Message = { role: 'assistant', content: '' };
        messages = [...messages, assistantMsg];

        const res = await callLLM(apiMessages, true, abortController.signal);
        if (!res.ok) {
          const body = await res.text().catch(() => '');
          throw new Error(`HTTP ${res.status} — ${body || res.statusText}`);
        }

        const { toolCalls } = await streamResponse(res, assistantMsg);

        // Append the assistant turn to apiMessages
        const assistantApiMsg: any = { role: 'assistant', content: assistantMsg.content || null };
        if (toolCalls.length > 0) {
          assistantApiMsg.tool_calls = toolCalls.map(tc => ({
            id: tc.id,
            type: 'function',
            function: { name: tc.name, arguments: tc.args },
          }));
        }
        apiMessages.push(assistantApiMsg);

        if (toolCalls.length === 0) break; // LLM is done with tools

        // Execute each tool call
        for (const tc of toolCalls) {
          if (tc.name !== 'search_files') continue;
          let query = '';
          try { query = JSON.parse(tc.args).query ?? ''; } catch {}

          // Show a search indicator in the UI
          const searchMsg: Message = {
            role: 'tool',
            content: '',
            tool_call_id: tc.id,
            _searchQuery: query,
            _searchResults: [],
          };
          messages = [...messages, searchMsg];

          let resultText = 'No results found.';
          let resultPaths: string[] = [];
          try {
            const results = await Search(query);
            if (results && results.length > 0) {
              resultPaths = results.slice(0, 10).map(r => r.Path);
              resultText = resultPaths.join('\n');
            }
          } catch (e) {
            resultText = 'Search failed: ' + String(e);
          }

          // Privacy gate — ask user before sending file paths to the LLM
          if (resultPaths.length > 0) {
            searchMsg._searchResults = resultPaths;
            searchMsg._pendingConfirm = true;
            messages = [...messages.slice(0, -1), { ...searchMsg }];

            const allowed = await requestConfirm(query, resultPaths);

            searchMsg._pendingConfirm = false;
            if (!allowed) {
              searchMsg._denied = true;
              messages = [...messages.slice(0, -1), { ...searchMsg }];
              resultText = 'User denied sharing these results with the AI.';
            } else {
              messages = [...messages.slice(0, -1), { ...searchMsg }];
            }
          } else {
            messages = [...messages.slice(0, -1), { ...searchMsg }];
          }

          apiMessages.push({ role: 'tool', tool_call_id: tc.id, content: resultText });
        }
        // Loop — LLM will now respond with the search results in context
      }
    } catch (e: any) {
      if (e?.name === 'AbortError') {
        // user cancelled — keep whatever streamed so far
      } else {
        messages = messages.slice(0, -1);
        error = e?.message ?? 'Request failed';
      }
    } finally {
      streaming = false;
      abortController = null;
    }
  }

  function cancel() {
    abortController?.abort();
  }

  function onKeyDown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      send();
    }
  }

  function clearChat() {
    messages = [];
    error = '';
  }

  // Format message content: handle code fences, inline code, bold, line breaks
  function formatContent(text: string): string {
    // Escape HTML first
    const esc = text
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;');

    // Code blocks
    let out = esc.replace(/```[\w]*\n?([\s\S]*?)```/g, (_, code) =>
      `<pre class="my-2 p-3 bg-gray-100 dark:bg-[#0a0a0a] border border-gray-200 dark:border-[#2a2a2a] rounded-lg overflow-x-auto text-[11px] font-mono leading-relaxed text-slate-800 dark:text-gray-200 whitespace-pre">${code.trimEnd()}</pre>`
    );
    // Inline code
    out = out.replace(/`([^`]+)`/g, (_, c) =>
      `<code class="px-1 py-0.5 bg-gray-100 dark:bg-[#1a1a1a] rounded text-[11px] font-mono text-blue-600 dark:text-blue-400">${c}</code>`
    );
    // Bold
    out = out.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
    // Line breaks
    out = out.replace(/\n/g, '<br>');
    return out;
  }

  $: cfg = getLLMConfig();
  $: providerLabel = cfg.provider !== 'none' && cfg.baseUrl
    ? ({ ollama: 'Ollama', lmstudio: 'LM Studio', 'openai-compatible': 'API' }[cfg.provider] ?? cfg.provider) + (cfg.model ? ' · ' + cfg.model : '')
    : null;
  $: hasConfig = cfg.provider !== 'none' && !!cfg.baseUrl;
</script>

<aside
  class="absolute inset-0 z-50 lg:relative lg:inset-auto lg:w-[40%] lg:min-w-[450px] bg-[#FAF9F6] dark:bg-[#111] border-l border-gray-200 dark:border-l-[#2a2a2a] flex flex-col shadow-2xl overflow-hidden"
>
  <!-- Header -->
  <header class="h-16 shrink-0 flex items-center justify-between px-8 border-b border-gray-100 dark:border-[#1a1a1a]">
    <div class="flex items-center gap-3">
      <span class="text-[10px] font-black uppercase tracking-[0.2em] text-slate-900 dark:text-gray-100">Chat</span>
      {#if providerLabel}
        <span class="px-2 py-0.5 text-[9px] font-bold uppercase tracking-wider bg-blue-50 dark:bg-blue-950/30 text-blue-600 dark:text-blue-400 rounded border border-blue-100 dark:border-blue-900/30" transition:fade>
          {providerLabel}
        </span>
      {/if}
    </div>
    <div class="flex items-center gap-1">
      {#if messages.length > 0}
        <button
          on:click={clearChat}
          class="p-2 text-gray-400 hover:text-slate-900 dark:hover:text-gray-100 transition-colors"
          aria-label="Clear conversation"
        >
          <Trash2 class="w-4 h-4" />
        </button>
      {/if}
      <button
        on:click={() => dispatch('close')}
        class="p-2 text-gray-400 hover:text-slate-900 dark:hover:text-gray-100 transition-colors"
        aria-label="Close chat"
      >
        <X class="w-5 h-5" />
      </button>
    </div>
  </header>

  <!-- Context file badge -->
  {#if contextFile}
    <div class="shrink-0 px-6 py-2.5 border-b border-gray-100 dark:border-[#1a1a1a] bg-blue-50/50 dark:bg-blue-950/10 flex items-center gap-2" transition:slide={{ duration: 200 }}>
      <FileText class="w-3.5 h-3.5 text-blue-500 shrink-0" />
      <span class="text-[10px] font-semibold text-blue-600 dark:text-blue-400 uppercase tracking-widest truncate">
        Context: {contextFile.Path.split(/[\\/]/).pop()}
      </span>
    </div>
  {/if}

  <!-- Messages -->
  <div
    bind:this={messagesEl}
    class="flex-1 overflow-y-auto px-6 py-6 space-y-6 scrollbar-custom"
  >
    {#if !hasConfig}
      <!-- No config state -->
      <div class="h-full flex flex-col items-center justify-center text-center gap-4 px-8">
        <div class="w-12 h-12 border border-gray-200 dark:border-[#2a2a2a] flex items-center justify-center text-gray-300 dark:text-gray-600">
          <Settings class="w-6 h-6" />
        </div>
        <div class="space-y-1.5">
          <p class="text-sm font-serif font-bold text-slate-800 dark:text-gray-200">No LLM Connected</p>
          <p class="text-[11px] text-gray-400 dark:text-gray-500 leading-relaxed max-w-xs">
            Go to <strong>Settings → Connections</strong> and add a local LLM like Ollama or LM Studio to start chatting.
          </p>
        </div>
      </div>
    {:else if messages.length === 0}
      <!-- Empty state -->
      <div class="h-full flex flex-col items-center justify-center text-center gap-3 px-8">
        <p class="text-2xl font-serif font-bold text-slate-900 dark:text-gray-100">How can I help?</p>
        <p class="text-[11px] text-gray-400 dark:text-gray-500 leading-relaxed max-w-xs">
          Ask anything — or select a file on the left to include it as context.
        </p>
      </div>
    {:else}
      {#each messages as msg, i (i)}
        {#if msg.role === 'user'}
          <!-- User bubble -->
          <div class="flex justify-end" in:fade={{ duration: 150 }}>
            <div class="max-w-[80%] px-4 py-3 bg-slate-900 dark:bg-white text-white dark:text-black text-[13px] leading-relaxed font-sans whitespace-pre-wrap">
              {msg.content}
            </div>
          </div>
        {:else if msg.role === 'tool'}
          <!-- Search tool call indicator -->
          <div class="flex gap-2 items-start" in:fade={{ duration: 150 }}>
            <div class="w-6 h-6 shrink-0 mt-0.5 flex items-center justify-center text-gray-400 dark:text-gray-500">
              <SearchIcon class="w-3.5 h-3.5" />
            </div>
            <div class="flex-1 min-w-0">
              <p class="text-[10px] font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-widest mb-1.5">
                Searched: <span class="text-blue-500 dark:text-blue-400 normal-case font-mono">{msg._searchQuery}</span>
              </p>

              {#if msg._searchResults && msg._searchResults.length > 0}
                <div class="border border-gray-100 dark:border-[#2a2a2a] divide-y divide-gray-100 dark:divide-[#2a2a2a] mb-2">
                  {#each msg._searchResults as path}
                    <p class="px-3 py-1.5 text-[10px] text-gray-500 dark:text-gray-400 font-mono truncate">{path.split(/[\\/]/).pop()}<span class="text-gray-300 dark:text-gray-600 ml-1 text-[9px]">{path.split(/[\\/]/).slice(0,-1).join('/')}</span></p>
                  {/each}
                </div>

                {#if msg._pendingConfirm}
                  <!-- Confirm gate -->
                  <div class="border border-amber-200 dark:border-amber-900/40 bg-amber-50/50 dark:bg-amber-950/10 p-3" transition:slide={{ duration: 150 }}>
                    <div class="flex items-center gap-1.5 mb-2">
                      <Shield class="w-3 h-3 text-amber-500 shrink-0" />
                      <p class="text-[10px] font-bold text-amber-700 dark:text-amber-400 uppercase tracking-widest">Share these files with the AI?</p>
                    </div>
                    <p class="text-[10px] text-amber-600 dark:text-amber-500 mb-3 leading-relaxed">
                      The file paths above will be sent to your LLM to answer your question.
                    </p>
                    <div class="flex gap-2">
                      <button
                        on:click={confirmDeny}
                        class="flex-1 py-1.5 border border-gray-200 dark:border-[#2a2a2a] text-[9px] font-bold uppercase tracking-widest text-gray-500 hover:bg-gray-50 dark:hover:bg-[#1a1a1a] transition-colors"
                      >Deny</button>
                      <button
                        on:click={confirmAllow}
                        class="flex-1 py-1.5 bg-slate-900 dark:bg-white text-white dark:text-black text-[9px] font-bold uppercase tracking-widest hover:bg-slate-700 dark:hover:bg-gray-200 transition-colors flex items-center justify-center gap-1.5"
                      ><Check class="w-3 h-3" /> Allow</button>
                    </div>
                    <button
                      on:click={confirmAlwaysAllow}
                      class="mt-2 w-full text-[9px] text-gray-400 dark:text-gray-600 hover:text-gray-600 dark:hover:text-gray-400 transition-colors"
                    >Always allow (don't ask again)</button>
                  </div>
                {:else if msg._denied}
                  <p class="text-[10px] text-red-400 dark:text-red-500 italic">Sharing denied — results not sent to AI</p>
                {:else}
                  <p class="text-[10px] text-green-600 dark:text-green-500 flex items-center gap-1"><Check class="w-3 h-3" /> Shared with AI</p>
                {/if}
              {:else}
                <p class="text-[10px] text-gray-400 italic">No results found</p>
              {/if}
            </div>
          </div>
        {:else}
          <!-- Assistant message -->
          <div class="flex gap-3 items-start" in:fade={{ duration: 150 }}>
            <div class="w-6 h-6 shrink-0 mt-0.5 bg-blue-600 flex items-center justify-center text-white text-[9px] font-black tracking-widest rounded-sm select-none">
              AI
            </div>
            <div class="flex-1 min-w-0">
              {#if msg.content}
                <div class="text-[13px] leading-relaxed text-slate-800 dark:text-gray-100 font-sans">
                  {@html formatContent(msg.content)}
                </div>
              {:else if streaming && i === messages.length - 1}
                <!-- Typing indicator -->
                <div class="flex items-center gap-1.5 py-1">
                  <span class="w-1.5 h-1.5 rounded-full bg-gray-400 dark:bg-gray-500 animate-bounce" style="animation-delay: 0ms"></span>
                  <span class="w-1.5 h-1.5 rounded-full bg-gray-400 dark:bg-gray-500 animate-bounce" style="animation-delay: 150ms"></span>
                  <span class="w-1.5 h-1.5 rounded-full bg-gray-400 dark:bg-gray-500 animate-bounce" style="animation-delay: 300ms"></span>
                </div>
              {/if}
            </div>
          </div>
        {/if}
      {/each}
    {/if}

    <!-- Error -->
    {#if error}
      <div class="flex items-start gap-2 p-3 bg-red-50/50 dark:bg-red-950/10 border border-red-100 dark:border-red-900/30 rounded-lg" transition:slide>
        <AlertCircle class="w-4 h-4 text-red-500 shrink-0 mt-0.5" />
        <p class="text-[11px] text-red-600 dark:text-red-400 leading-relaxed font-mono">{error}</p>
      </div>
    {/if}
  </div>

  <!-- Input area -->
  {#if hasConfig}
    <div class="shrink-0 border-t border-gray-100 dark:border-[#1a1a1a] px-6 py-4">
      <div class="flex items-end gap-3 border border-gray-200 dark:border-[#2a2a2a] bg-white dark:bg-[#0a0a0a] px-4 py-3 focus-within:border-blue-400 dark:focus-within:border-blue-600 transition-colors">
        <textarea
          bind:this={textareaEl}
          bind:value={input}
          on:keydown={onKeyDown}
          on:input={resizeTextarea}
          placeholder="Message…"
          rows="1"
          disabled={streaming}
          class="flex-1 bg-transparent border-none outline-none resize-none text-[13px] text-slate-800 dark:text-gray-100 placeholder:text-gray-300 dark:placeholder:text-gray-600 leading-relaxed disabled:opacity-50 max-h-40 scrollbar-custom"
        ></textarea>
        {#if streaming}
          <button
            on:click={cancel}
            class="shrink-0 w-8 h-8 flex items-center justify-center bg-gray-200 dark:bg-[#2a2a2a] text-gray-600 dark:text-gray-400 hover:bg-red-100 dark:hover:bg-red-950/30 hover:text-red-500 transition-colors"
            aria-label="Stop generation"
          >
            <span class="w-3 h-3 bg-current block"></span>
          </button>
        {:else}
          <button
            on:click={send}
            disabled={!input.trim()}
            class="shrink-0 w-8 h-8 flex items-center justify-center bg-slate-900 dark:bg-white text-white dark:text-black hover:bg-slate-700 dark:hover:bg-gray-200 disabled:opacity-30 transition-colors"
            aria-label="Send message"
          >
            <Send class="w-3.5 h-3.5" />
          </button>
        {/if}
      </div>
      <p class="mt-2 text-[9px] text-gray-400 dark:text-gray-600 text-center font-medium">
        Enter to send · Shift+Enter for new line
      </p>
    </div>
  {/if}
</aside>
