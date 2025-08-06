import { writable } from 'svelte/store';

function initializeTheme() {
  if (typeof window === 'undefined') return 'light';
  
  const savedTheme = localStorage.getItem('theme');
  const systemPrefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;

  return savedTheme || (systemPrefersDark ? 'dark' : 'light');
}

export const theme = writable(initializeTheme());

theme.subscribe(currentTheme => {
  if (typeof window !== 'undefined') {
    localStorage.setItem('theme', currentTheme);
    document.documentElement.setAttribute('data-theme', currentTheme);
  }
});