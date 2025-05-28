// vitest.config.ts
import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { svelteTesting } from '@testing-library/svelte/vite';
import path from 'path';
import type { mockApi as MockApiType } from './src/mocks/api_mock';

declare global {
  var mockApi: typeof MockApiType;
}
export default defineConfig({
  plugins: [svelte(), svelteTesting()],
  resolve: {
    alias: {
      'grpc-web': path.resolve(__dirname, 'src/mocks/grpc-web.ts'),
    },
  },
  test: {
    environment: 'jsdom', 
    globals: true, 
    setupFiles: './src/setupTests.ts', 
  },
});
