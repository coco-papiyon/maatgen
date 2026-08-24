import { inject } from 'vue';
import { httpAgentApi, type AgentApi } from '../api';

// Mirrors App.vue's own `props.agentApi ?? inject('agentApi', httpAgentApi)`
// resolution: the GitHub monitoring views are mounted by vue-router, which
// can't pass component props, so main.ts provides the mock (or real)
// AgentApi at the app level instead (see main.ts).
export function useAgentApi(): AgentApi {
  return inject<AgentApi>('agentApi', httpAgentApi);
}
