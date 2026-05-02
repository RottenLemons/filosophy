<svelte:head>
  <title>Settings — Filosophy</title>
</svelte:head>

<div class="settings-page">
  <h2 class="settings-title">Settings</h2>
  <p class="settings-sub">Manage your account, preferences, and workspace settings.</p>

  <div class="settings-section">
    <div class="setting-group">
      <h3 class="group-title">Profile</h3>

      <div class="setting-row">
        <div class="setting-info">
          <div class="setting-label">Display name</div>
          <div class="setting-desc">Your name shown in the app and to teammates</div>
        </div>
        <input class="setting-input" type="text" value="Alex Morgan" />
      </div>

      <div class="setting-row">
        <div class="setting-info">
          <div class="setting-label">Email address</div>
          <div class="setting-desc">Used for notifications and account recovery</div>
        </div>
        <input class="setting-input" type="email" value="alex@company.com" />
      </div>
    </div>

    <div class="setting-group">
      <h3 class="group-title">Preferences</h3>

      <div class="setting-row">
        <div class="setting-info">
          <div class="setting-label">Language</div>
          <div class="setting-desc">Interface language</div>
        </div>
        <select class="setting-select">
          <option>English (UK)</option>
          <option>English (US)</option>
          <option>French</option>
          <option>German</option>
        </select>
      </div>

      <div class="setting-row">
        <div class="setting-info">
          <div class="setting-label">AI Summary in search</div>
          <div class="setting-desc">Show an AI-generated summary above search results</div>
        </div>
        <label class="toggle-wrap">
          <input type="checkbox" checked class="toggle-input" />
          <span class="toggle-track"></span>
        </label>
      </div>
    </div>
  </div>
</div>

<style>
  .settings-page {
    padding: 32px 36px;
    max-width: 640px;
  }
  .settings-title {
    font-family: var(--f-font-serif);
    font-size: 1.375rem;
    font-weight: 500;
    letter-spacing: -0.3px;
    margin: 0 0 4px;
  }
  .settings-sub {
    font-size: 0.875rem;
    color: var(--f-text-3);
    margin: 0 0 32px;
  }

  .setting-group {
    background: var(--f-bg2);
    border: 1px solid var(--f-border);
    border-radius: 10px;
    overflow: hidden;
    margin-bottom: 16px;
  }
  .group-title {
    font-size: 0.75rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.07em;
    color: var(--f-text-3);
    padding: 12px 18px;
    border-bottom: 1px solid var(--f-border);
    margin: 0;
  }

  .setting-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 24px;
    padding: 14px 18px;
    border-bottom: 1px solid var(--f-border);
  }
  .setting-row:last-child { border-bottom: none; }

  .setting-info { flex: 1; }
  .setting-label {
    font-size: 0.875rem;
    font-weight: 500;
    color: var(--f-text);
    margin-bottom: 2px;
  }
  .setting-desc {
    font-size: 0.75rem;
    color: var(--f-text-3);
  }

  .setting-input,
  .setting-select {
    background: var(--f-bg3);
    border: 1px solid var(--f-border-md);
    border-radius: 6px;
    padding: 7px 10px;
    font-size: 0.8125rem;
    font-family: var(--f-font-ui);
    color: var(--f-text);
    outline: none;
    min-width: 180px;
    transition: border-color 0.13s;
  }
  .setting-input:focus,
  .setting-select:focus {
    border-color: var(--f-accent);
  }

  /* Toggle */
  .toggle-wrap { display: flex; align-items: center; cursor: pointer; }
  .toggle-input { display: none; }
  .toggle-track {
    width: 40px;
    height: 22px;
    background: var(--f-border-md);
    border-radius: 12px;
    position: relative;
    transition: background 0.2s;
  }
  .toggle-track::after {
    content: '';
    position: absolute;
    width: 16px;
    height: 16px;
    background: #fff;
    border-radius: 50%;
    top: 3px;
    left: 3px;
    transition: transform 0.2s;
    box-shadow: 0 1px 3px rgba(0,0,0,0.2);
  }
  .toggle-input:checked + .toggle-track {
    background: var(--f-accent);
  }
  .toggle-input:checked + .toggle-track::after {
    transform: translateX(18px);
  }
</style>
