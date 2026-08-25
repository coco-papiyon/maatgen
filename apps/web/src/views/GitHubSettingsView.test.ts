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
    const rule = rules.find((candidate) => candidate.name === '新しいルール');
    expect(rule?.priority).toBe('medium');
  });

  it('allows changing the local path when editing a trigger rule', async () => {
    const api = new MockAgentApi();
    await api.createGitHubMonitor({ workspace: 'C:/demo/another-repository', pollIntervalSeconds: 300 });
    await api.createGitHubTriggerRule({
      workspace: DEMO_WORKSPACE,
      name: '移動可能なルール',
      enabled: true,
      eventKinds: ['issue'],
      filters: {},
      promptTemplate: 'Design {{.Title}}',
      includeBody: false,
      provider: 'codex',
      concurrencyPolicy: 'coalesce',
      priority: 'medium',
    });
    const { wrapper } = await mountSettings(api);

    await wrapper.get('.github-rule-list li button').trigger('click');
    const repositorySelect = wrapper.get('.github-rule-form label select');
    expect((repositorySelect.element as HTMLSelectElement).value).toBe(DEMO_WORKSPACE);
    await repositorySelect.setValue('C:/demo/another-repository');
    await wrapper.get('.github-rule-form').trigger('submit');
    await flushPromises();

    const rules = await api.listGitHubTriggerRules();
    expect(rules[0]?.repository).toBe('C:/demo/another-repository');
  });

  it("lets the user set a rule's priority", async () => {
    const { wrapper, api } = await mountSettings();
    await wrapper.get('.github-card-header button').trigger('click');
    await wrapper.get('.github-rule-form select').setValue(DEMO_WORKSPACE);
    await wrapper.get('.github-rule-form input:not([disabled])').setValue('優先度ルール');
    await wrapper.get('.github-rule-form textarea').setValue('Design {{.Title}}');
    const prioritySelect = wrapper.findAll('.github-rule-form select').find((select) => select.text().includes('高'));
    await prioritySelect!.setValue('high');
    await wrapper.get('.github-rule-form').trigger('submit');
    await flushPromises();

    const rule = (await api.listGitHubTriggerRules(DEMO_WORKSPACE)).find((candidate) => candidate.name === '優先度ルール');
    expect(rule?.priority).toBe('high');
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

  it('offers only the installed providers, scopes the model dropdown to the selected provider, and offers a fixed reasoning-effort dropdown', async () => {
    const { wrapper } = await mountSettings();
    await wrapper.get('.github-card-header button').trigger('click');

    const providerOptions = wrapper.findAll('select[aria-label="Provider"] option').map((option) => option.attributes('value'));
    expect(providerOptions).toEqual(['codex', 'claude']);

    const modelOptions = () => wrapper.findAll('select[aria-label="model"] option').map((option) => option.attributes('value'));
    expect(modelOptions()).toEqual(['', 'gpt-5.6-sol']);

    await wrapper.get('select[aria-label="Provider"]').setValue('claude');
    expect(modelOptions()).toEqual(['', 'claude-opus-5', 'claude-sonnet-5', 'claude-sonnet-4-6', 'claude-haiku-4-5']);

    const reasoningEffortOptions = wrapper.findAll('select[aria-label="reasoningEffort"] option').map((option) => option.attributes('value'));
    expect(reasoningEffortOptions).toEqual(['', 'low', 'medium', 'high', 'xhigh', 'max']);
  });

  it('clears the selected model when switching providers away from a model it does not offer', async () => {
    const { wrapper, api } = await mountSettings();
    await wrapper.get('.github-card-header button').trigger('click');
    await wrapper.get('.github-rule-form select').setValue(DEMO_WORKSPACE);
    await wrapper.get('.github-rule-form input:not([disabled])').setValue('モデル指定ルール');
    await wrapper.get('.github-rule-form textarea').setValue('Design {{.Title}}');
    await wrapper.get('select[aria-label="model"]').setValue('gpt-5.6-sol');
    await wrapper.get('.github-rule-form').trigger('submit');
    await flushPromises();

    const created = (await api.listGitHubTriggerRules()).find((rule) => rule.name === 'モデル指定ルール');
    expect(created?.provider).toBe('codex');
    expect(created?.model).toBe('gpt-5.6-sol');

    await wrapper.get('.github-rule-list li:last-child button').trigger('click');
    expect((wrapper.get('select[aria-label="model"]').element as HTMLSelectElement).value).toBe('gpt-5.6-sol');

    await wrapper.get('select[aria-label="Provider"]').setValue('claude');
    expect((wrapper.get('select[aria-label="model"]').element as HTMLSelectElement).value).toBe('');
  });

  it('shows an out-of-list saved model/reasoningEffort as a selected option and keeps it unchanged when saving', async () => {
    const api = new MockAgentApi();
    await api.createGitHubTriggerRule({
      workspace: DEMO_WORKSPACE,
      name: '旧モデルルール',
      enabled: true,
      eventKinds: ['issue'],
      filters: {},
      promptTemplate: 'Design {{.Title}}',
      includeBody: false,
      provider: 'codex',
      model: 'gpt-4-legacy',
      reasoningEffort: 'ultra-legacy',
      concurrencyPolicy: 'coalesce',
      priority: 'medium',
    });
    const { wrapper } = await mountSettings(api);

    await wrapper.get('.github-rule-list li:last-child button').trigger('click');

    const modelSelect = wrapper.get('select[aria-label="model"]');
    expect((modelSelect.element as HTMLSelectElement).value).toBe('gpt-4-legacy');
    expect(wrapper.findAll('select[aria-label="model"] option').map((option) => option.attributes('value'))).toContain('gpt-4-legacy');

    const reasoningSelect = wrapper.get('select[aria-label="reasoningEffort"]');
    expect((reasoningSelect.element as HTMLSelectElement).value).toBe('ultra-legacy');
    expect(wrapper.findAll('select[aria-label="reasoningEffort"] option').map((option) => option.attributes('value'))).toContain('ultra-legacy');

    await wrapper.get('.github-rule-form').trigger('submit');
    await flushPromises();

    const saved = (await api.listGitHubTriggerRules()).find((rule) => rule.name === '旧モデルルール');
    expect(saved?.model).toBe('gpt-4-legacy');
    expect(saved?.reasoningEffort).toBe('ultra-legacy');
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
