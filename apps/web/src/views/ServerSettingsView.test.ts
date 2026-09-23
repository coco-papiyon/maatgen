import { afterEach, describe, expect, it, vi } from 'vitest';
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

function newServerInputs(wrapper: VueWrapper) {
  const section = wrapper.findAll('.github-card').at(-1)!;
  return {
    checkbox: section.get('input[type="checkbox"]'),
    textInputs: section.findAll('input[type="text"]'),
    addButton: section.get('.github-form-actions button'),
  };
}

describe('ServerSettingsView (ADR-009 upstream connections, multi-server)', () => {
  it('saves one retry policy for all servers', async () => {
    const { wrapper, api } = await mountSettings();
    const inputs = wrapper.get('.server-retry-settings').findAll('input[type="number"]');
    await inputs[0]!.setValue('7');
    await inputs[1]!.setValue('4');
    await wrapper.get('.server-retry-settings button').trigger('click');
    await flushPromises();
    expect(await api.getUpstreamRetrySettings()).toEqual({ maxFailures: 4, retryIntervalMinutes: 7 });
    expect(wrapper.get('.server-retry-settings').text()).toContain('保存しました');
  });

  it('starts with no configured servers and an empty add-server form', async () => {
    const { wrapper } = await mountSettings();
    expect(wrapper.findAll('.github-card')).toHaveLength(2);
    const { checkbox } = newServerInputs(wrapper);
    expect((checkbox.element as HTMLInputElement).checked).toBe(true);
  });

  it('adds a new server and lists it with its connecting state', async () => {
    const { wrapper, api } = await mountSettings();

    const { textInputs, addButton } = newServerInputs(wrapper);
    await textInputs[0]!.setValue('upper-host');
    await textInputs[1]!.setValue('3101');
    await textInputs[2]!.setValue('linux-dev');
    await textInputs[3]!.setValue('Linux dev box');
    await addButton.trigger('click');
    await flushPromises();

    expect(wrapper.text()).toContain('接続試行中');
    expect(wrapper.text()).toContain('Linux dev box');

    const upstreams = await api.listUpstreams();
    expect(upstreams).toHaveLength(1);
    expect(upstreams[0]!.config).toMatchObject({
      enabled: true,
      upstreamUrl: 'ws://upper-host:3101/api/relay/connect',
      nodeId: 'linux-dev',
      nodeName: 'Linux dev box',
    });
    expect(nodes.value.find((node) => node.id === 'local')?.name).toBe('Local');
    expect(nodes.value.find((node) => node.name === 'Linux dev box')).toBeDefined();
  });

  it('adding twice creates two independent servers', async () => {
    const { wrapper, api } = await mountSettings();

    const first = newServerInputs(wrapper);
    await first.textInputs[2]!.setValue('node-a');
    await first.textInputs[0]!.setValue('host-a');
    await first.textInputs[1]!.setValue('3101');
    await first.addButton.trigger('click');
    await flushPromises();

    const second = newServerInputs(wrapper);
    await second.textInputs[2]!.setValue('node-b');
    await second.textInputs[0]!.setValue('host-b');
    await second.textInputs[1]!.setValue('3101');
    await second.addButton.trigger('click');
    await flushPromises();

    const upstreams = await api.listUpstreams();
    expect(upstreams.map((status) => status.config.nodeId).sort()).toEqual(['node-a', 'node-b']);
    expect(wrapper.findAll('.github-card')).toHaveLength(4); // two servers, common settings, and add-server form
  });

  it('disables the add button when enabled without the required fields', async () => {
    const { wrapper } = await mountSettings();
    const { addButton } = newServerInputs(wrapper);
    expect((addButton.element as HTMLButtonElement).disabled).toBe(true);
  });

  it('deletes a configured server', async () => {
    const { wrapper, api } = await mountSettings();

    const { textInputs, addButton } = newServerInputs(wrapper);
    await textInputs[0]!.setValue('upper-host');
    await textInputs[1]!.setValue('3101');
    await textInputs[2]!.setValue('linux-dev');
    await addButton.trigger('click');
    await flushPromises();

    expect((await api.listUpstreams())).toHaveLength(1);

    await wrapper.get('.github-danger').trigger('click');
    await flushPromises();

    expect((await api.listUpstreams())).toHaveLength(0);
    expect(wrapper.findAll('.github-card')).toHaveLength(2); // common settings and add-server form remain
  });

  it('reconnects a server after its failure limit stops automatic attempts', async () => {
    const api = new MockAgentApi();
    await api.updateUpstreamRetrySettings({ maxFailures: 2, retryIntervalMinutes: 5 });
    const configured = await api.createUpstream({ enabled: true, upstreamUrl: 'ws://upper:3101/api/relay/connect', nodeId: 'n1', nodeName: 'Upper', nodeToken: '' });
    const stopped = { ...configured, state: 'stopped' as const, failureCount: 2, lastError: 'connection refused' };
    const connecting = { ...configured, state: 'connecting' as const, failureCount: 0 };
    vi.spyOn(api, 'listUpstreams').mockResolvedValueOnce([stopped]).mockResolvedValueOnce([stopped]).mockResolvedValue([connecting]);
    const reconnect = vi.spyOn(api, 'reconnectUpstream').mockResolvedValue(connecting);
    const { wrapper } = await mountSettings(api);
    expect(wrapper.text()).toContain('接続停止');
    expect(wrapper.text()).toContain('2 / 2');
    expect(nodes.value.some((node) => node.name === 'Upper')).toBe(false);
    await wrapper.get('.server-settings-card button:nth-child(2)').trigger('click');
    await flushPromises();
    expect(reconnect).toHaveBeenCalledWith(configured.config.id);
    expect(wrapper.text()).toContain('接続試行中');
    expect(nodes.value.some((node) => node.name === 'Upper')).toBe(true);
  });
});
