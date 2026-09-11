import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import App from './App.vue';
import { createMockEnvironment } from './testing/mock-agent-api';

let wrapper: VueWrapper | undefined;

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  localStorage.removeItem('maatgen.provider');
  localStorage.removeItem('maatgen.workspaceHistory');
  window.history.replaceState(window.history.state, '', '/');
  vi.restoreAllMocks();
});

async function mountApp() {
  const environment = createMockEnvironment();
  wrapper = mount(App, { props: environment });
  await flushPromises();
  return wrapper;
}

// ADR-009: the node selector always shows at least "Local" (the upper
// node's own instance, id "local"), and offers to add a node even before
// any lower node has ever connected.
describe('Node relay (ADR-009)', () => {
  it('shows Local as the default node and offers to add another', async () => {
    const app = await mountApp();

    expect(app.find('.node-selector-toggle').text()).toContain('Local');
    expect(app.find('.node-selector-list').exists()).toBe(false);

    await app.find('.node-selector-toggle').trigger('click');

    const list = app.find('.node-selector-list');
    expect(list.exists()).toBe(true);
    expect(list.text()).toContain('Local');
    expect(list.find('.node-option-add').text()).toBe('＋ ノードを追加');
  });

  it('creates a node, shows a copyable startup command, and lists it as pending', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const app = await mountApp();

    await app.find('.node-selector-toggle').trigger('click');
    await app.find('.node-option-add').trigger('click');

    expect(app.find('.add-node-modal').exists()).toBe(true);
    await app.find('.add-node-name-field input').setValue('Linux dev box');
    await app.findAll('.add-node-actions button').find((b) => b.text() === '追加')!.trigger('click');
    await flushPromises();

    const commandRow = app.find('.add-node-command');
    expect(commandRow.exists()).toBe(true);
    expect(commandRow.text()).toContain('--node-name "Linux dev box"');
    expect(commandRow.text()).toContain('agent-manager --upstream-url');

    await app.find('.add-node-command-row button').trigger('click');
    expect(writeText).toHaveBeenCalledWith(commandRow.text());

    await app.find('.usage-summary-modal-header .icon-button').trigger('click'); // close the dialog
    await app.find('.node-selector-toggle').trigger('click');
    const pendingOption = app.findAll('.node-option').find((option) => option.text().includes('Linux dev box'))!;
    expect(pendingOption.text()).toContain('登録待ち');
  });

  it('lets a pending node be removed from the selector history', async () => {
    const app = await mountApp();

    await app.find('.node-selector-toggle').trigger('click');
    await app.find('.node-option-add').trigger('click');
    await app.find('.add-node-name-field input').setValue('Linux dev box');
    await app.findAll('.add-node-actions button').find((b) => b.text() === '追加')!.trigger('click');
    await flushPromises();
    await app.find('.usage-summary-modal-header .icon-button').trigger('click');
    await app.find('.node-selector-toggle').trigger('click');

    expect(app.findAll('.node-option').some((option) => option.text().includes('Linux dev box'))).toBe(true);
    await app.find('.node-option-delete').trigger('click');
    await flushPromises();

    expect(app.findAll('.node-option').some((option) => option.text().includes('Linux dev box'))).toBe(false);
  });
});
