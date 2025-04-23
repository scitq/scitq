// src/main.ts
import { mount } from 'svelte';
import './app.css';
import './styles/worker.css';
import App from './App.svelte';

// Monte le composant principal App dans l'élément ayant l'id 'app'
const app = mount(App, {
  target: document.getElementById('app')!,
});

export default app;
