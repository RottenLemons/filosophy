<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/stores';
  import { Search, Bot, Cable, Settings, Sun, Moon } from 'lucide-svelte';
  import '../app.css';
  import { themeStore, toggleTheme } from '../stores/theme';
  import { indexingStatus, initIndexerStore } from '../stores/indexer';

  const navigation = [
    { href: '/', label: 'Search', Icon: Search },
    { href: '/chat', label: 'Assistant', Icon: Bot },
    { href: '/connections', label: 'Connections', Icon: Cable }
  ];

  onMount(() => {
    initIndexerStore();
  });

  $: currentPath = $page.url.pathname;
  $: readinessTitle = $indexingStatus.enhancedReady
    ? 'Search ready'
    : $indexingStatus.searchReady
      ? 'Basic search ready'
      : $indexingStatus.isIndexing
        ? 'Preparing search'
        : 'Search unavailable';
  $: mobileReadiness = $indexingStatus.enhancedReady
    ? 'Ready'
    : $indexingStatus.searchReady
      ? 'Basic'
      : $indexingStatus.isIndexing
        ? 'Starting'
        : 'Offline';
  $: etaMatch = $indexingStatus.statusMessage.match(/\bETA\s+(.+)$/i);
  $: statusDetail = etaMatch ? `ETA ${etaMatch[1]}` : $indexingStatus.statusMessage;
  $: {
    if (typeof window !== 'undefined') {
      document.documentElement.classList.toggle('dark', $themeStore === 'dark');
      localStorage.setItem('theme', $themeStore);
    }
  }
</script>

<div class="app-frame">
  <aside class="app-sidebar" aria-label="Main navigation">
    <a class="brand" href="/" aria-label="Filosophy home">
      <span class="brand-mark">F</span>
      <span>Filosophy</span>
    </a>

    <div class="nav-label">Workspace</div>
    <nav class="primary-nav">
      {#each navigation as item}
        <a class:active={currentPath === item.href || (item.href === '/' && currentPath === '/search')} href={item.href} aria-current={currentPath === item.href ? 'page' : undefined}>
          <svelte:component this={item.Icon} size={17} strokeWidth={1.8} />
          <span>{item.label}</span>
        </a>
      {/each}
    </nav>

    <div class="sidebar-spacer"></div>
    <div class="index-state" aria-live="polite">
      <span class:busy={$indexingStatus.isIndexing} class:offline={!$indexingStatus.searchReady && !$indexingStatus.isIndexing} class="state-dot"></span>
      <span class="state-copy">
        <strong>{readinessTitle}</strong>
        <small>{statusDetail}</small>
      </span>
      {#if $indexingStatus.isIndexing}
        <span class="state-progress">{Math.round($indexingStatus.progress)}%</span>
      {/if}
    </div>
    {#if $indexingStatus.isIndexing}
      <div class="progress-track"><span class:indeterminate={!$indexingStatus.searchReady} style={`width:${$indexingStatus.searchReady ? $indexingStatus.progress : 30}%`}></span></div>
    {/if}
    <a class:active={currentPath === '/settings'} class="settings-link" href="/settings">
      <Settings size={17} strokeWidth={1.8} />
      <span>Settings</span>
    </a>
    <button class="theme-button" type="button" on:click={toggleTheme} aria-label={$themeStore === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}>
      {#if $themeStore === 'dark'}<Sun size={16} />{:else}<Moon size={16} />{/if}
      <span>{$themeStore === 'dark' ? 'Light appearance' : 'Dark appearance'}</span>
    </button>
    <div class="privacy-note"><span></span> Private by default</div>
  </aside>

  <div class="app-content">
    <div class="mobile-brand">
      <a class="brand" href="/" aria-label="Filosophy home"><span class="brand-mark">F</span><span>Filosophy</span></a>
      <span class="mobile-status" class:offline={!$indexingStatus.searchReady && !$indexingStatus.isIndexing}><i></i>{mobileReadiness}</span>
    </div>
    <nav class="mobile-nav" aria-label="Main navigation">
      {#each navigation as item}
        <a class:active={currentPath === item.href || (item.href === '/' && currentPath === '/search')} href={item.href}>
          <svelte:component this={item.Icon} size={16} strokeWidth={1.8} /><span>{item.label}</span>
        </a>
      {/each}
      <a class:active={currentPath === '/settings'} href="/settings"><Settings size={16} /><span>Settings</span></a>
    </nav>
    <main class="app-main"><slot /></main>
  </div>
</div>
