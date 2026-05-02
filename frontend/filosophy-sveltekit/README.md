# Filosophy — SvelteKit Frontend

Enterprise document search + AI assistant. Built with SvelteKit, Vite, and Carbon Components Svelte.

---

## Stack

| Layer | Library |
|---|---|
| Framework | SvelteKit 2 + Vite 5 |
| UI components | `carbon-components-svelte` (IBM Carbon, g10 theme) |
| Icons | `carbon-icons-svelte` |
| Fonts | IBM Plex Sans + IBM Plex Serif (loaded via Carbon CSS) |

---

## Project structure

```
src/
├── app.html                  # HTML shell
├── app.css                   # Global styles + Carbon overrides
└── routes/
    ├── +layout.svelte        # Shell: Header, SideNav, Content wrapper
    ├── +page.svelte          # Home — greeting + search + recent docs
    ├── search/
    │   └── +page.svelte      # Search results (reads ?q= param)
    ├── chat/
    │   └── +page.svelte      # AI Chat ("Ask AI") page
    ├── docs/
    │   └── +page.svelte      # Documents library with filter
    ├── connections/
    │   └── +page.svelte      # Integration cards (connect / disconnect)
    └── settings/
        └── +page.svelte      # Settings panel
```

---

## Getting started

```bash
# Install dependencies
npm install

# Start dev server
npm run dev

# Build for production
npm run build

# Preview production build
npm run preview
```

---

## Connecting your backend

Each page has clearly marked `// TODO:` sections where mock data should be replaced with real API calls. Key integration points:

### Search (`/search`)
```js
// routes/search/+page.svelte
// Replace allResults with a load function:
export async function load({ url, fetch }) {
  const q = url.searchParams.get('q') ?? '';
  const res = await fetch(`/api/search?q=${encodeURIComponent(q)}`);
  const { results, aiSummary } = await res.json();
  return { results, aiSummary };
}
```

### Chat (`/chat`)
```js
// Replace the setTimeout mock with a real streaming call:
const res = await fetch('/api/chat', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ messages: conversationHistory })
});
// Handle SSE stream or JSON response from your LLM backend
```

### Connections (`/connections`)
```js
// Load connected integrations from your API:
export async function load({ fetch }) {
  const res = await fetch('/api/integrations');
  return { integrations: await res.json() };
}
```

---

## Theming

The app uses the Carbon **g10** (warm light) theme. To switch:

```js
// +layout.svelte — change the CSS import:
import 'carbon-components-svelte/css/white.css'; // pure white
import 'carbon-components-svelte/css/g10.css';   // warm light (default)
import 'carbon-components-svelte/css/g90.css';   // dark
import 'carbon-components-svelte/css/g100.css';  // darkest
```

Custom colour overrides live in `src/app.css` under the `:root` block — change `--f-accent` to update the primary blue throughout the entire app.

---

## Notes

- All mock data is co-located in each page file for easy replacement.
- `SideNav` uses `rail` mode — hovering expands it, collapsed by default on small screens.
- The search bar on the home page navigates to `/search?q=...` so back-button works correctly.
- Chat uses a simple `messages` array — wire `conversationHistory` to your LLM API to make it stateful.
