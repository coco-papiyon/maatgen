import { afterEach, describe, expect, it } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import ServerSettingsView from './ServerSettingsView.vue';
import { MockAgentApi } from '../testing/mock-agent-api';

let wrapper: VueWrapper | undefined;

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

async function mountSettings(api = new MockAgentApi()) {
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
  });

  it('disables the save button when enabled without the required fields', async () => {
    const { wrapper } = await mountSettings();
    await wrapper.get('input[type="checkbox"]').setValue(true);
    expect((wrapper.get('.github-form-actions button').element as HTMLButtonElement).disabled).toBe(true);
  });
});
