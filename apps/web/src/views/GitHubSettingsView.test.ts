import { afterEach, describe, expect, it } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import GitHubSettingsView from './GitHubSettingsView.vue';
import { MockAgentApi } from '../testing/mock-agent-api';
import { githubWorkspace } from '../github/workspace';

let wrapper: VueWrapper | undefined;

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  githubWorkspace.value = '';
});

async function mountSettings(workspace = 'C:/demo/current-repository', api = new MockAgentApi()) {
  githubWorkspace.value = workspace;
  wrapper = mount(GitHubSettingsView, { global: { provide: { agentApi: api } } });
  await flushPromises();
  return { wrapper, api };
}

describe('GitHubSettingsView', () => {
  it('prompts to select a Session when no workspace is set', async () => {
    const { wrapper } = await mountSettings('');
    expect(wrapper.text()).toContain('Sessionを選択すると');
  });

  it('shows the resolved repository and existing monitor settings', async () => {
    const { wrapper } = await mountSettings();
    expect(wrapper.text()).toContain('github.com/octo-demo/example-repo');
    expect(wrapper.get('input[aria-label="プロジェクト名"]')).toBeTruthy();
    const pollInput = wrapper.find('.github-form-row input[type="number"]');
    expect((pollInput.element as HTMLInputElement).valueAsNumber).toBe(300);
    expect(wrapper.text()).toContain('Ready になったら設計する');
  });

  it('creates a new trigger rule', async () => {
    const { wrapper, api } = await mountSettings();
    await wrapper.get('.github-card-header button').trigger('click');
    await wrapper.get('.github-rule-form input').setValue('新しいルール');
    await wrapper.get('.github-rule-form textarea').setValue('Design {{.Title}}');
    await wrapper.get('.github-rule-form').trigger('submit');
    await flushPromises();

    const rules = await api.listGitHubTriggerRules(githubWorkspace.value);
    expect(rules.some((rule) => rule.name === '新しいルール')).toBe(true);
  });

  it('deletes the monitor after confirmation', async () => {
    const originalConfirm = window.confirm;
    window.confirm = () => true;
    try {
      const { wrapper, api } = await mountSettings();
      await wrapper.get('.github-danger').trigger('click');
      await flushPromises();
      await expect(api.getGitHubMonitor(githubWorkspace.value)).rejects.toThrow();
    } finally {
      window.confirm = originalConfirm;
    }
  });
});
