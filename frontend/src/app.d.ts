// See https://svelte.dev/docs/kit/types#app.d.ts
// for information about these interfaces
declare global {
	namespace App {
		// interface Error {}
		// interface Locals {}
		// interface PageData {}
		// interface PageState {}
		// interface Platform {}
	}

	// Wails v2 injects window.runtime at startup
	interface WailsRuntime {
		EventsOn(eventName: string, callback: (...data: any[]) => void): () => void;
		EventsOnce(eventName: string, callback: (...data: any[]) => void): () => void;
		EventsOff(eventName: string, ...additionalEventNames: string[]): void;
		EventsOffAll(): void;
		EventsEmit(eventName: string, ...data: any[]): void;
	}

	interface Window {
		runtime: WailsRuntime;
	}

	// Custom DOM events used by Svelte actions
	namespace svelteHTML {
		interface HTMLAttributes<T> {
			'on:click_outside'?: (event: CustomEvent<HTMLElement>) => void;
		}
	}
}

export {};
