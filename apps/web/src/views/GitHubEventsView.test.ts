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

async function mountEvents(api = new MockAgentApi()) {
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

  it("shows each event's repository and rule provider, independent of the top-right repository selector", async () => {
    selectedRepository.value = 'C:/some/other-repository-not-monitored';
    const { wrapper } = await mountEvents();
    const rows = wrapper.findAll('.github-table tbody tr');
    // Events keep showing even though selectedRepository points elsewhere:
    // the Job view no longer filters by the top-right repository selector.
    expect(rows.length).toBeGreaterThan(0);
    expect(rows.some((row) => row.text().includes('octo-demo/example-repo'))).toBe(true);
    expect(rows.some((row) => row.text().includes('Codex'))).toBe(true);
    expect(rows.some((row) => row.text().includes('Claude Code'))).toBe(true);
  });

  it('renders the リポジトリ column right after 状態 and the プロバイダー column right after ルール', async () => {
    const { wrapper } = await mountEvents();
    const headers = wrapper.findAll('.github-table thead th').map((header) => header.text());
    expect(headers.indexOf('リポジトリ')).toBe(headers.indexOf('状態') + 1);
    expect(headers.indexOf('プロバイダー')).toBe(headers.indexOf('ルール') + 1);
  });
});
