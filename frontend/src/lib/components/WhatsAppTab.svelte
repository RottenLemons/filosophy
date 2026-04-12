<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
  import { fade, slide } from 'svelte/transition';
  import { MessageCircle, QrCode, Wifi, WifiOff, LogOut, RefreshCw, ArrowLeft, ExternalLink, Users, ChevronRight } from 'lucide-svelte';
  import { WAConnect, WADisconnect, WALogout, WAStatus, WAGetChats, WAGetChatMessages, WAGetLastQR, WAOpenChat } from '$lib/wailsjs/go/main/App';
  import { Check, Copy } from 'lucide-svelte';

  // ── Types ──────────────────────────────────────────────────────────────────
  type ConnStatus = 'disconnected' | 'connecting' | 'connected' | 'logged_out';

  interface Chat {
    jid: string;
    name: string;
    isGroup: boolean;
    lastMessage: string;
    lastAt: string;
    msgCount: number;
  }

  interface Message {
    messageId: string;
    chatJid: string;
    chatName: string;
    sender: string;
    senderName: string;
    text: string;
    timestamp: string;
    isGroup: boolean;
    mediaType: string;
    mediaName: string;
  }

  // ── State ──────────────────────────────────────────────────────────────────
  let status: ConnStatus = 'disconnected';
  let qrImage = '';
  let connecting = false;
  let loggingOut = false;
  let error = '';

  let chats: Chat[] = [];
  let selectedChat: Chat | null = null;
  let messages: Message[] = [];
  let loadingMessages = false;

  let threadEl: HTMLElement;
  let copiedMsgId: string | null = null;

  let unsubQR: (() => void) | null = null;
  let unsubStatus: (() => void) | null = null;
  let qrPollInterval: ReturnType<typeof setInterval> | undefined = undefined;

  onMount(async () => {
    try {
      const s = await WAStatus();
      status = s as ConnStatus;
      if (status === 'connected') loadChats();
    } catch {}

    if (window.runtime?.EventsOn) {
      unsubQR = window.runtime.EventsOn('wa_qr', (update: any) => {
        clearInterval(qrPollInterval);
        connecting = false;
        if (update.event === 'code') {
          qrImage = update.imageBase64;
          error = '';
        } else if (update.event === 'success') {
          qrImage = '';
          status = 'connected';
          loadChats();
        } else if (update.event === 'timeout') {
          qrImage = '';
          error = 'QR code timed out. Click Connect to try again.';
        } else {
          qrImage = '';
          error = update.error || 'Pairing error. Try again.';
        }
      });

      unsubStatus = window.runtime.EventsOn('wa_status', (s: string) => {
        status = s as ConnStatus;
        if (s === 'connected') {
          clearInterval(qrPollInterval);
          qrImage = '';
          loadChats();
        } else if (s === 'disconnected' || s === 'logged_out') {
          clearInterval(qrPollInterval);
          qrImage = '';
          chats = [];
          selectedChat = null;
          messages = [];
        }
      });
    }
  });

  onDestroy(() => {
    unsubQR?.();
    unsubStatus?.();
    clearInterval(qrPollInterval);
  });

  // ── Connection ─────────────────────────────────────────────────────────────

  async function connect() {
    connecting = true;
    error = '';
    qrImage = '';
    try {
      await WAConnect();
      clearInterval(qrPollInterval);
      qrPollInterval = setInterval(async () => {
        const s = await WAStatus();
        status = s as ConnStatus;
        if (s === 'connected') {
          clearInterval(qrPollInterval);
          qrImage = '';
          connecting = false;
          loadChats();
          return;
        }
        if (s === 'disconnected' || s === 'logged_out') {
          clearInterval(qrPollInterval);
          connecting = false;
          return;
        }
        const qr = await WAGetLastQR();
        if (qr) { qrImage = qr; connecting = false; }
      }, 500);
    } catch (e: any) {
      error = typeof e === 'string' ? e : (e?.message ?? 'Failed to connect');
      connecting = false;
    }
  }

  async function disconnect() {
    WADisconnect();
    status = 'disconnected';
    qrImage = '';
    chats = [];
    selectedChat = null;
    messages = [];
  }

  async function logout() {
    if (!confirm('Log out of WhatsApp? You will need to scan a QR code again.')) return;
    loggingOut = true;
    try {
      await WALogout();
      status = 'logged_out';
      qrImage = '';
      chats = [];
      selectedChat = null;
      messages = [];
    } catch (e: any) {
      error = typeof e === 'string' ? e : (e?.message ?? 'Logout failed');
    }
    loggingOut = false;
  }

  // ── Data ───────────────────────────────────────────────────────────────────

  async function loadChats() {
    try {
      chats = (await WAGetChats()) ?? [];
    } catch {}
  }

  async function selectChat(chat: Chat) {
    selectedChat = chat;
    messages = [];
    loadingMessages = true;
    try {
      const raw = (await WAGetChatMessages(chat.jid)) ?? [];
      // Backend returns newest-first; reverse so oldest is at top
      messages = [...raw].reverse();
    } catch {}
    loadingMessages = false;
    // Scroll to bottom after render
    await tick();
    threadEl?.scrollTo({ top: threadEl.scrollHeight, behavior: 'instant' });
  }

  function openInWhatsApp(jid: string) {
    WAOpenChat(jid);
  }

  async function copyMsgTimestamp(msg: Message) {
    // Format: "Sent on Wednesday 12 April 2026 at 14:32"
    // WhatsApp desktop has a calendar/jump-to-date feature — this gives the
    // user the exact date to jump to after opening the chat.
    const d = new Date(msg.timestamp);
    const label = d.toLocaleString([], {
      weekday: 'long', day: 'numeric', month: 'long', year: 'numeric',
      hour: '2-digit', minute: '2-digit',
    });
    const text = `Sent on ${label}`;
    try {
      await navigator.clipboard.writeText(text);
      copiedMsgId = msg.messageId;
      setTimeout(() => { if (copiedMsgId === msg.messageId) copiedMsgId = null; }, 2000);
    } catch {}
  }

  // ── Helpers ────────────────────────────────────────────────────────────────

  function formatTime(ts: string): string {
    if (!ts) return '';
    try {
      const d = new Date(ts);
      const now = new Date();
      if (d.toDateString() === now.toDateString())
        return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      const yesterday = new Date(now);
      yesterday.setDate(now.getDate() - 1);
      if (d.toDateString() === yesterday.toDateString()) return 'Yesterday';
      if (now.getTime() - d.getTime() < 7 * 86400000)
        return d.toLocaleDateString([], { weekday: 'short' });
      return d.toLocaleDateString([], { month: 'short', day: 'numeric' });
    } catch { return ''; }
  }

  function formatMsgTime(ts: string): string {
    if (!ts) return '';
    try {
      return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    } catch { return ''; }
  }

  function avatarLetter(chat: Chat): string {
    return (chat.name || chat.jid)[0].toUpperCase();
  }

  // Show a date separator when the day changes between messages
  function showDateSep(msgs: Message[], i: number): boolean {
    if (i === 0) return true;
    const a = new Date(msgs[i - 1].timestamp).toDateString();
    const b = new Date(msgs[i].timestamp).toDateString();
    return a !== b;
  }

  function formatSepDate(ts: string): string {
    if (!ts) return '';
    const d = new Date(ts);
    const now = new Date();
    if (d.toDateString() === now.toDateString()) return 'Today';
    const yesterday = new Date(now);
    yesterday.setDate(now.getDate() - 1);
    if (d.toDateString() === yesterday.toDateString()) return 'Yesterday';
    return d.toLocaleDateString([], { weekday: 'long', month: 'long', day: 'numeric' });
  }

  $: isConnected = status === 'connected';
  $: totalMsgs = chats.reduce((s, c) => s + (c.msgCount ?? 0), 0);
</script>

<div class="flex h-full bg-[#FAF9F6] dark:bg-[#111]">

  <!-- ── Left pane: chat list ──────────────────────────────────────────────── -->
  <div class="w-[300px] shrink-0 flex flex-col border-r border-gray-100 dark:border-[#2a2a2a] h-full">

    <!-- Header -->
    <div class="flex items-center justify-between px-5 py-4 border-b border-gray-100 dark:border-[#2a2a2a] shrink-0">
      <div class="flex items-center gap-2">
        <MessageCircle class="w-4 h-4 text-green-600 dark:text-green-400" />
        <span class="text-sm font-bold text-slate-900 dark:text-gray-100">WhatsApp</span>
      </div>
      <div class="flex items-center gap-1">
        {#if isConnected}
          <button on:click={disconnect} title="Disconnect" class="p-1.5 rounded text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 transition-colors">
            <WifiOff class="w-3.5 h-3.5" />
          </button>
          <button on:click={logout} disabled={loggingOut} title="Log out" class="p-1.5 rounded text-gray-400 hover:text-red-500 transition-colors disabled:opacity-40">
            <LogOut class="w-3.5 h-3.5" />
          </button>
        {/if}
      </div>
    </div>

    <!-- Status bar -->
    <div class="px-5 py-2 shrink-0 border-b border-gray-100 dark:border-[#2a2a2a]">
      {#if isConnected}
        <span class="flex items-center gap-1.5 text-[10px] font-semibold text-green-600 dark:text-green-400">
          <Wifi class="w-3 h-3" /> Connected
          {#if totalMsgs > 0}
            <span class="text-gray-400 dark:text-gray-500 font-normal">· {totalMsgs.toLocaleString()} messages</span>
          {/if}
        </span>
      {:else if connecting}
        <span class="flex items-center gap-1.5 text-[10px] font-semibold text-blue-500">
          <RefreshCw class="w-3 h-3 animate-spin" /> Connecting…
        </span>
      {:else}
        <span class="flex items-center gap-1.5 text-[10px] text-gray-400 dark:text-gray-500">
          <WifiOff class="w-3 h-3" /> {status === 'logged_out' ? 'Logged out' : 'Not connected'}
        </span>
      {/if}
    </div>

    <!-- Error -->
    {#if error}
      <div class="mx-4 mt-2 px-3 py-2 bg-red-50 dark:bg-red-950/20 border border-red-200 dark:border-red-900/30 rounded-lg text-[11px] text-red-600 dark:text-red-400" transition:fade>
        {error}
      </div>
    {/if}

    <!-- Body -->
    <div class="flex-1 overflow-y-auto scrollbar-custom">

      {#if !isConnected}
        <!-- Connect prompt -->
        <div class="flex flex-col items-center justify-center gap-5 py-12 px-6">
          {#if qrImage}
            <div class="p-3 bg-white rounded-xl shadow border border-gray-100" transition:fade>
              <img src={qrImage} alt="WhatsApp QR Code" class="w-48 h-48" />
            </div>
            <div class="text-center space-y-1">
              <p class="text-xs font-semibold text-gray-800 dark:text-gray-200">Scan with WhatsApp</p>
              <p class="text-[10px] text-gray-400 dark:text-gray-500 leading-relaxed">Settings → Linked Devices → Link a Device</p>
            </div>
          {:else}
            <div class="w-14 h-14 rounded-2xl bg-green-100 dark:bg-green-950/30 flex items-center justify-center">
              <MessageCircle class="w-7 h-7 text-green-600 dark:text-green-400" />
            </div>
            <div class="text-center space-y-1 max-w-[200px]">
              <p class="text-xs font-semibold text-gray-800 dark:text-gray-200">Connect WhatsApp</p>
              <p class="text-[10px] text-gray-400 dark:text-gray-500 leading-relaxed">Link your account to browse and search messages.</p>
            </div>
            <button
              on:click={connect}
              disabled={connecting}
              class="flex items-center gap-2 px-5 py-2 bg-green-600 hover:bg-green-700 text-white text-xs font-bold rounded-full transition-all shadow-md shadow-green-900/20 disabled:opacity-50"
            >
              {#if connecting}
                <RefreshCw class="w-3 h-3 animate-spin" /> Connecting…
              {:else}
                <QrCode class="w-3 h-3" /> Connect via QR
              {/if}
            </button>
          {/if}
        </div>

      {:else if chats.length === 0}
        <div class="flex flex-col items-center justify-center py-16 text-gray-400 dark:text-gray-600 px-6">
          <MessageCircle class="w-8 h-8 mb-3 opacity-30" />
          <p class="text-xs font-medium">No chats synced yet</p>
          <p class="text-[10px] mt-1 text-center opacity-70">Messages appear here after history syncs</p>
        </div>

      {:else}
        <!-- Chat list -->
        {#each chats as chat (chat.jid)}
          <button
            on:click={() => selectChat(chat)}
            class="w-full flex items-center gap-3 px-4 py-3 text-left transition-colors
                   {selectedChat?.jid === chat.jid
                     ? 'bg-green-50 dark:bg-green-950/20 border-l-2 border-green-500'
                     : 'hover:bg-gray-50 dark:hover:bg-[#1a1a1a] border-l-2 border-transparent'}"
          >
            <!-- Avatar -->
            <div class="w-10 h-10 rounded-full shrink-0 flex items-center justify-center font-bold text-sm
                        {chat.isGroup
                          ? 'bg-blue-100 dark:bg-blue-950/40 text-blue-700 dark:text-blue-400'
                          : 'bg-green-100 dark:bg-green-950/40 text-green-700 dark:text-green-400'}">
              {#if chat.isGroup}
                <Users class="w-4 h-4" />
              {:else}
                {avatarLetter(chat)}
              {/if}
            </div>
            <!-- Text -->
            <div class="flex-1 min-w-0">
              <div class="flex items-center justify-between gap-2">
                <span class="text-xs font-semibold text-gray-900 dark:text-gray-100 truncate">{chat.name || chat.jid}</span>
                <span class="text-[9px] text-gray-400 dark:text-gray-500 shrink-0">{formatTime(chat.lastAt)}</span>
              </div>
              <p class="text-[10px] text-gray-400 dark:text-gray-500 truncate mt-0.5">{chat.lastMessage || ''}</p>
            </div>
            <!-- Count -->
            <span class="shrink-0 min-w-[20px] text-center px-1.5 py-0.5 rounded-full bg-gray-100 dark:bg-[#222] text-[9px] font-bold text-gray-500 dark:text-gray-400">
              {chat.msgCount}
            </span>
          </button>
        {/each}
      {/if}

    </div>
  </div>

  <!-- ── Right pane: message thread ───────────────────────────────────────── -->
  <div class="flex-1 flex flex-col min-w-0 h-full">

    {#if !selectedChat}
      <!-- Empty state -->
      <div class="flex-1 flex flex-col items-center justify-center text-gray-300 dark:text-gray-700 select-none">
        <MessageCircle class="w-16 h-16 mb-4 opacity-30" />
        <p class="text-sm font-serif italic">{isConnected ? 'Select a chat to read messages' : 'Connect to get started'}</p>
      </div>

    {:else}
      <!-- Thread header -->
      <div class="flex items-center gap-3 px-6 py-4 border-b border-gray-100 dark:border-[#2a2a2a] shrink-0 bg-[#FAF9F6] dark:bg-[#111]">
        <!-- Back on mobile (hidden on large) -->
        <button on:click={() => selectedChat = null} class="p-1 -ml-1 text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 transition-colors lg:hidden">
          <ArrowLeft class="w-4 h-4" />
        </button>

        <!-- Avatar -->
        <div class="w-9 h-9 rounded-full flex items-center justify-center font-bold text-sm shrink-0
                    {selectedChat.isGroup
                      ? 'bg-blue-100 dark:bg-blue-950/40 text-blue-700 dark:text-blue-400'
                      : 'bg-green-100 dark:bg-green-950/40 text-green-700 dark:text-green-400'}">
          {#if selectedChat.isGroup}
            <Users class="w-4 h-4" />
          {:else}
            {avatarLetter(selectedChat)}
          {/if}
        </div>

        <div class="flex-1 min-w-0">
          <p class="text-sm font-bold text-slate-900 dark:text-gray-100 truncate">{selectedChat.name || selectedChat.jid}</p>
          <p class="text-[10px] text-gray-400 dark:text-gray-500">
            {selectedChat.isGroup ? 'Group' : 'Direct message'} · {selectedChat.msgCount} messages
          </p>
        </div>

        <!-- Open in WhatsApp -->
        <button
          on:click={() => openInWhatsApp(selectedChat!.jid)}
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-green-600 hover:bg-green-700 text-white text-[10px] font-bold transition-colors shadow-sm shrink-0"
          title="Open chat in WhatsApp. Hover a message and click the copy icon to copy its timestamp, then use WhatsApp's jump-to-date to find it."
        >
          <ExternalLink class="w-3 h-3" />
          Open in WhatsApp
        </button>
      </div>

      <!-- Messages -->
      <div bind:this={threadEl} class="flex-1 overflow-y-auto px-6 py-4 space-y-1 scrollbar-custom">
        {#if loadingMessages}
          <div class="flex items-center justify-center py-16 text-gray-400">
            <RefreshCw class="w-5 h-5 animate-spin" />
          </div>
        {:else if messages.length === 0}
          <div class="flex flex-col items-center justify-center py-16 text-gray-400 dark:text-gray-600">
            <p class="text-xs">No messages stored for this chat</p>
          </div>
        {:else}
          {#each messages as msg, i (msg.messageId)}
            <!-- Date separator -->
            {#if showDateSep(messages, i)}
              <div class="flex items-center gap-3 py-3">
                <div class="flex-1 h-px bg-gray-100 dark:bg-[#2a2a2a]"></div>
                <span class="text-[10px] font-semibold text-gray-400 dark:text-gray-500 uppercase tracking-wider shrink-0">
                  {formatSepDate(msg.timestamp)}
                </span>
                <div class="flex-1 h-px bg-gray-100 dark:bg-[#2a2a2a]"></div>
              </div>
            {/if}

            <!-- Message bubble -->
            <div class="group flex flex-col gap-0.5 py-0.5" transition:fade={{ duration: 80 }}>
              <!-- Sender name (groups or when sender changes) -->
              {#if selectedChat.isGroup && msg.senderName && (i === 0 || messages[i-1].sender !== msg.sender || showDateSep(messages, i))}
                <span class="text-[10px] font-semibold text-green-600 dark:text-green-400 px-1 mt-1">
                  {msg.senderName || msg.sender}
                </span>
              {/if}

              <div class="flex items-end gap-2 max-w-[80%]">
                <div class="flex-1 px-3 py-2 rounded-2xl rounded-tl-sm bg-white dark:bg-[#1a1a1a] border border-gray-100 dark:border-[#2a2a2a] shadow-sm">
                  {#if msg.text}
                    <p class="text-[13px] text-slate-800 dark:text-gray-200 leading-relaxed whitespace-pre-wrap break-words">{msg.text}</p>
                  {/if}
                  {#if msg.mediaType}
                    <div class="flex items-center gap-1.5 mt-1 text-[10px] text-gray-400 dark:text-gray-500 font-medium">
                      <span class="uppercase">{msg.mediaType}</span>
                      {#if msg.mediaName}<span class="truncate max-w-[200px]">· {msg.mediaName}</span>{/if}
                    </div>
                  {/if}
                  <p class="text-right text-[9px] text-gray-300 dark:text-gray-600 mt-1 leading-none">{formatMsgTime(msg.timestamp)}</p>
                </div>
                <!-- Copy timestamp button — visible on row hover -->
                <button
                  on:click={() => copyMsgTimestamp(msg)}
                  title="Copy timestamp — use WhatsApp's jump-to-date to find this message"
                  class="opacity-0 group-hover:opacity-100 transition-opacity p-1 rounded text-gray-300 hover:text-gray-500 dark:text-gray-600 dark:hover:text-gray-400 shrink-0"
                >
                  {#if copiedMsgId === msg.messageId}
                    <Check class="w-3 h-3 text-green-500" />
                  {:else}
                    <Copy class="w-3 h-3" />
                  {/if}
                </button>
              </div>
            </div>
          {/each}
        {/if}
      </div>
    {/if}

  </div>
</div>
