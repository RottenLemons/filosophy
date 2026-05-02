<script>
  import { goto } from '$app/navigation';
  import { Search } from 'carbon-icons-svelte';

  let filterQuery = '';

  const allDocs = [
    { id: 1,  title: 'Q1 2026 Engineering Roadmap',         source: 'Notion',     time: '2h ago',      icon: '📝', pip: '#4361EE' },
    { id: 2,  title: 'Customer Onboarding Deck v4',         source: 'Drive',      time: '5h ago',      icon: '📁', pip: '#4285F4' },
    { id: 3,  title: 'API Design Review — Auth Service',    source: 'Confluence', time: 'Yesterday',   icon: '🌊', pip: '#172B4D' },
    { id: 4,  title: '#product-team — Pricing Discussion',  source: 'Slack',      time: '3 days ago',  icon: '💬', pip: '#4A154B' },
    { id: 5,  title: 'Q1 2026 Engineering Roadmap',         source: 'Notion',     time: '1 week ago',  icon: '📝', pip: '#4361EE' },
    { id: 6,  title: 'Customer Onboarding Deck v4',         source: 'Drive',      time: '1 week ago',  icon: '📁', pip: '#4285F4' },
    { id: 7,  title: 'API Design Review — Auth Service',    source: 'Confluence', time: '2 weeks ago', icon: '🌊', pip: '#172B4D' },
    { id: 8,  title: '#product-team — Pricing Discussion',  source: 'Slack',      time: '2 weeks ago', icon: '💬', pip: '#4A154B' },
  ];

  $: filtered = filterQuery
    ? allDocs.filter(d => d.title.toLowerCase().includes(filterQuery.toLowerCase()))
    : allDocs;

  function openDoc(doc) {
    goto(`/search?q=${encodeURIComponent(doc.title)}`);
  }
</script>

<svelte:head>
  <title>Documents — Filosophy</title>
</svelte:head>

<div class="docs-page">
  <div class="docs-header f-fade-up">
    <h2 class="docs-title">Documents</h2>
    <div class="filter-wrap">
      <span class="filter-icon"><Search size={15} /></span>
      <input
        class="filter-input"
        placeholder="Filter documents…"
        bind:value={filterQuery}
      />
    </div>
  </div>

  {#if filtered.length === 0}
    <div class="empty">
      <div class="empty-icon">🔍</div>
      <div class="empty-title">No documents found</div>
      <div class="empty-sub">Try a different search term</div>
    </div>
  {:else}
    <div class="doc-grid">
      {#each filtered as doc, i}
        <button
          class="doc-card f-result-in"
          style="animation-delay:{i * 0.04}s"
          on:click={() => openDoc(doc)}
        >
          <div class="doc-source">
            <span class="pip" style="background:{doc.pip}"></span>
            {doc.icon} {doc.source}
          </div>
          <div class="doc-title">{doc.title}</div>
          <div class="doc-time">{doc.time}</div>
        </button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .docs-page {
    padding: 32px 36px;
    max-width: 860px;
  }

  .docs-header {
    margin-bottom: 24px;
  }
  .docs-title {
    font-family: var(--f-font-serif);
    font-size: 1.375rem;
    font-weight: 500;
    letter-spacing: -0.3px;
    margin: 0 0 16px;
  }
  .filter-wrap {
    position: relative;
    max-width: 380px;
  }
  .filter-icon {
    position: absolute;
    left: 11px;
    top: 50%;
    transform: translateY(-50%);
    color: var(--f-text-3);
    display: flex;
    align-items: center;
    pointer-events: none;
  }
  .filter-input {
    width: 100%;
    padding: 9px 14px 9px 36px;
    background: var(--f-bg2);
    border: 1.5px solid var(--f-border-md);
    border-radius: var(--f-radius);
    font-size: 0.875rem;
    font-family: var(--f-font-ui);
    color: var(--f-text);
    outline: none;
    transition: border-color 0.15s, box-shadow 0.15s;
  }
  .filter-input::placeholder { color: var(--f-text-3); }
  .filter-input:focus {
    border-color: var(--f-accent);
    box-shadow: 0 0 0 3px rgba(27,94,142,0.09);
  }

  .doc-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(210px, 1fr));
    gap: 10px;
  }

  .doc-card {
    background: var(--f-bg2);
    border: 1px solid var(--f-border);
    border-radius: var(--f-radius);
    padding: 16px;
    cursor: pointer;
    text-align: left;
    font-family: var(--f-font-ui);
    transition: border-color 0.15s, box-shadow 0.15s, transform 0.15s;
    width: 100%;
  }
  .doc-card:hover {
    border-color: var(--f-border-md);
    box-shadow: var(--f-shadow-md);
    transform: translateY(-1px);
  }

  .doc-source {
    display: flex;
    align-items: center;
    gap: 5px;
    font-size: 0.7rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--f-text-3);
    margin-bottom: 8px;
  }
  .pip {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    display: inline-block;
    flex-shrink: 0;
  }
  .doc-title {
    font-size: 0.875rem;
    font-weight: 600;
    color: var(--f-text);
    line-height: 1.4;
    margin-bottom: 6px;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .doc-time {
    font-size: 0.72rem;
    color: var(--f-text-3);
  }

  /* Empty state */
  .empty {
    text-align: center;
    padding: 56px 24px;
    color: var(--f-text-3);
  }
  .empty-icon { font-size: 2rem; margin-bottom: 10px; }
  .empty-title { font-size: 0.9375rem; font-weight: 600; color: var(--f-text-2); margin-bottom: 4px; }
  .empty-sub   { font-size: 0.8125rem; }
</style>
