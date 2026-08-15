import { createApp } from 'vue';
import App from './App.vue';
import './styles.css';

async function bootstrap() {
  if (import.meta.env.MODE === 'mock') {
    const { createMockEnvironment } = await import('./testing/mock-agent-api');
    createApp(App, createMockEnvironment()).mount('#app');
    return;
  }
  createApp(App).mount('#app');
}

void bootstrap();
