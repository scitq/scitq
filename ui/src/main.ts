import { mount } from 'svelte';
import './app.css';
import './styles/worker.css';
import App from './App.svelte';

// Mount the main App component into the element with the id 'app'
const app = mount(App, {
  target: document.getElementById('app')!,
});

export default app;