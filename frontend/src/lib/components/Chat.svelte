<script lang="ts">
  import { afterUpdate, createEventDispatcher } from 'svelte';
  import { fade, slide } from 'svelte/transition';
  import { X, Trash2, AlertCircle, Send, FileText, Settings } from 'lucide-svelte';

  const dispatch = createEventDispatcher();

  export let contextFile: any = null;
  export let fileContent: string = '';

  interface Message {
    role: 'user' | 'assistant';
    content: string;
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

    // Build system prompt
    let systemContent = 'You are a helpful assistant embedded in Filosophy, a local AI-powered file search app. Be concise and helpful.';
    if (contextFile) {
      systemContent += `\n\nContext: the user has selected the file "${contextFile.Path}".`;
      if (fileContent) {
        const preview = fileContent.slice(0, 6000);
        systemContent += `\n\nFile content:\n---\n${preview}${fileContent.length > 6000 ? '\n[content truncated]' : '\n---'}`;
      }
    }

    const apiMessages = [
      { role: 'system', content: systemContent },
      ...messages,
    ];

    let endpoint: string;
    if (cfg.provider === 'ollama') {
      endpoint = `${cfg.baseUrl.replace(/\/$/, '')}/v1/chat/completions`;
    } else {
      endpoint = `${cfg.baseUrl.replace(/\/$/, '')}/chat/completions`;
    }

    const assistantMsg: Message = { role: 'assistant', content: '' };
    messages = [...messages, assistantMsg];

    abortController = new AbortController();

    try {
      const headers: Record<string, string> = { 'Content-Type': 'application/json' };
      if (cfg.apiKey) headers['Authorization'] = `Bearer ${cfg.apiKey}`;

      const res = await fetch(endpoint, {
        method: 'POST',
        headers,
        signal: abortController.signal,
        body: JSON.stringify({
          model: cfg.model || undefined,
          messages: apiMessages,
          stream: true,
        }),
      });

      if (!res.ok) {
        const body = await res.text().catch(() => '');
        throw new Error(`HTTP ${res.status} — ${body || res.statusText}`);
      }

      const reader = res.body!.getReader();
      const decoder = new TextDecoder();
      let buffer = '';

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
            const delta = json.choices?.[0]?.delta?.content ?? '';
            if (delta) {
              assistantMsg.content += delta;
              messages = [...messages.slice(0, -1), { ...assistantMsg }];
            }
          } catch {}
        }
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
