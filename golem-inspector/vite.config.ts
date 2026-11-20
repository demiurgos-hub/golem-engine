import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

const hub = process.env.GOLEM_INSPECTOR_DEV_HUB ?? 'http://127.0.0.1:8787';
const outDir = fileURLToPath(new URL('../cmd/golem-inspector/web/dist', import.meta.url));

export default defineConfig({
  plugins: [react()],
  build: {
    // The hub embeds this directory with go:embed.
    outDir,
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': hub,
      '/auth': hub,
      '/live': hub,
      '/ready': hub,
      '/health': hub,
      '/ws': { target: hub.replace(/^http/, 'ws'), ws: true },
    },
  },
  test: {
    include: ['src/**/*.test.ts'],
    environment: 'node',
  },
});
