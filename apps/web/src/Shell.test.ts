import { afterEach, describe, expect, it } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import Shell from './Shell.vue';
import { createAppRouter } from './router';
import { createMockEnvironment } from './testing/mock-agent-api';
import { githubWorkspace } from './github/workspace';

let wrapper: VueWrapper | undefined;

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  githubWorkspace.value = '';
  for (const key of ['maatgen.showSystemMessages', 'maatgen.provider', 'maatgen.sidePanel', 'maatgen.sessionStatusFilter']) {
    localStorage.removeItem(key);
  }
});

async function mountShell(initialPath = '/') {
  const router = createAppRouter();
  await router.push(initialPath);
  await router.isReady();
  const environment = createMockEnvironment();
  wrapper = mount(Shell, {
    global: { plugins: [router], provide: { agentApi: environment.agentApi, eventStreamFactory: environment.eventStreamFactory } },
  });
  await flushPromises();
  return { wrapper, router };
}

describe('Shell', () => {
  it('renders the existing Session view at "/"', async () => {
    const { wrapper } = await mountShell('/');
    expect(wrapper.find('.app-shell').exists()).toBe(true);
    expect(wrapper.find('a.shell-nav-link.active').text()).toBe('Session');
  });

  it('navigates to the GitHub event history view and highlights it', async () => {
    // RouterLink's click-interception is vue-router's own tested behavior;
    // pushing the route directly isolates what this test actually owns:
    // Shell's reactive nav highlighting and view swap in response to a
    // route change.
    const { wrapper, router } = await mountShell('/');
    await router.push({ name: 'github-events' });
    await flushPromises();
    expect(wrapper.text()).toContain('イベント履歴');
    expect(wrapper.find('.shell-nav-group.active').exists()).toBe(true);
    expect(wrapper.find('a[href="/github/events"]').classes()).toContain('active');
  });

  it('redirects "/github" to the event history route', async () => {
    const { router } = await mountShell('/github');
    expect(router.currentRoute.value.name).toBe('github-events');
  });

  it('shows a neutral placeholder before any Session has been selected', async () => {
    const { wrapper } = await mountShell('/github/issues');
    expect(wrapper.find('.shell-repository').text()).toBe('セッション未選択');
  });

  it('resolves and displays the GitHub repository once a Session workspace is set (as App.vue does on select)', async () => {
    const { wrapper } = await mountShell('/github/issues');
    githubWorkspace.value = 'C:/demo/current-repository';
    await flushPromises();
    expect(wrapper.find('.shell-repository.resolved').text()).toBe('github.com/octo-demo/example-repo');
  });
});
