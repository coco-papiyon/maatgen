import { createApp } from 'vue';
import Shell from './Shell.vue';
import { createAppRouter } from './router';
import './styles.css';

async function bootstrap() {
  const app = createApp(Shell);
  app.use(createAppRouter());

  if (import.meta.env.MODE === 'mock') {
    const { createMockEnvironment } = await import('./testing/mock-agent-api');
    const environment = createMockEnvironment();
    // App.vue (mounted as the "/" route, see router.ts) and the GitHub
    // monitoring views resolve their AgentApi/EventStreamFactory via
    // inject(), since the router mounts them without props.
    app.provide('agentApi', environment.agentApi);
    app.provide('eventStreamFactory', environment.eventStreamFactory);
  }

  app.mount('#app');
}

void bootstrap();
