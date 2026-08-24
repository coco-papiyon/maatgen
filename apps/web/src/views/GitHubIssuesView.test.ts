import { afterEach, describe, expect, it } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createRouter, createMemoryHistory } from 'vue-router';
import GitHubIssuesView from './GitHubIssuesView.vue';
import { MockAgentApi } from '../testing/mock-agent-api';
import { githubWorkspace } from '../github/workspace';

let wrapper: VueWrapper | undefined;

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  githubWorkspace.value = '';
});

async function mountIssues(workspace = 'C:/demo/current-repository', api = new MockAgentApi()) {
  githubWorkspace.value = workspace;
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: GitHubIssuesView },
      { path: '/github/issues/:number', component: { template: '<div />' } },
    ],
  });
  wrapper = mount(GitHubIssuesView, { global: { plugins: [router], provide: { agentApi: api } } });
  await flushPromises();
  return { wrapper, api };
}

describe('GitHubIssuesView', () => {
  it('lists issues fetched fresh from GitHub, including a Status column sourced from Project data', async () => {
    const { wrapper } = await mountIssues();
    expect(wrapper.text()).toContain('ログイン画面のバリデーションを強化する');
    expect(wrapper.text()).toContain('Ready');
  });

  it('shows a per-item notice when Project data could not be fetched', async () => {
    const { wrapper } = await mountIssues();
    expect(wrapper.text()).toContain('（取得不可）');
  });

  it('re-fetches when a filter is applied', async () => {
    const { wrapper, api } = await mountIssues();
    let lastQuery: unknown;
    const original = api.listGitHubIssues.bind(api);
    api.listGitHubIssues = (workspace, query) => {
      lastQuery = query;
      return original(workspace, query);
    };
    await wrapper.get('.github-filter-bar input[placeholder="login"]').setValue('bob');
    await wrapper.get('.github-filter-bar').trigger('submit');
    await flushPromises();
    expect(lastQuery).toMatchObject({ assignee: 'bob' });
  });
});
