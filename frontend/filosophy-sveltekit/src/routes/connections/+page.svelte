<script>
  let integrations = [
    { id: 'gdrive',     name: 'Google Drive',  icon: '📁', connected: true,  desc: 'Docs, Sheets, Slides, and folders',      count: '3,421 docs' },
    { id: 'slack',      name: 'Slack',         icon: '💬', connected: true,  desc: 'Messages, threads, and shared files',    count: '12,840 messages' },
    { id: 'notion',     name: 'Notion',        icon: '📝', connected: true,  desc: 'Pages, databases, and wikis',            count: '892 pages' },
    { id: 'gmail',      name: 'Gmail',         icon: '✉️', connected: false, desc: 'Emails, threads, and attachments',       count: null },
    { id: 'github',     name: 'GitHub',        icon: '🐙', connected: false, desc: 'Repos, PRs, issues, and READMEs',        count: null },
    { id: 'whatsapp',   name: 'WhatsApp',      icon: '📱', connected: false, desc: 'Business chat history',                  count: null },
    { id: 'telegram',   name: 'Telegram',      icon: '✈️', connected: false, desc: 'Channels, groups, and DMs',              count: null },
    { id: 'confluence', name: 'Confluence',    icon: '🌊', connected: false, desc: 'Team spaces and documentation',          count: null },
    { id: 'onedrive',   name: 'OneDrive',      icon: '☁️', connected: false, desc: 'Personal and shared files',              count: null },
  ];

  function toggle(id) {
    integrations = integrations.map(i =>
      i.id === id ? { ...i, connected: !i.connected } : i
    );
  }

  $: connectedCount = integrations.filter(i => i.connected).length;
</script>

<svelte:head>
  <title>Connections — Filosophy</title>
</svelte:head>

<div class="connections-page">
  <div class="conn-header f-fade-up">
    <h2 class="conn-title">Connections</h2>
    <p class="conn-sub">
      Connect your apps so Filosophy can search across all of them.
      <strong>{connectedCount} of {integrations.length}</strong> connected.
    </p>
  </div>

  <div class="intg-grid">
    {#each integrations as intg, i}
      <div
        class="intg-card f-result-in"
        class:connected={intg.connected}
        style="animation-delay:{i * 0.04}s"
      >
        <div class="intg-status">
          <span class="sdot" class:on={intg.connected} class:off={!intg.connected}></span>
          <span class="status-label" class:green={intg.connected} class:muted={!intg.connected}>
            {intg.connected ? 'Connected' : 'Not connected'}
          </span>
        </div>

        <div class="intg-icon">{intg.icon}</div>
        <div class="intg-name">{intg.name}</div>
        <div class="intg-desc">{intg.desc}</div>

        {#if intg.connected && intg.count}
          <div class="intg-count">✓ {intg.count} indexed</div>
        {/if}

        <button
          class="intg-btn"
          class:btn-connected={intg.connected}
          class:btn-disconnected={!intg.connected}
          on:click={() => toggle(intg.id)}
        >
          {intg.connected ? '✓ Connected' : `Connect ${intg.name}`}
        </button>
      </div>
    {/each}
  </div>
</div>

<style>
  .connections-page {
    padding: 32px 36px;
    max-width: 920px;
  }

  .conn-header {
    margin-bottom: 28px;
  }
  .conn-title {
    font-family: var(--f-font-serif);
    font-size: 1.375rem;
    font-weight: 500;
    letter-spacing: -0.3px;
    margin: 0 0 6px;
  }
  .conn-sub {
    font-size: 0.875rem;
    color: var(--f-text-3);
    margin: 0;
  }

  .intg-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(210px, 1fr));
    gap: 10px;
  }

  .intg-card {
    background: var(--f-bg2);
    border: 1px solid var(--f-border);
    border-radius: 12px;
    padding: 18px;
    position: relative;
    transition: border-color 0.15s, box-shadow 0.15s;
  }
  .intg-card:hover {
    border-color: var(--f-border-md);
    box-shadow: var(--f-shadow-md);
  }
  .intg-card.connected {
    border-color: #b8dfc8;
  }

  .intg-status {
    position: absolute;
    top: 14px;
    right: 14px;
    display: flex;
    align-items: center;
    gap: 4px;
  }
  .sdot {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    display: inline-block;
  }
  .sdot.on  { background: var(--f-green); }
  .sdot.off { background: var(--f-border-md); }
  .status-label {
    font-size: 0.6875rem;
    font-weight: 600;
  }
  .status-label.green { color: var(--f-green); }
  .status-label.muted { color: var(--f-text-3); }

  .intg-icon {
    font-size: 1.5rem;
    width: 40px;
    height: 40px;
    border-radius: 10px;
    background: var(--f-bg3);
    display: flex;
    align-items: center;
    justify-content: center;
    margin-bottom: 12px;
  }
  .intg-name {
    font-size: 0.9375rem;
    font-weight: 600;
    margin-bottom: 4px;
    color: var(--f-text);
  }
  .intg-desc {
    font-size: 0.75rem;
    color: var(--f-text-3);
    line-height: 1.5;
  }
  .intg-count {
    font-size: 0.75rem;
    color: var(--f-green);
    font-weight: 500;
    margin-top: 6px;
  }

  .intg-btn {
    width: 100%;
    margin-top: 14px;
    padding: 8px 0;
    border-radius: 7px;
    font-size: 0.8125rem;
    font-weight: 600;
    font-family: var(--f-font-ui);
    cursor: pointer;
    transition: all 0.13s;
    border: 1px solid;
  }
  .btn-connected {
    background: var(--f-green-lt);
    border-color: #b8dfc8;
    color: var(--f-green);
  }
  .btn-disconnected {
    background: var(--f-bg3);
    border-color: var(--f-border-md);
    color: var(--f-text-2);
  }
  .btn-disconnected:hover {
    background: var(--f-accent-lt);
    border-color: var(--f-accent-md);
    color: var(--f-accent);
  }
</style>
