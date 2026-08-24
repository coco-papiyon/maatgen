import { afterEach, describe, expect, it } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import GitHubSettingsView from './GitHubSettingsView.vue';
import { MockAgentApi } from '../testing/mock-agent-api';
import { repositories, selectedRepository } from '../github/repositories';

const DEMO_WORKSPACE = 'C:/demo/current-repository';

let wrapper: VueWrapper | undefined;

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  selectedRepository.value = '';
  repositories.value = [];
});

async function mountSettings(api = new MockAgentApi()) {
  wrapper = mount(GitHubSettingsView, { global: { provide: { agentApi: api } } });
  await flushPromises();
  return { wrapper, api };
}

describe('GitHubSettingsView', () => {
  it('lists every registered repository in a table, keyed by local path', async () => {
    const { wrapper } = await mountSettings();
    const row = wrapper.get('.github-table tbody tr');
    expect(row.text()).toContain(DEMO_WORKSPACE);
    expect(row.text()).toContain('github.com/octo-demo/example-repo');
    expect((row.get('input[type="checkbox"]').element as HTMLInputElement).checked).toBe(true);
  });

  it('registers a new repository from a local path', async () => {
    const { wrapper, api } = await mountSettings();
    await wrapper.get('.github-add-repository input').setValue('C:/demo/another-repository');
    await wrapper.get('.github-add-repository form').trigger('submit');
    await flushPromises();

    await wrapper.get('.github-add-repository .github-form-actions button').trigger('click');
    await flushPromises();

    const monitors = await api.listGitHubMonitors();
    expect(monitors.some((monitor) => monitor.repository === 'C:/demo/another-repository')).toBe(true);
    expect(wrapper.findAll('.github-table tbody tr')).toHaveLength(2);
  });

  it('applies the common poll interval and coalesce limit to every registered repository', async () => {
    const api = new MockAgentApi();
    await api.createGitHubMonitor({ workspace: 'C:/demo/other-repository', pollIntervalSeconds: 600, coalesceQueueLimit: 5 });
    const { wrapper } = await mountSettings(api);

    const numberInputs = wrapper.findAll('.github-card .github-form-row input[type="number"]');
    await numberInputs[0]!.setValue(120);
    await numberInputs[1]!.setValue(10);
    await wrapper.get('.github-card .github-form-actions button').trigger('click');
    await flushPromises();

    const monitors = await api.listGitHubMonitors();
    expect(monitors).toHaveLength(2);
    expect(monitors.every((monitor) => monitor.pollIntervalSeconds === 120 && monitor.coalesceQueueLimit === 10)).toBe(true);
  });

  it("does not expose a way to change an existing repository's remote (it always follows the local Git remote)", async () => {
    const { wrapper } = await mountSettings();
    expect(wrapper.find('.github-table select').exists()).toBe(false);
  });

  it('toggles a repository between enabled and disabled', async () => {
    const { wrapper, api } = await mountSettings();
    await wrapper.get('.github-table input[type="checkbox"]').setValue(false);
    await flushPromises();

    const monitor = await api.getGitHubMonitor(DEMO_WORKSPACE);
    expect(monitor.enabled).toBe(false);
  });

  it('creates a new trigger rule with a local-path selector', async () => {
    const { wrapper, api } = await mountSettings();
    await wrapper.get('.github-card-header button').trigger('click');
    await wrapper.get('.github-rule-form select').setValue(DEMO_WORKSPACE);
    await wrapper.get('.github-rule-form input:not([disabled])').setValue('新しいルール');
    await wrapper.get('.github-rule-form textarea').setValue('Design {{.Title}}');
    await wrapper.get('.github-rule-form').trigger('submit');
    await flushPromises();

    const rules = await api.listGitHubTriggerRules(DEMO_WORKSPACE);
    expect(rules.some((rule) => rule.name === '新しいルール')).toBe(true);
  });

  it('opens rule creation in a dialog and saves assignee and PR reviewer filters', async () => {
    const { wrapper, api } = await mountSettings();
    await wrapper.get('.github-card-header button').trigger('click');

    expect(wrapper.get('[role="dialog"]').attributes('aria-modal')).toBe('true');
    expect(wrapper.find('input[aria-label="レビューア"]').exists()).toBe(false);

    await wrapper.get('input[value="pull_request"]').setValue(true);
    await wrapper.get('input[aria-label="アサイン"]').setValue('alice, bob');
    await wrapper.get('input[aria-label="レビューア"]').setValue('carol, dave');
    await wrapper.get('.github-rule-form input:not([disabled])').setValue('PR担当ルール');
    await wrapper.get('.github-rule-form textarea').setValue('Review {{.Title}}');
    await wrapper.get('.github-rule-form').trigger('submit');
    await flushPromises();

    const rule = (await api.listGitHubTriggerRules()).find((candidate) => candidate.name === 'PR担当ルール');
    expect(rule?.filters.assignees).toEqual(['alice', 'bob']);
    expect(rule?.filters.reviewers).toEqual(['carol', 'dave']);
  });

  it('deletes a repository monitor after confirmation', async () => {
    const originalConfirm = window.confirm;
    window.confirm = () => true;
    try {
      const { wrapper, api } = await mountSettings();
      await wrapper.get('.github-danger').trigger('click');
      await flushPromises();
      await expect(api.getGitHubMonitor(DEMO_WORKSPACE)).rejects.toThrow();
    } finally {
      window.confirm = originalConfirm;
    }
  });
});
