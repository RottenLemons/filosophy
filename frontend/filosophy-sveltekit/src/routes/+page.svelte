<script>
  import { Search, Button, Tag, ClickableTile } from 'carbon-components-svelte';
  import { goto } from '$app/navigation';

  // Time-aware greeting
  function getGreeting() {
    const h = new Date().getHours();
    if (h < 12) return 'Good morning';
    if (h < 17) return 'Good afternoon';
    return 'Good evening';
  }

  const suggestions = [
    'quarterly roadmap',
    'authentication docs',
    'pricing discussion',
    'open PRs',
    'onboarding flow'
  ];

  const recentDocs = [
    { id: 1, title: 'Q1 2026 Engineering Roadmap',        source: 'Notion',     time: '2h ago',     icon: '📝', pip: '#4361EE' },
    { id: 2, title: 'Customer Onboarding Deck v4',        source: 'Drive',      time: '5h ago',     icon: '📁', pip: '#4285F4' },
    { id: 3, title: 'API Design Review — Auth Service',   source: 'Confluence', time: 'Yesterday',  icon: '🌊', pip: '#172B4D' },
    { id: 4, title: '#product-team — Pricing Discussion', source: 'Slack',      time: '3 days ago', icon: '💬', pip: '#4A154B' },
  ];

  let searchValue = '';

  function handleSearch() {
    if (searchValue.trim()) {
      goto(`/search?q=${encodeURIComponent(searchValue.trim())}`);
    }
  }

  function handleKeydown(e) {
    if (e.key === 'Enter') handleSearch();
  }

  function runSuggestion(s) {
    goto(`/search?q=${encodeURIComponent(s)}`);
  }

  function openDoc(doc) {
    goto(`/search?q=${encodeURIComponent(doc.title)}`);
  }
</script>

<svelte:head>
  <title>Home — Filosophy</title>
</svelte:head>

<div class="home-page">
  <!-- Hero -->
  <div class="hero f-fade-up">
    <h1 class="greeting">{getGreeting()}, Alex.</h1>
    <p class="greeting-sub">What are you looking for today?</p>

    <div class="search-wrap">
      <Search
        bind:value={searchValue}
        placeholder="Search documents, messages, files…"
        size="xl"
        on:keydown={handleKeydown}
        on:clear={() => (searchValue = '')}
      />
      <Button kind="primary" size="field" on:click={handleSearch}>
        Search
      </Button>
    </div>

    <div class="chips" style="animation-delay:0.1s">
      {#each suggestions as s}
        <button class="chip" on:click={() => runSuggestion(s)}>{s}</button>
      {/each}
    </div>

    <div class="status-bar" style="animation-delay:0.18s">
      <span class="status-item">
        <span class="sdot" style="background:#1A7A4A"></span>
        3 apps connected
      </span>
      <span class="sep">·</span>
      <span class="status-item">17,153 documents indexed</span>
      <span class="sep">·</span>
      <span class="status-item">Last synced 4 min ago</span>
    </div>
  </div>

  <hr class="divider" />

  <!-- Recent docs -->
  <div class="recent-section f-fade-in" style="animation-delay:0.22s">
    <div class="section-row">
      <span class="section-label">Recently accessed</span>
      <button class="view-all" on:click={() => goto('/docs')}>View all</button>
    </div>

    <div class="doc-grid">
      {#each recentDocs as doc, i}
        <ClickableTile
          class="doc-tile"
          style="animation-delay:{0.06 * i}s"
          on:click={() => openDoc(doc)}
        >
          <div class="doc-source">
            <span class="pip" style="background:{doc.pip}"></span>
            {doc.icon} {doc.source}
          </div>
          <div class="doc-title">{doc.title}</div>
          <div class="doc-time">{doc.time}</div>
        </ClickableTile>
      {/each}
    </div>
  </div>
</div>

<style>
  .home-page {
    max-width: 800px;
    margin: 0 auto;
    padding: 48px 32px 64px;
  }

  /* ── Hero ── */
  .hero {
    text-align: center;
    margin-bottom: 40px;
  }
  .greeting {
    font-family: var(--f-font-serif);
    font-size: 2rem;
    font-weight: 500;
    letter-spacing: -0.3px;
    color: var(--f-text);
    margin-bottom: 6px;
  }
  .greeting-sub {
    font-size: 1rem;
    color: var(--f-text-3);
    margin-bottom: 28px;
  }

  .search-wrap {
    display: flex;
    gap: 8px;
    align-items: flex-end;
    max-width: 580px;
    margin: 0 auto 14px;
  }
  .search-wrap :global(.bx--search) {
    flex: 1;
  }
  .search-wrap :global(.bx--search-input) {
    border-radius: var(--f-radius) !important;
    height: 48px !important;
  }

  .chips {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    justify-content: center;
    margin-bottom: 20px;
  }
  .chip {
    background: var(--f-bg2);
    border: 1px solid var(--f-border);
    border-radius: 20px;
    padding: 5px 13px;
    font-size: 0.8125rem;
    color: var(--f-text-2);
    cursor: pointer;
    font-family: var(--f-font-ui);
    transition: border-color 0.13s, color 0.13s, background 0.13s;
  }
  .chip:hover {
    border-color: var(--f-accent-md);
    color: var(--f-accent);
    background: var(--f-accent-lt);
  }

  .status-bar {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 10px;
    font-size: 0.8rem;
    color: var(--f-text-3);
  }
  .status-item {
    display: flex;
    align-items: center;
    gap: 5px;
  }
  .sdot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    display: inline-block;
  }
  .sep { color: var(--f-border-md); }

  /* ── Divider ── */
  .divider {
    border: none;
    border-top: 1px solid var(--f-border);
    margin: 0 0 28px;
  }

  /* ── Recent ── */
  .recent-section { animation: f-fade-in 0.4s ease both; }

  .section-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 14px;
  }
  .section-label {
    font-size: 0.75rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--f-text-3);
  }
  .view-all {
    font-size: 0.8rem;
    color: var(--f-accent);
    background: none;
    border: none;
    cursor: pointer;
    font-family: var(--f-font-ui);
    padding: 0;
  }
  .view-all:hover { text-decoration: underline; }

  .doc-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(210px, 1fr));
    gap: 10px;
  }

  /* Override tile styles */
  :global(.doc-tile) {
    border-radius: var(--f-radius) !important;
    padding: 16px !important;
    animation: f-result-in 0.35s cubic-bezier(0.4,0,0.2,1) both;
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
    font-size: 0.75rem;
    color: var(--f-text-3);
  }
</style>
