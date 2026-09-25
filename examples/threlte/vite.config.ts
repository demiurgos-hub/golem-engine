import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  root: 'web',
  plugins: [svelte({ configFile: '../svelte.config.js' })],
  resolve: { dedupe: ['svelte', 'three', '@threlte/core', 'golem-engine'] },
  server: {
    port: 5173,
    strictPort: true,
    proxy: { '/api': { target: 'http://127.0.0.1:8080', ws: true } },
  },
});
