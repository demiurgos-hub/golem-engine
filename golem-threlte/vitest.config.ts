import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { playwright } from '@vitest/browser-playwright';
export default defineConfig({
  plugins: [svelte()],
  resolve: { dedupe: ['svelte', 'three', '@threlte/core', 'golem-engine'] },
  test: {
    include: ['test/**/*.browser.test.ts'],
    maxWorkers: 1,
    browser: {
      enabled: true,
      headless: true,
      provider: playwright({ launchOptions: process.env.PLAYWRIGHT_CHANNEL ? { channel: process.env.PLAYWRIGHT_CHANNEL } : {} }),
      instances: [{ browser: 'chromium' }]
    }
  }
});
