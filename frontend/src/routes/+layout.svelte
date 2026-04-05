<script>
  import '../app.css';
  import { themeStore } from '../stores/theme';

  $: {
    if (typeof window !== 'undefined') {
      const isDark = $themeStore === 'dark';
      console.log('Layout reactivity: setting dark class to', isDark);
      if (isDark) {
        document.documentElement.classList.add('dark');
      } else {
        document.documentElement.classList.remove('dark');
      }
      localStorage.setItem('theme', $themeStore);
    }
  }
</script>

<slot />