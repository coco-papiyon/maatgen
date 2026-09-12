import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it } from 'vitest';
import Shell from './Shell.vue';
import { createAppRouter } from './router';
import { createMockEnvironment, MockAgentApi } from './testing/mock-agent-api';
import { nodes, selectedNodeId } from './nodes';

let wrapper: VueWrapper | undefined;

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  localStorage.removeItem('maatgen.provider');
  localStorage.removeItem('maatgen.workspaceHistory');
  window.history.replaceState(window.history.state, '', '/');
  nodes.value = [];
  selectedNodeId.value = 'local';
});

async function mountApp(api = new MockAgentApi()) {
  const environment = createMockEnvironment(api);
  const router = createAppRouter();
  await router.push('/');
  await router.isReady();
  wrapper = mount(Shell, {
    global: { plugins: [router], provide: { agentApi: environment.agentApi, eventStreamFactory: environment.eventStreamFactory } },
  });
  await flushPromises();
  return wrapper;
}

// ADR-009: the selector is selection-only. Node registration is managed
// outside the top-right selector.
describe('Node relay ', () => {
  it('shows Local as the default node without an add action', async () => {
    const app = await mountApp();

    expect(app.find('.node-selector-toggle').text()).toContain('Local');
    expect(app.find('.node-selector-list').exists()).toBe(false);

    await app.find('.node-selector-toggle').trigger('click');

    const list = app.find('.node-selector-list');
    expect(list.exists()).toBe(true);
    expect(list.text()).toContain('Local');
    expect(list.text()).not.toContain('ノードを追加');
  });

  it('lists an already registered node as pending', async () => {
    const api = new MockAgentApi();
    await api.createNode('Linux dev box');
    const app = await mountApp(api);
    await app.find('.node-selector-toggle').trigger('click');
    const pendingOption = app.findAll('.node-option').find((option) => option.text().includes('Linux dev box'))!;
    expect(pendingOption.text()).toContain('登録待ち');
  });

  it('lets a pending node be removed from the selector history', async () => {
    const api = new MockAgentApi();
    await api.createNode('Linux dev box');
    const app = await mountApp(api);
    await app.find('.node-selector-toggle').trigger('click');

    expect(app.findAll('.node-option').some((option) => option.text().includes('Linux dev box'))).toBe(true);
    await app.find('.node-option-delete').trigger('click');
    await flushPromises();

    expect(app.findAll('.node-option').some((option) => option.text().includes('Linux dev box'))).toBe(false);
  });
});
