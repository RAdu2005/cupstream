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
      // WHEP signaling: POST/PATCH/DELETE on /live/whep[/...] are proxied
      // to auth-svc, which in turn forwards them to MediaMTX's WHEP
      // listener. ICE-trickle PATCHes are short-lived here, so no extra
      // timeout is needed.
      '/live': { target: 'http://localhost:9999', changeOrigin: true },
    },
  },
  build: {
    outDir: 'auth-svc/dist',
    emptyOutDir: true,
    sourcemap: false,
    target: 'es2022',
  },
});
