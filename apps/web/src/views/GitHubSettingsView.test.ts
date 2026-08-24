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
    const rule = rules.find((candidate) => candidate.name === '新しいルール');
    expect(rule).toBeDefined();
    expect(rule?.filters.states).toEqual(['open']);
  });

  it('opens rule creation in a dialog and saves assignee and PR reviewer filters', async () => {
    const { wrapper, api } = await mountSettings();
    await wrapper.get('.github-card-header button').trigger('click');

    expect(wrapper.get('[role="dialog"]').attributes('aria-modal')).toBe('true');
    expect(wrapper.find('input[aria-label="レビューア"]').exists()).toBe(false);

    await wrapper.get('input[value="pull_request"]').setValue(true);
    await wrapper.get('input[aria-label="アサイン"]').setValue('alice, bob');
    await wrapper.get('input[aria-label="レビューア"]').setValue('carol, dave');
    await wrapper.get('.github-rule-form input').setValue('PR担当ルール');
    await wrapper.get('.github-rule-form textarea').setValue('Review {{.Title}}');
    await wrapper.get('.github-rule-form').trigger('submit');
    await flushPromises();

    const rule = (await api.listGitHubTriggerRules(githubWorkspace.value)).find((candidate) => candidate.name === 'PR担当ルール');
    expect(rule?.filters.assignees).toEqual(['alice', 'bob']);
    expect(rule?.filters.reviewers).toEqual(['carol', 'dave']);
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
