<script>
  import { page } from '$app/stores';
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { Search, Tag, ClickableTile, Button, InlineNotification } from 'carbon-components-svelte';
  import {
    DocumentText,
    LogoSlack,
    LogoGithub,
    Time,
    Idea
  } from 'carbon-icons-svelte';

  // Read query from URL
  let query = '';
  let searchValue = '';
  let activeFilter = 'All';

  $: query = $page.url.searchParams.get('q') || '';
  $: searchValue = query;

  const filters = ['All', 'Notion', 'Drive', 'Slack', 'GitHub', 'Confluence'];

  const allResults = [
    { id: 1, icon: '📝', title: 'Q1 2026 Engineering Roadmap',           snippet: 'Key initiatives include <mark>authentication</mark> refactor, new search pipeline, and mobile app release. Timeline begins March 1.',                                                  source: 'Notion',     time: '2h ago',  tags: ['Engineering', 'Q1'] },
    { id: 2, icon: '📁', title: 'Auth Service API Specification v2.3',   snippet: 'The new <mark>authentication</mark> endpoint supports OAuth 2.0 and PKCE flow. Token refresh interval is configurable per client app.',                                                   source: 'Drive',      time: 'Jan 12',  tags: ['API', 'Auth'] },
    { id: 3, icon: '🌊', title: 'Authentication — Architecture Decision', snippet: 'Decision: migrate from session cookies to JWT-based <mark>authentication</mark>. This enables stateless scaling and cross-service token sharing.',                                        source: 'Confluence', time: 'Dec 8',   tags: ['ADR', 'Security'] },
    { id: 4, icon: '💬', title: '#eng-platform — Auth Refactor Thread',  snippet: '"We should prioritize the <mark>authentication</mark> migration before Q2." — alice · "Token library is nearly ready." — bob',                                                           source: 'Slack',      time: '3d ago',  tags: ['Conversation'] },
    { id: 5, icon: '🐙', title: 'PR #482 — feat: JWT middleware',        snippet: 'Adds <mark>authentication</mark> middleware using the new token validator. Reviewed by 3 engineers. All tests passing on main.',                                                          source: 'GitHub',     time: '1w ago',  tags: ['PR', 'Code'] },
  ];

  $: results = activeFilter === 'All'
    ? allResults
    : allResults.filter(r => r.source === activeFilter);

  function newSearch() {
    if (searchValue.trim()) {
      goto(`/search?q=${encodeURIComponent(searchValue.trim())}`);
    }
  }

  function handleKeydown(e) {
    if (e.key === 'Enter') newSearch();
  }
</script>

<svelte:head>
  <title>Search — Filosophy</title>
</svelte:head>

<div class="search-page">
  <!-- Search bar row -->
  <div class="top-row f-fade-in">
    <div class="search-wrap">
      <Search
        bind:value={searchValue}
        placeholder="Search again…"
        size="lg"
        on:keydown={handleKeydown}
        on:clear={() => (searchValue = '')}
      />
      <Button kind="primary" size="field" on:click={newSearch}>Search</Button>
    </div>
    <span class="results-meta">{results.length} results · 0.3s</span>
  </div>

  <!-- Filters -->
  <div class="filters f-fade-in" style="animation-delay:0.05s">
    {#each filters as f}
      <button
        class="filter-btn"
        class:active={activeFilter === f}
        on:click={() => (activeFilter = f)}
      >{f}</button>
    {/each}
  </div>

  <!-- AI Summary -->
  <div class="ai-box f-result-in" style="animation-delay:0.08s">
    <div class="ai-box-label">
      <Idea size={14} />
      AI Summary
    </div>
    <p class="ai-box-text">
      Your search spans <strong>authentication</strong> topics. The main thread is an in-progress
      migration from session-based to JWT auth — with an API spec in Drive, an architecture
      decision in Confluence, and active work in Slack and GitHub.
    </p>
    <div class="ai-sources">
      {#each ['Drive','Confluence','Slack','GitHub'] as s}
        <span class="src-tag">{s}</span>
      {/each}
    </div>
  </div>

  <!-- Results -->
  <div class="results-list">
    {#each results as r, i}
      <div class="result-card f-result-in" style="animation-delay:{i * 0.05}s">
        <div class="result-icon">{r.icon}</div>
        <div class="result-body">
          <div class="result-title">{r.title}</div>
          <div class="result-snippet">{@html r.snippet}</div>
          <div class="result-footer">
            <span class="result-source">{r.source}</span>
            <span class="dot">·</span>
            <Time size={12} />
            <span>{r.time}</span>
            {#each r.tags as tag}
              <span class="result-tag">{tag}</span>
            {/each}
          </div>
        </div>
      </div>
    {/each}
  </div>
</div>

<style>
  .search-page {
    padding: 32px 36px;
    max-width: 860px;
  }

  .top-row {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 18px;
    flex-wrap: wrap;
  }
  .search-wrap {
    display: flex;
    gap: 8px;
    align-items: flex-end;
    flex: 1;
    min-width: 300px;
  }
  .search-wrap :global(.bx--search) { flex: 1; }
  .results-meta {
    font-size: 0.8rem;
    color: var(--f-text-3);
    white-space: nowrap;
  }

  /* Filters */
  .filters {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    margin-bottom: 20px;
  }
  .filter-btn {
    padding: 5px 13px;
    border-radius: 20px;
    background: var(--f-bg2);
    border: 1px solid var(--f-border);
    font-size: 0.8125rem;
    font-weight: 500;
    color: var(--f-text-2);
    cursor: pointer;
    font-family: var(--f-font-ui);
    transition: all 0.13s;
  }
  .filter-btn:hover { border-color: var(--f-border-md); color: var(--f-text); }
  .filter-btn.active {
    background: var(--f-accent-lt);
    border-color: var(--f-accent-md);
    color: var(--f-accent);
  }

  /* AI Box */
  .ai-box {
    background: var(--f-accent-lt);
    border: 1px solid var(--f-accent-md);
    border-radius: 10px;
    padding: 16px 20px;
    margin-bottom: 20px;
  }
  .ai-box-label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 0.7rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.07em;
    color: var(--f-accent);
    margin-bottom: 8px;
  }
  .ai-box-text {
    font-size: 0.875rem;
    line-height: 1.7;
    color: var(--f-text);
    margin: 0 0 10px;
  }
  .ai-sources {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }
  .src-tag {
    background: var(--f-bg2);
    border: 1px solid var(--f-border);
    border-radius: 6px;
    padding: 2px 9px;
    font-size: 0.75rem;
    color: var(--f-text-2);
    cursor: pointer;
    transition: border-color 0.13s;
  }
  .src-tag:hover { border-color: var(--f-accent-md); color: var(--f-accent); }

  /* Results */
  .results-list {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .result-card {
    background: var(--f-bg2);
    border: 1px solid var(--f-border);
    border-radius: var(--f-radius);
    padding: 16px 18px;
    display: flex;
    gap: 14px;
    cursor: pointer;
    transition: border-color 0.15s, box-shadow 0.15s;
  }
  .result-card:hover {
    border-color: var(--f-border-md);
    box-shadow: var(--f-shadow-md);
  }
  .result-icon {
    width: 36px;
    height: 36px;
    border-radius: 8px;
    background: var(--f-bg3);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 18px;
    flex-shrink: 0;
  }
  .result-body { flex: 1; min-width: 0; }
  .result-title {
    font-size: 0.9375rem;
    font-weight: 600;
    color: var(--f-text);
    margin-bottom: 4px;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .result-snippet {
    font-size: 0.8125rem;
    color: var(--f-text-2);
    line-height: 1.6;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  :global(.result-snippet mark) {
    background: rgba(27,94,142,0.12);
    color: var(--f-accent);
    border-radius: 3px;
    padding: 0 2px;
  }
  .result-footer {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 8px;
    font-size: 0.75rem;
    color: var(--f-text-3);
  }
  .dot { color: var(--f-border-md); }
  .result-source { font-weight: 500; }
  .result-tag {
    background: var(--f-bg3);
    border-radius: 4px;
    padding: 1px 7px;
    font-size: 0.7rem;
    color: var(--f-text-3);
  }
</style>
