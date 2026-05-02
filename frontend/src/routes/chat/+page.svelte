<script lang="ts">
  import Chat from '$lib/components/Chat.svelte';
</script>

<svelte:head>
  <title>Ask AI - Filosophy</title>
</svelte:head>

<div class="f-page chat-page">
  <Chat embedded />
</div>

<style>
  .chat-page {
    max-width: none;
    padding-bottom: 0;
  }
</style>
