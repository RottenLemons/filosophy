import { writable } from 'svelte/store';

export const indexingStatus = writable({
    isIndexing: false,
    progress: 0,
    statusMessage: ''
});

// Setup event listeners for Wails
// This will be called from an onMount in a component that's guaranteed to be loaded (like App or SettingsModal)
export function initIndexerStore() {
    const w = (window as any);
    if (w.runtime && w.runtime.EventsOn) {
        w.runtime.EventsOn('indexing_progress', (p: number) => {
            indexingStatus.update(s => ({ ...s, progress: p, isIndexing: true }));
        });

        w.runtime.EventsOn('indexing_status', (msg: string) => {
            indexingStatus.update(s => {
                const newState = { ...s, statusMessage: msg };
                if (msg === 'Indexing complete.') {
                    setTimeout(() => {
                        indexingStatus.set({ isIndexing: false, progress: 0, statusMessage: '' });
                    }, 3000);
                } else if (msg) {
                    newState.isIndexing = true;
                }
                return newState;
            });
        });
    }
}
