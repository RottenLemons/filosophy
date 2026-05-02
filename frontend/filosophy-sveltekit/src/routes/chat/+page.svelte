<script>
  import { onMount, tick } from 'svelte';
  import { TextArea, Button, Tag } from 'carbon-components-svelte';
  import { SendFilled, Idea } from 'carbon-icons-svelte';

  let messages = [
    {
      id: 1,
      role: 'ai',
      text: "Hi! I'm Filo, your AI assistant. I can search across all your connected apps and answer questions about your documents. What would you like to know?",
      sources: []
    },
    {
      id: 2,
      role: 'user',
      text: "What were the main decisions from last month's product reviews?"
    },
    {
      id: 3,
      role: 'ai',
      text: "Based on your connected sources, here are the key decisions from last month's product reviews:\n\n1. The team agreed to push the mobile search feature to Q2 and focus on onboarding improvements first.\n\n2. After reviewing customer feedback in Notion, you moved to a per-seat pricing model.\n\n3. Confluence and Gmail were approved as priority integrations for Q1.\n\nWould you like more detail on any of these?",
      sources: ['Notion', 'Slack #product', 'Drive']
    }
  ];

  let inputValue = '';
  let isTyping = false;
  let messagesEl;

  async function scrollToBottom() {
    await tick();
    if (messagesEl) {
      messagesEl.scrollTop = messagesEl.scrollHeight;
    }
  }

  async function sendMessage() {
    const text = inputValue.trim();
    if (!text) return;

    messages = [...messages, { id: Date.now(), role: 'user', text, sources: [] }];
    inputValue = '';
    isTyping = true;
    await scrollToBottom();

    // Simulate AI response
    setTimeout(async () => {
      isTyping = false;
      messages = [
        ...messages,
        {
          id: Date.now() + 1,
          role: 'ai',
          text: "I searched across your connected apps. The most relevant context comes from your Notion workspace and recent Slack discussions. Would you like me to dig deeper into a specific source?",
          sources: ['Notion', 'Slack', 'Drive']
        }
      ];
      await scrollToBottom();
    }, 1800);
  }

  function handleKeydown(e) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  }

  onMount(scrollToBottom);
</script>

<svelte:head>
  <title>Ask AI — Filosophy</title>
</svelte:head>

<div class="chat-page">
  <div class="chat-header f-fade-in">
    <h2 class="chat-title">Ask AI</h2>
    <span class="model-pill">✦ Filo</span>
  </div>

  <div class="messages" bind:this={messagesEl}>
    {#each messages as msg, i}
      <div class="msg {msg.role}" style="animation-delay:{i * 0.04}s">
        <div class="msg-av {msg.role === 'ai' ? 'ai-av' : 'user-av'}">
          {msg.role === 'ai' ? '✦' : 'A'}
        </div>
        <div class="msg-bubble {msg.role === 'ai' ? 'ai-bubble' : 'user-bubble'}">
          {#each msg.text.split('\n') as line, j}
            {line}{#if j < msg.text.split('\n').length - 1}<br />{/if}
          {/each}
          {#if msg.sources && msg.sources.length > 0}
            <div class="msg-sources">
              {#each msg.sources as src}
                <span class="src-chip">{src}</span>
              {/each}
            </div>
          {/if}
        </div>
      </div>
    {/each}

    {#if isTyping}
      <div class="msg ai">
        <div class="msg-av ai-av">✦</div>
        <div class="msg-bubble ai-bubble">
          <div class="typing">
            <span class="dot"></span>
            <span class="dot"></span>
            <span class="dot"></span>
          </div>
        </div>
      </div>
    {/if}
  </div>

  <div class="input-area f-fade-in">
    <div class="input-wrap">
      <textarea
        class="chat-input"
        rows="1"
        placeholder="Ask anything about your documents…"
        bind:value={inputValue}
        on:keydown={handleKeydown}
      ></textarea>
      <button class="send-btn" on:click={sendMessage} aria-label="Send">
        <SendFilled size={16} />
      </button>
    </div>
    <p class="input-hint">Press Enter to send · Shift+Enter for new line</p>
  </div>
</div>

<style>
  .chat-page {
    display: flex;
    flex-direction: column;
    height: calc(100vh - 48px);
    padding: 28px 36px 0;
    max-width: 820px;
  }

  /* Header */
  .chat-header {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 20px;
    flex-shrink: 0;
  }
  .chat-title {
    font-family: var(--f-font-serif);
    font-size: 1.375rem;
    font-weight: 500;
    letter-spacing: -0.3px;
    margin: 0;
  }
  .model-pill {
    background: var(--f-accent-lt);
    border: 1px solid var(--f-accent-md);
    border-radius: 20px;
    padding: 3px 10px;
    font-size: 0.75rem;
    color: var(--f-accent);
    font-weight: 600;
  }

  /* Messages */
  .messages {
    flex: 1;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 16px;
    padding-bottom: 16px;
    padding-right: 4px;
  }
  .msg {
    display: flex;
    gap: 10px;
    animation: f-fade-up 0.35s cubic-bezier(0.4,0,0.2,1) both;
  }
  .msg.user { flex-direction: row-reverse; }

  .msg-av {
    width: 30px;
    height: 30px;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 13px;
    font-weight: 600;
    flex-shrink: 0;
  }
  .ai-av  { background: var(--f-accent); color: #fff; font-size: 12px; }
  .user-av{ background: var(--f-bg4);    color: var(--f-text-2); }

  .msg-bubble {
    max-width: 68%;
    padding: 12px 16px;
    font-size: 0.875rem;
    line-height: 1.7;
    border-radius: 12px;
  }
  .ai-bubble {
    background: var(--f-bg2);
    border: 1px solid var(--f-border);
    border-top-left-radius: 4px;
    color: var(--f-text);
  }
  .user-bubble {
    background: var(--f-accent-lt);
    border: 1px solid var(--f-accent-md);
    border-top-right-radius: 4px;
    color: var(--f-text);
  }

  .msg-sources {
    display: flex;
    gap: 5px;
    flex-wrap: wrap;
    margin-top: 10px;
  }
  .src-chip {
    background: var(--f-bg3);
    border: 1px solid var(--f-border);
    border-radius: 6px;
    padding: 2px 8px;
    font-size: 0.72rem;
    color: var(--f-text-3);
  }

  /* Typing indicator */
  .typing {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 4px 0;
  }
  .dot {
    width: 6px;
    height: 6px;
    background: var(--f-text-3);
    border-radius: 50%;
    animation: f-blink 1.1s ease-in-out infinite;
  }
  .dot:nth-child(2) { animation-delay: 0.18s; }
  .dot:nth-child(3) { animation-delay: 0.36s; }

  /* Input */
  .input-area {
    flex-shrink: 0;
    padding: 12px 0 16px;
    border-top: 1px solid var(--f-border);
    margin-top: 4px;
  }
  .input-wrap {
    position: relative;
  }
  .chat-input {
    width: 100%;
    padding: 12px 48px 12px 16px;
    background: var(--f-bg2);
    border: 1.5px solid var(--f-border-md);
    border-radius: var(--f-radius);
    font-size: 0.875rem;
    font-family: var(--f-font-ui);
    color: var(--f-text);
    outline: none;
    resize: none;
    line-height: 1.5;
    transition: border-color 0.15s, box-shadow 0.15s;
  }
  .chat-input::placeholder { color: var(--f-text-3); }
  .chat-input:focus {
    border-color: var(--f-accent);
    box-shadow: 0 0 0 3px rgba(27,94,142,0.09);
  }
  .send-btn {
    position: absolute;
    right: 10px;
    bottom: 10px;
    width: 30px;
    height: 30px;
    border-radius: 6px;
    background: var(--f-accent);
    color: #fff;
    border: none;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    transition: background 0.13s, transform 0.13s;
  }
  .send-btn:hover {
    background: #1a5280;
    transform: scale(1.06);
  }
  .input-hint {
    font-size: 0.72rem;
    color: var(--f-text-3);
    text-align: right;
    margin: 6px 0 0;
  }
</style>
