import { defineConfig } from 'vite';

export default defineConfig({
  server: {
    port: 5173,
    strictPort: true,
    proxy: { '/api/admin': { target: 'http://127.0.0.1:4033', changeOrigin: true } },
  },
  build: { outDir: 'dist', emptyOutDir: true },
});
