/// <reference types="vitest/config" />
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

// The Go server listens on 127.0.0.1:8080 in development, or wherever
// scripts/dev.sh puts it (CUPSTREAM_ADDR). changeOrigin stays false so the
// server sees Host: localhost:5173, which matches the browser's Origin for its
// cross-origin checks.
const server = `http://${process.env.CUPSTREAM_ADDR ?? '127.0.0.1:8080'}`;

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': { target: server, ws: true },
      '/healthz': { target: server },
      '/whip': { target: server },
    },
  },
  build: {
    target: 'es2022',
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
  },
});
