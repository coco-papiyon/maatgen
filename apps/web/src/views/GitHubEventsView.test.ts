import { afterEach, describe, expect, it } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createRouter, createMemoryHistory } from 'vue-router';
import GitHubEventsView from './GitHubEventsView.vue';
import { MockAgentApi } from '../testing/mock-agent-api';
import { selectedRepository } from '../github/repositories';

let wrapper: VueWrapper | undefined;

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  selectedRepository.value = '';
});

async function mountEvents(workspace = 'C:/demo/current-repository', api = new MockAgentApi()) {
  selectedRepository.value = workspace;
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: { template: '<div />' } }],
  });
  wrapper = mount(GitHubEventsView, { global: { plugins: [router], provide: { agentApi: api } } });
  await flushPromises();
  return { wrapper, api };
}

describe('GitHubEventsView', () => {
  it("shows each event's rule priority", async () => {
    const { wrapper } = await mountEvents();
    const rows = wrapper.findAll('.github-table tbody tr');
    expect(rows.some((row) => row.text().includes('中'))).toBe(true);
    expect(rows.some((row) => row.text().includes('高'))).toBe(true);
  });
});
