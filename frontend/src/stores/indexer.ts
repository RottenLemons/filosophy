import { writable } from 'svelte/store';
import { EventsOn } from '../lib/wailsjs/runtime/runtime';
import { GetIndexingStatus } from '../lib/wailsjs/go/main/App';

export const indexingStatus = writable({
    isIndexing: false,
    progress: 0,
    statusMessage: 'System Ready'
});

// Setup event listeners for Wails
export async function initIndexerStore() {
    try {
        // 1. Fetch the absolute current state from the Go backend
        const currentState = await GetIndexingStatus();
        console.log('[Indexer Store] Initial state:', currentState);
        
        indexingStatus.set({
            isIndexing: currentState.isIndexing,
            statusMessage: currentState.statusMessage || 'System Ready',
            progress: currentState.progress
        });
    } catch (err) {
        console.warn("[Indexer Store] Failed to fetch initial indexing status:", err);
    }

    // 2. Listen for ongoing changes emitted by a.setStatus()
    EventsOn("indexing_status", (status: any) => {
        console.log('[Indexer Store] Received status object:', status);
        
        if (status.statusMessage === 'Indexing complete.') {
            // Keep the 'Indexing complete.' message visible for 3 seconds
            indexingStatus.set({
                isIndexing: true,
                statusMessage: status.statusMessage,
                progress: 100
            });
            setTimeout(() => {
                indexingStatus.set({
                    isIndexing: false,
                    statusMessage: 'System Ready',
                    progress: 0
                });
            }, 3000);
        } else {
            indexingStatus.set({
                isIndexing: status.isIndexing,
                statusMessage: status.statusMessage,
                progress: status.progress
            });
        }
    });
}
