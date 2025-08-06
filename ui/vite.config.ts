import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { svelteTesting } from '@testing-library/svelte/vite';
import path from 'path';
import fs from 'fs';

// https://vite.dev/config/
export default defineConfig({
  plugins: [svelte(), svelteTesting()],
  
  resolve: {
    alias: {
      'grpc-web': path.resolve(__dirname, 'src/mocks/grpc-web.ts'),
    },
  },

  server: {
    https: {
      key: fs.readFileSync(path.resolve(__dirname, '../certs/privkey.pem')),
      cert: fs.readFileSync(path.resolve(__dirname, '../certs/fullchain.pem')),
    },
    host: 'alpha2.gmt.bio',
    port: 5173,
  }
});
