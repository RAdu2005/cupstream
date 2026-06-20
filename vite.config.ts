import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'node:path';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 5173,
    host: true,
    proxy: {
      '/api': { target: 'http://localhost:9999', changeOrigin: true, ws: true },
      // LL-HLS uses long-lived blocking playlist requests (30s+). Vite's
      // default 30s proxy timeout cuts them off, so the player never
      // receives segment updates.
      '/live': {
        target: 'http://localhost:9999',
        changeOrigin: true,
        timeout: 120_000,
        proxyTimeout: 120_000,
      },
    },
  },
  build: {
    outDir: 'auth-svc/dist',
    emptyOutDir: true,
    sourcemap: false,
    target: 'es2022',
  },
});
