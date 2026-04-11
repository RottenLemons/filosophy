<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { fade } from 'svelte/transition';
  import { MessageCircle, QrCode, Wifi, WifiOff, LogOut, RefreshCw, Users, Clock } from 'lucide-svelte';
  import { WAConnect, WADisconnect, WALogout, WAStatus, WAGetChats, WAGetLastQR } from '$lib/wailsjs/go/main/App';

  // ── State ──────────────────────────────────────────────────────────────────
  type ConnStatus = 'disconnected' | 'connecting' | 'connected' | 'logged_out';

  let status: ConnStatus = 'disconnected';
  let qrImage = '';
  let connecting = false;
  let loggingOut = false;
  let error = '';

  let chats: any[] = [];
  let msgCount = 0;

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
        }
      });
    }
  });

  onDestroy(() => {
    unsubQR?.();
    unsubStatus?.();
    clearInterval(qrPollInterval);
  });

  // ── Actions ────────────────────────────────────────────────────────────────

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
        if (qr) {
          qrImage = qr;
          connecting = false;
        }
      }, 500);
    } catch (e: any) {
      error = (typeof e === 'string' ? e : (e?.message ?? 'Failed to connect'));
      connecting = false;
    }
  }

  async function disconnect() {
    WADisconnect();
    status = 'disconnected';
    qrImage = '';
    chats = [];
  }

  async function logout() {
    if (!confirm('Log out of WhatsApp? You will need to scan a QR code again.')) return;
    loggingOut = true;
    try {
      await WALogout();
      status = 'logged_out';
      qrImage = '';
      chats = [];
    } catch (e: any) {
      error = (typeof e === 'string' ? e : (e?.message ?? 'Logout failed'));
    }
    loggingOut = false;
  }

  async function loadChats() {
    try {
      const result = await WAGetChats();
      chats = result ?? [];
      msgCount = chats.reduce((sum: number, c: any) => sum + (c.msgCount ?? 0), 0);
    } catch {}
  }

  function formatTime(ts: string): string {
    if (!ts) return '';
    try {
      const d = new Date(ts);
      const now = new Date();
      if (d.toDateString() === now.toDateString())
        return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      return d.toLocaleDateString([], { month: 'short', day: 'numeric' });
    } catch { return ''; }
  }

  $: isConnected = status === 'connected';
</script>

<div class="flex flex-col h-full">

  <!-- Header -->
  <div class="flex items-center justify-between px-6 py-4 border-b border-gray-100 dark:border-[#2a2a2a] shrink-0">
    <div class="flex items-center gap-2">
      <MessageCircle class="w-4 h-4 text-green-600 dark:text-green-400" />
      <span class="text-sm font-bold text-slate-900 dark:text-gray-100">WhatsApp</span>
    </div>

    <!-- Status + actions -->
    <div class="flex items-center gap-1.5">
      {#if isConnected}
        <span class="flex items-center gap-1 px-2 py-0.5 rounded-full text-[9px] font-bold uppercase tracking-wider bg-green-100 dark:bg-green-950/30 text-green-700 dark:text-green-400">
          <Wifi class="w-2.5 h-2.5" /> Online
        </span>
        <button on:click={disconnect} title="Disconnect" class="p-1 rounded text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 transition-colors">
          <WifiOff class="w-3.5 h-3.5" />
        </button>
        <button on:click={logout} disabled={loggingOut} title="Log out" class="p-1 rounded text-gray-400 hover:text-red-500 transition-colors disabled:opacity-40">
          <LogOut class="w-3.5 h-3.5" />
        </button>
      {:else if connecting}
        <span class="flex items-center gap-1 px-2 py-0.5 rounded-full text-[9px] font-bold uppercase tracking-wider bg-blue-100 dark:bg-blue-950/30 text-blue-600 dark:text-blue-400">
          <RefreshCw class="w-2.5 h-2.5 animate-spin" /> Connecting
        </span>
      {:else}
        <span class="flex items-center gap-1 px-2 py-0.5 rounded-full text-[9px] font-bold uppercase tracking-wider bg-gray-100 dark:bg-[#222] text-gray-500 dark:text-gray-400">
          <WifiOff class="w-2.5 h-2.5" /> {status === 'logged_out' ? 'Logged out' : 'Offline'}
        </span>
      {/if}
    </div>
  </div>

  <!-- Error -->
  {#if error}
    <div class="mx-4 mt-3 px-3 py-2 bg-red-50 dark:bg-red-950/20 border border-red-200 dark:border-red-900/30 rounded-lg text-xs text-red-600 dark:text-red-400" transition:fade>
      {error}
    </div>
  {/if}

  <!-- Body -->
  <div class="flex-1 overflow-y-auto scrollbar-custom">

    <!-- ── Connect / QR ── -->
    {#if !isConnected}
      <div class="flex flex-col items-center justify-center gap-5 py-10 px-6">
        {#if qrImage}
          <div class="p-3 bg-white rounded-xl shadow border border-gray-100" transition:fade>
            <img src={qrImage} alt="WhatsApp QR Code" class="w-44 h-44" />
          </div>
          <div class="text-center space-y-1">
            <p class="text-xs font-semibold text-gray-800 dark:text-gray-200">Scan with WhatsApp</p>
            <p class="text-[10px] text-gray-400 dark:text-gray-500">Settings → Linked Devices → Link a Device</p>
          </div>
        {:else}
          <div class="w-14 h-14 rounded-2xl bg-green-100 dark:bg-green-950/30 flex items-center justify-center">
            <MessageCircle class="w-7 h-7 text-green-600 dark:text-green-400" />
          </div>
          <div class="text-center space-y-1 max-w-[200px]">
            <p class="text-xs font-semibold text-gray-800 dark:text-gray-200">Connect WhatsApp</p>
            <p class="text-[10px] text-gray-400 dark:text-gray-500 leading-relaxed">
              Link your account to index messages for search.
            </p>
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

    <!-- ── Chats list ── -->
    {:else}
      {#if msgCount > 0}
        <div class="px-6 pt-3 pb-1 flex items-center gap-1.5 text-[10px] text-gray-400 dark:text-gray-500">
          <Users class="w-3 h-3" />
          {msgCount.toLocaleString()} messages · {chats.length} chat{chats.length !== 1 ? 's' : ''}
        </div>
      {/if}

      {#if chats.length === 0}
        <div class="flex flex-col items-center justify-center py-12 text-gray-400 dark:text-gray-600 px-6">
          <MessageCircle class="w-7 h-7 mb-2 opacity-40" />
          <p class="text-xs font-medium">No chats yet</p>
          <p class="text-[10px] mt-0.5 text-center">Messages appear here after history sync</p>
        </div>
      {:else}
        <div class="px-3 py-2 space-y-1">
          {#each chats as chat (chat.jid)}
            <div class="flex items-center gap-3 px-3 py-2.5 rounded-lg hover:bg-gray-50 dark:hover:bg-[#1a1a1a] transition-colors">
              <div class="w-7 h-7 rounded-full bg-green-100 dark:bg-green-950/40 flex items-center justify-center shrink-0 text-green-700 dark:text-green-400 text-[10px] font-bold">
                {(chat.name || chat.jid)[0].toUpperCase()}
              </div>
              <div class="flex-1 min-w-0">
                <div class="flex items-center justify-between gap-1">
                  <span class="text-[11px] font-semibold text-gray-800 dark:text-gray-200 truncate">{chat.name || chat.jid}</span>
                  <span class="text-[9px] text-gray-400 dark:text-gray-500 shrink-0 flex items-center gap-0.5">
                    <Clock class="w-2.5 h-2.5" />{formatTime(chat.lastAt)}
                  </span>
                </div>
                <p class="text-[10px] text-gray-400 dark:text-gray-500 truncate">{chat.lastMessage || ''}</p>
              </div>
              <span class="shrink-0 px-1.5 py-0.5 rounded-full bg-gray-100 dark:bg-[#222] text-[9px] font-bold text-gray-500 dark:text-gray-400">{chat.msgCount}</span>
            </div>
          {/each}
        </div>
      {/if}
    {/if}
  </div>
</div>
