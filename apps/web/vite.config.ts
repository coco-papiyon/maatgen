import { defineConfig } from 'vitest/config';
import vue from '@vitejs/plugin-vue';

const managerTarget = 'http://127.0.0.1:3100';
const authorization = 'Bearer maatgen-local-development-token';

export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'happy-dom',
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': {
        target: managerTarget,
        headers: { Authorization: authorization },
      },
      '/ws': {
        target: managerTarget,
        ws: true,
      },
    },
  },
});
