import { writable } from 'svelte/store';
import { browser } from '$app/environment';

export type Theme = 'light' | 'dark';

const getInitialTheme = (): Theme => {
    if (browser) {
        const stored = localStorage.getItem('theme') as Theme | null;
        if (stored) return stored;
        if (window.matchMedia('(prefers-color-scheme: dark)').matches) return 'dark';
    }
    return 'light';
};

export const themeStore = writable<Theme>(getInitialTheme());

export const toggleTheme = () => {
    themeStore.update(current => {
        const newTheme = current === 'dark' ? 'light' : 'dark';
        console.log('Theme toggled to:', newTheme);
        return newTheme;
    });
};
