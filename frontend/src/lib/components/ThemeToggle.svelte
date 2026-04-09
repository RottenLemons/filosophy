<script lang="ts">
  import { Sun, Moon } from 'lucide-svelte';
  import { themeStore, toggleTheme } from '../../stores/theme';
</script>

<button
  on:click={toggleTheme}
  class="p-2 rounded-full transition-all duration-300 hover:opacity-100 opacity-60 text-slate-900 dark:text-gray-100 focus:outline-none group cursor-pointer"
  style="--wails-draggable:no-drag; -webkit-app-region:no-drag; pointer-events:auto;"
  aria-label="Toggle Dark Mode"
>
  {#if $themeStore === 'dark'}
    <Sun class="w-5 h-5 transition-transform group-hover:rotate-45" />
  {:else}
    <Moon class="w-5 h-5 transition-transform group-hover:-rotate-[15deg]" />
  {/if}
</button>
