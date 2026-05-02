<script>
  import 'carbon-components-svelte/css/g10.css';
  import '../app.css';

  import {
    Header,
    HeaderNav,
    HeaderAction,
    HeaderUtilities,
    SkipToContent,
    SideNav,
    SideNavItems,
    SideNavLink,
    SideNavDivider,
    SideNavMenu,
    Content
  } from 'carbon-components-svelte';

  import {
    Search,
    Home,
    Chat,
    Document,
    Connect,
    Settings,
    Notification,
    UserAvatar
  } from 'carbon-icons-svelte';

  import { page } from '$app/stores';
  import { goto } from '$app/navigation';

  let isSideNavOpen = false;

  // Map routes to nav ids
  const routeId = (path) => {
    if (path === '/') return 'home';
    return path.replace('/', '');
  };

  $: currentRoute = routeId($page.url.pathname);
</script>

<Header
  company="Filosophy"
  platformName=""
  bind:isSideNavOpen
>
  <svelte:fragment slot="skip-to-content">
    <SkipToContent />
  </svelte:fragment>

  <HeaderUtilities>
    <HeaderAction>
      <svelte:fragment slot="icon">
        <Notification size={20} />
      </svelte:fragment>
    </HeaderAction>
    <HeaderAction>
      <svelte:fragment slot="icon">
        <UserAvatar size={20} />
      </svelte:fragment>
    </HeaderAction>
  </HeaderUtilities>
</Header>

<SideNav bind:isOpen={isSideNavOpen} rail>
  <SideNavItems>
    <SideNavLink
      icon={Home}
      text="Home"
      href="/"
      isSelected={currentRoute === 'home'}
      on:click={() => goto('/')}
    />
    <SideNavLink
      icon={Search}
      text="Search"
      href="/search"
      isSelected={currentRoute === 'search'}
      on:click={() => goto('/search')}
    />
    <SideNavLink
      icon={Chat}
      text="Ask AI"
      href="/chat"
      isSelected={currentRoute === 'chat'}
      on:click={() => goto('/chat')}
    />
    <SideNavDivider />
    <SideNavLink
      icon={Document}
      text="Documents"
      href="/docs"
      isSelected={currentRoute === 'docs'}
      on:click={() => goto('/docs')}
    />
    <SideNavLink
      icon={Connect}
      text="Connections"
      href="/connections"
      isSelected={currentRoute === 'connections'}
      on:click={() => goto('/connections')}
    />
    <SideNavDivider />
    <SideNavLink
      icon={Settings}
      text="Settings"
      href="/settings"
      isSelected={currentRoute === 'settings'}
      on:click={() => goto('/settings')}
    />
  </SideNavItems>
</SideNav>

<Content class="f-content">
  <slot />
</Content>

<style>
  :global(.bx--content.f-content) {
    background: var(--f-bg);
    padding: 0;
  }
</style>
