import { defineConfig } from 'vitest/config';
import vue from '@vitejs/plugin-vue';

const managerTarget = 'http://127.0.0.1:3100';

export default defineConfig({
  plugins: [vue()],
  // Keep Vite/Vitest temporary files outside the dependency tree. This also
  // makes tests work in read-only or managed node_modules installations.
  cacheDir: '../../.vite-cache/web',
  test: {
    environment: 'happy-dom',
  },
  server: {
    port: 5173,
    strictPort: true,
    // Keep the running UI fixed until the Vite process is restarted.
    hmr: false,
    watch: null,
    proxy: {
      '/api': {
        target: managerTarget,
      },
      '/ws': {
        target: managerTarget,
        ws: true,
      },
    },
  },
});
