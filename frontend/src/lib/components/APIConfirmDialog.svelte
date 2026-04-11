<script lang="ts">
  import { fade, scale } from 'svelte/transition';
  import { Shield, Search, X, Check } from 'lucide-svelte';

  export let request: { id: string; query: string } | null = null;

  import { createEventDispatcher } from 'svelte';
  const dispatch = createEventDispatcher<{ reply: { id: string; allowed: boolean } }>();

  function allow() {
    if (!request) return;
    dispatch('reply', { id: request.id, allowed: true });
  }

  function deny() {
    if (!request) return;
    dispatch('reply', { id: request.id, allowed: false });
  }

  function onKeydown(e: KeyboardEvent) {
    if (!request) return;
    if (e.key === 'Enter') allow();
    if (e.key === 'Escape') deny();
  }
</script>

<svelte:window on:keydown={onKeydown} />

{#if request}
  <!-- Backdrop -->
  <div
    class="fixed inset-0 z-[100] bg-black/40 dark:bg-black/60 backdrop-blur-sm flex items-center justify-center p-4"
    transition:fade={{ duration: 150 }}
    on:click={deny}
    role="presentation"
  >
    <!-- Card -->
    <div
      class="relative w-full max-w-sm bg-white dark:bg-[#111] border border-gray-200 dark:border-[#2a2a2a] shadow-2xl p-6 flex flex-col gap-5"
      transition:scale={{ duration: 150, start: 0.96 }}
      on:click|stopPropagation
      role="dialog"
      aria-modal="true"
      aria-labelledby="confirm-title"
    >
      <!-- Header -->
      <div class="flex items-start gap-3">
        <div class="w-9 h-9 shrink-0 bg-amber-50 dark:bg-amber-950/30 border border-amber-200 dark:border-amber-900/40 flex items-center justify-center">
          <Shield class="w-4 h-4 text-amber-500" />
        </div>
        <div class="flex-1 min-w-0">
          <p id="confirm-title" class="text-[11px] font-black uppercase tracking-[0.15em] text-slate-900 dark:text-gray-100">
            External Search Request
          </p>
          <p class="mt-0.5 text-[10px] text-gray-400 dark:text-gray-500">
            An external tool is requesting access to your files
          </p>
        </div>
        <button
          on:click={deny}
          class="shrink-0 p-1 text-gray-300 dark:text-gray-600 hover:text-gray-500 dark:hover:text-gray-400 transition-colors"
          aria-label="Deny"
        >
          <X class="w-4 h-4" />
        </button>
      </div>

      <!-- Query preview -->
      <div class="border border-gray-100 dark:border-[#2a2a2a] bg-gray-50 dark:bg-[#0a0a0a] px-4 py-3">
        <div class="flex items-center gap-2 mb-1.5">
          <Search class="w-3 h-3 text-gray-400 shrink-0" />
          <span class="text-[9px] font-bold uppercase tracking-widest text-gray-400">Query</span>
        </div>
        <p class="text-[13px] font-serif font-medium text-slate-800 dark:text-gray-100 break-words">
          {request.query}
        </p>
      </div>

      <!-- Actions -->
      <div class="flex gap-2">
        <button
          on:click={deny}
          class="flex-1 py-2.5 border border-gray-200 dark:border-[#2a2a2a] text-[10px] font-bold uppercase tracking-widest text-gray-500 dark:text-gray-400 hover:bg-gray-50 dark:hover:bg-[#1a1a1a] transition-colors"
        >
          Deny
        </button>
        <button
          on:click={allow}
          class="flex-1 py-2.5 bg-slate-900 dark:bg-white text-white dark:text-black text-[10px] font-bold uppercase tracking-widest hover:bg-slate-700 dark:hover:bg-gray-200 transition-colors flex items-center justify-center gap-2"
        >
          <Check class="w-3.5 h-3.5" />
          Allow
        </button>
      </div>

      <p class="text-[9px] text-gray-400 dark:text-gray-600 text-center -mt-1">
        Enter to allow · Esc to deny
      </p>
    </div>
  </div>
{/if}
