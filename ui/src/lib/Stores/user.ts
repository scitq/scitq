import { writable } from 'svelte/store';

export const isLoggedIn = writable(false);
export const userInfo = writable<{ token: string | null }>({ token: null });