import { writable } from 'svelte/store';
import { EventsOn } from '../lib/wailsjs/runtime/runtime';

export const indexingStatus = writable({
    isIndexing: false,
    progress: 0,
    statusMessage: ''
});

// Setup event listeners for Wails
export function initIndexerStore() {
    EventsOn('indexing_progress', (p: number) => {
        console.log('[Indexer Store] Received progress:', p);
        indexingStatus.update(s => ({ ...s, progress: p, isIndexing: true }));
    });

    EventsOn('indexing_status', (msg: string) => {
        console.log('[Indexer Store] Received status:', msg);
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
