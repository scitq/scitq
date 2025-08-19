import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { svelteTesting } from '@testing-library/svelte/vite';
import path from 'path';
import fs from 'fs';

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // Optional dev HTTPS (for npm run dev only)
  const keyPath  = process.env.VITE_DEV_TLS_KEY;
  const certPath = process.env.VITE_DEV_TLS_CERT;
  const useHttps = !!(keyPath && certPath && fs.existsSync(keyPath) && fs.existsSync(certPath));

  // Optional host/port for dev
  const host = process.env.VITE_DEV_HOST || '127.0.0.1';
  const port = Number(process.env.VITE_DEV_PORT || 5173);

  // Only alias grpc-web to a mock in test/dev if you really need that.
  const aliases: Record<string, string> = {
    '@': path.resolve(__dirname, 'src'),
  };
  if (mode !== 'production') {
    // If you still need the mock in dev/testing, keep this line.
    // Otherwise remove it entirely to use the real 'grpc-web' package.
    aliases['grpc-web'] = path.resolve(__dirname, 'src/mocks/grpc-web.ts');
  }

  return {
    plugins: [svelte(), svelteTesting()],
    resolve: { alias: aliases },
    server: {
      https: useHttps
        ? {
            key: fs.readFileSync(keyPath!, 'utf8'),
            cert: fs.readFileSync(certPath!, 'utf8'),
          }
        : undefined,
      host,
      port,
    },
  };
});
