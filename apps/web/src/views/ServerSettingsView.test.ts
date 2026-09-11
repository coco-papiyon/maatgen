import { afterEach, describe, expect, it } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import ServerSettingsView from './ServerSettingsView.vue';
import { MockAgentApi } from '../testing/mock-agent-api';
import { nodes } from '../nodes';

let wrapper: VueWrapper | undefined;

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

async function mountSettings(api = new MockAgentApi()) {
  nodes.value = await api.listNodes();
  wrapper = mount(ServerSettingsView, { global: { provide: { agentApi: api } } });
  await flushPromises();
  return { wrapper, api };
}

describe('ServerSettingsView (ADR-009 upstream connection)', () => {
  it('starts disabled with empty fields', async () => {
    const { wrapper } = await mountSettings();
    expect((wrapper.get('input[type="checkbox"]').element as HTMLInputElement).checked).toBe(false);
    expect(wrapper.text()).toContain('無効');
  });

  it('saves the upstream connection settings and shows the resulting state', async () => {
    const { wrapper, api } = await mountSettings();

    await wrapper.get('input[type="checkbox"]').setValue(true);
    const [hostInput, portInput, nodeIdInput, nodeNameInput] = wrapper.findAll('input[type="text"]');
    await hostInput!.setValue('upper-host');
    await portInput!.setValue('3101');
    await nodeIdInput!.setValue('linux-dev');
    await nodeNameInput!.setValue('Linux dev box');
    await wrapper.get('.github-form-actions button').trigger('click');
    await flushPromises();

    expect(wrapper.text()).toContain('保存しました');
    expect(wrapper.text()).toContain('接続試行中');

    const status = await api.getUpstreamStatus();
    expect(status.config).toEqual({
      enabled: true,
      upstreamUrl: 'ws://upper-host:3101/api/relay/connect',
      nodeId: 'linux-dev',
      nodeName: 'Linux dev box',
      nodeToken: '',
    });
    expect(nodes.value.find((node) => node.id === 'local')?.name).toBe('Local');
    expect(nodes.value.find((node) => node.id === 'upstream')?.name).toBe('Linux dev box');
    expect((await api.listNodes()).filter((node) => node.status === 'pending')).toHaveLength(0);
  });

  it('does not add the same Node Name more than once', async () => {
    const { wrapper, api } = await mountSettings();
    const nodeNameInput = wrapper.findAll('input[type="text"]')[3]!;
    await nodeNameInput.setValue('Linux dev box');
    await wrapper.get('.github-form-actions button').trigger('click');
    await flushPromises();
    await wrapper.get('.github-form-actions button').trigger('click');
    await flushPromises();

    expect(nodes.value.filter((node) => node.id === 'upstream' && node.name === 'Linux dev box')).toHaveLength(1);
    expect((await api.listNodes()).filter((node) => node.status === 'pending')).toHaveLength(0);
  });

  it('disables the save button when enabled without the required fields', async () => {
    const { wrapper } = await mountSettings();
    await wrapper.get('input[type="checkbox"]').setValue(true);
    expect((wrapper.get('.github-form-actions button').element as HTMLButtonElement).disabled).toBe(true);
  });
});
