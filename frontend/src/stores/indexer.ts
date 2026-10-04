import { writable } from 'svelte/store';
import { EventsOn } from '../lib/wailsjs/runtime/runtime';
import { GetIndexingStatus } from '../lib/wailsjs/go/main/App';

export interface IndexingState {
    isIndexing: boolean;
    progress: number;
    statusMessage: string;
    searchReady: boolean;
    enhancedReady: boolean;
}

export const indexingStatus = writable<IndexingState>({
    isIndexing: true,
    progress: 0,
    statusMessage: 'Starting search engine...',
    searchReady: false,
    enhancedReady: false
});

// Setup event listeners for Wails
export async function initIndexerStore() {
    try {
        // 1. Fetch the absolute current state from the Go backend
        const currentState = await GetIndexingStatus();
        indexingStatus.set({
            isIndexing: currentState.isIndexing,
            statusMessage: currentState.statusMessage || 'Search status unavailable',
            progress: currentState.progress || 0,
            searchReady: currentState.searchReady || false,
            enhancedReady: currentState.enhancedReady || false
        });
    } catch (err) {
        console.warn("[Indexer Store] Failed to fetch initial indexing status:", err);
        indexingStatus.set({
            isIndexing: false,
            statusMessage: 'Search engine unavailable',
            progress: 0,
            searchReady: false,
            enhancedReady: false
        });
    }

    // 2. Listen for ongoing changes emitted by a.setStatus()
    EventsOn("indexing_status", (status: any) => {
        indexingStatus.set({
            isIndexing: status.isIndexing,
            statusMessage: status.statusMessage || 'Search status unavailable',
            progress: status.progress || 0,
            searchReady: status.searchReady || false,
            enhancedReady: status.enhancedReady || false
        });
    });
}
