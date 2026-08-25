import { afterEach, describe, expect, it } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import Shell from './Shell.vue';
import { createAppRouter } from './router';
import { createMockEnvironment, MockAgentApi } from './testing/mock-agent-api';
import { githubWorkspace } from './github/workspace';
import { selectedRepository } from './github/repositories';

let wrapper: VueWrapper | undefined;

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  githubWorkspace.value = '';
  selectedRepository.value = '';
  for (const key of ['maatgen.showSystemMessages', 'maatgen.provider', 'maatgen.sidePanel', 'maatgen.sessionStatusFilter']) {
    localStorage.removeItem(key);
  }
});

async function mountShell(initialPath = '/', api: MockAgentApi = new MockAgentApi()) {
  const router = createAppRouter();
  await router.push(initialPath);
  await router.isReady();
  const environment = createMockEnvironment(api);
  wrapper = mount(Shell, {
    global: { plugins: [router], provide: { agentApi: environment.agentApi, eventStreamFactory: environment.eventStreamFactory } },
  });
  await flushPromises();
  return { wrapper, router, api };
}

describe('Shell', () => {
  it('renders the existing Session view at "/"', async () => {
    const { wrapper } = await mountShell('/');
    expect(wrapper.find('.app-shell').exists()).toBe(true);
    expect(wrapper.find('a.shell-nav-link.active').text()).toBe('Session');
  });

  it('navigates to the GitHub Job view and highlights it', async () => {
    // RouterLink's click-interception is vue-router's own tested behavior;
    // pushing the route directly isolates what this test actually owns:
    // Shell's reactive nav highlighting and view swap in response to a
    // route change.
    const { wrapper, router } = await mountShell('/');
    await router.push({ name: 'github-events' });
    await flushPromises();
    expect(wrapper.text()).toContain('Job');
    expect(wrapper.find('a[href="/github/events"]').classes()).toContain('active');
  });

  it('renders the requested top menu order without a GitHub monitoring group', async () => {
    const { wrapper } = await mountShell('/');
    const labels = wrapper.findAll('.shell-nav > .shell-nav-link').map((link) => link.text());
    expect(labels).toEqual(['Session', 'Issue', 'PR', 'Job', '設定']);
    expect(wrapper.find('.shell-nav-group').exists()).toBe(false);
    expect(wrapper.text()).not.toContain('GitHub監視');
  });

  it('redirects "/github" to the event history route', async () => {
    const { router } = await mountShell('/github');
    expect(router.currentRoute.value.name).toBe('github-events');
  });

  it('shows a neutral placeholder when no repository is registered yet', async () => {
    const api = new MockAgentApi();
    await api.deleteGitHubMonitor('C:/demo/current-repository');
    const { wrapper } = await mountShell('/github/issues', api);
    expect(wrapper.find('.shell-repository').text()).toBe('リポジトリ未登録');
  });

  it('lists every registered repository, grouping by remote, and defaults to the first one', async () => {
    const { wrapper } = await mountShell('/github/issues');
    // The option text stays short (owner/name only, host dropped) so the
    // nav bar's other buttons never wrap; the full host/owner/name identity
    // is still the option's value and the select's title.
    const options = wrapper.findAll('select.shell-repository option').map((option) => option.text());
    expect(options).toEqual(['octo-demo/example-repo']);
    const select = wrapper.find('select.shell-repository').element as HTMLSelectElement;
    expect(select.value).toBe('github.com/octo-demo/example-repo');
    expect(select.title).toBe('github.com/octo-demo/example-repo');
  });

  it('resolves and displays the GitHub repository once a Session workspace is set (as App.vue does on select)', async () => {
    const api = new MockAgentApi();
    await api.createGitHubMonitor({ workspace: 'C:/demo/other-repository', pollIntervalSeconds: 300 });
    const { wrapper } = await mountShell('/github/issues', api);
    githubWorkspace.value = 'C:/demo/current-repository';
    await flushPromises();
    const select = wrapper.find('select.shell-repository.resolved').element as HTMLSelectElement;
    expect(select.value).toBe('github.com/octo-demo/example-repo');
  });

  it('registers a new Repository path as a monitored repository when the ＋ button creates a Session', async () => {
    const api = new MockAgentApi();
    const { wrapper } = await mountShell('/', api);
    await wrapper.find('#workspace').setValue('C:/demo/new-repository');
    await wrapper.find('form.new-session').trigger('submit');
    await flushPromises();
    const monitors = await api.listGitHubMonitors();
    expect(monitors.filter((monitor) => monitor.repository === 'C:/demo/new-repository')).toHaveLength(1);
  });

  it('does not register a repository a second time when it is already monitored', async () => {
    const api = new MockAgentApi();
    await api.createGitHubMonitor({ workspace: 'C:/demo/current-repository', pollIntervalSeconds: 300 });
    const { wrapper } = await mountShell('/', api);
    await wrapper.find('#workspace').setValue('C:/demo/current-repository');
    await wrapper.find('form.new-session').trigger('submit');
    await flushPromises();
    const monitors = await api.listGitHubMonitors();
    expect(monitors.filter((monitor) => monitor.repository === 'C:/demo/current-repository')).toHaveLength(1);
  });
});
