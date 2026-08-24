<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue';
import type {
  GitHubConcurrencyPolicy,
  GitHubItemKind,
  GitHubRepositoryMonitor,
  GitHubRepositoryResolution,
  GitHubTriggerRule,
} from '@maatgen/protocol';
import { AgentApiError } from '../api';
import { useAgentApi } from '../github/useAgentApi';
import { refreshRepositories, repositories } from '../github/repositories';

const api = useAgentApi();

const rules = ref<GitHubTriggerRule[]>([]);
const loading = ref(false);
const error = ref('');

// ポーリング間隔とcoalesce保留上限はリポジトリごとに変える理由がないため、
// 全リポジトリ共通の一つの設定として扱う（個別編集はしない）。
const commonPollIntervalSeconds = ref(300);
const commonCoalesceQueueLimit = ref(20);
const applyingCommonSettings = ref(false);
const projectNameDrafts = ref<Record<string, string>>({});

async function refresh() {
  loading.value = true;
  error.value = '';
  try {
    await refreshRepositories(api);
    const first = repositories.value[0];
    if (first) {
      commonPollIntervalSeconds.value = first.pollIntervalSeconds;
      commonCoalesceQueueLimit.value = first.coalesceQueueLimit;
    }
    projectNameDrafts.value = Object.fromEntries(repositories.value.map((monitor) => [monitor.repository, monitor.projectName ?? '']));
    rules.value = await api.listGitHubTriggerRules();
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    loading.value = false;
  }
}

async function applyCommonSettings() {
  applyingCommonSettings.value = true;
  error.value = '';
  try {
    for (const monitor of repositories.value) {
      await api.updateGitHubMonitor({
        workspace: monitor.repository,
        enabled: monitor.enabled,
        pollIntervalSeconds: commonPollIntervalSeconds.value,
        coalesceQueueLimit: commonCoalesceQueueLimit.value,
        projectName: monitor.projectName ?? '',
      });
    }
    await refresh();
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    applyingCommonSettings.value = false;
  }
}

function repositoryLabel(monitor: GitHubRepositoryMonitor): string {
  return `${monitor.host}/${monitor.owner}/${monitor.name}`;
}

function ruleRepositoryLabel(repository: string): string {
  const monitor = repositories.value.find((candidate) => candidate.repository === repository);
  return monitor ? repositoryLabel(monitor) : repository;
}

async function toggleEnabled(monitor: GitHubRepositoryMonitor) {
  error.value = '';
  try {
    await api.updateGitHubMonitor({
      workspace: monitor.repository,
      enabled: !monitor.enabled,
      pollIntervalSeconds: monitor.pollIntervalSeconds,
      coalesceQueueLimit: monitor.coalesceQueueLimit,
      projectName: monitor.projectName ?? '',
    });
    await refresh();
  } catch (cause) {
    error.value = describeError(cause);
  }
}

const syncingRepository = ref('');
async function syncNow(monitor: GitHubRepositoryMonitor) {
  syncingRepository.value = monitor.repository;
  error.value = '';
  try {
    await api.syncGitHubMonitorNow(monitor.repository);
    await refresh();
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    syncingRepository.value = '';
  }
}

async function deleteMonitor(monitor: GitHubRepositoryMonitor) {
  if (!confirm(`「${repositoryLabel(monitor)}」の監視を削除しますか？関連する監視ルールも削除されます。`)) return;
  error.value = '';
  try {
    await api.deleteGitHubMonitor(monitor.repository);
    await refresh();
  } catch (cause) {
    error.value = describeError(cause);
  }
}

// --- Project name (the only per-repository field left to edit; poll
// interval/coalesce limit are common settings above, and remote is always
// whatever the local repository's Git remote resolves to — never
// user-editable, see resolveNewRepository/registerNewRepository). --------

const savingProjectName = ref('');

async function saveProjectName(monitor: GitHubRepositoryMonitor) {
  savingProjectName.value = monitor.repository;
  error.value = '';
  try {
    await api.updateGitHubMonitor({
      workspace: monitor.repository,
      enabled: monitor.enabled,
      pollIntervalSeconds: monitor.pollIntervalSeconds,
      coalesceQueueLimit: monitor.coalesceQueueLimit,
      projectName: projectNameDrafts.value[monitor.repository] ?? '',
    });
    await refresh();
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    savingProjectName.value = '';
  }
}

// --- Add a repository -----------------------------------------------------

const newRepositoryPath = ref('');
const addResolution = ref<GitHubRepositoryResolution>();
const addRemoteChoice = ref('');
const addError = ref('');
const resolvingNewRepository = ref(false);
const registeringRepository = ref(false);

const addHasNoRemote = computed(() => addResolution.value !== undefined && addResolution.value.candidates.length === 0);
const addSingleCandidate = computed(() => (addResolution.value?.candidates.length === 1 ? addResolution.value.candidates[0] : undefined));
const addAlreadyRegistered = computed(
  () => addResolution.value !== undefined && repositories.value.some((monitor) => monitor.repository === addResolution.value!.repository),
);

async function resolveNewRepository() {
  const path = newRepositoryPath.value.trim();
  if (!path) return;
  resolvingNewRepository.value = true;
  addError.value = '';
  addResolution.value = undefined;
  try {
    const resolution = await api.resolveGitHubRepository(path);
    addResolution.value = resolution;
    addRemoteChoice.value = resolution.selected?.remoteName ?? resolution.candidates[0]?.remoteName ?? '';
  } catch (cause) {
    addError.value = describeError(cause);
  } finally {
    resolvingNewRepository.value = false;
  }
}

async function registerNewRepository() {
  const resolution = addResolution.value;
  if (!resolution) return;
  registeringRepository.value = true;
  addError.value = '';
  try {
    await api.createGitHubMonitor({
      workspace: resolution.repository,
      ...(addRemoteChoice.value ? { remoteName: addRemoteChoice.value } : {}),
      pollIntervalSeconds: commonPollIntervalSeconds.value,
      coalesceQueueLimit: commonCoalesceQueueLimit.value,
    });
    newRepositoryPath.value = '';
    addResolution.value = undefined;
    await refresh();
  } catch (cause) {
    addError.value = describeError(cause);
  } finally {
    registeringRepository.value = false;
  }
}

// --- Trigger Rule editor (cross-repository) -------------------------------

interface RuleForm {
  id: string;
  repository: string;
  name: string;
  enabled: boolean;
  eventKinds: GitHubItemKind[];
  promptTemplate: string;
  includeBody: boolean;
  provider: 'codex' | 'claude' | 'copilot';
  model: string;
  reasoningEffort: string;
  concurrencyPolicy: GitHubConcurrencyPolicy;
  labels: string;
  assignees: string;
  reviewers: string;
  projectTitle: string;
  projectField: string;
  projectValue: string;
}

function blankRuleForm(): RuleForm {
  return {
    id: '', repository: repositories.value[0]?.repository ?? '', name: '', enabled: true, eventKinds: ['issue'], promptTemplate: '', includeBody: false,
    provider: 'codex', model: '', reasoningEffort: '', concurrencyPolicy: 'coalesce',
    labels: '', assignees: '', reviewers: '', projectTitle: '', projectField: '', projectValue: '',
  };
}

const editingRule = ref<RuleForm>();
const savingRule = ref(false);
const ruleDialog = ref<HTMLElement>();
const includesIssues = computed(() => editingRule.value?.eventKinds.includes('issue') ?? false);
const includesPullRequests = computed(() => editingRule.value?.eventKinds.includes('pull_request') ?? false);

function focusRuleDialog() {
  void nextTick(() => ruleDialog.value?.querySelector<HTMLElement>('input, select')?.focus());
}

function startCreateRule() {
  editingRule.value = blankRuleForm();
  focusRuleDialog();
}

function startEditRule(rule: GitHubTriggerRule) {
  editingRule.value = {
    id: rule.id, repository: rule.repository, name: rule.name, enabled: rule.enabled, eventKinds: [...rule.eventKinds],
    promptTemplate: rule.promptTemplate, includeBody: rule.includeBody, provider: rule.provider as RuleForm['provider'],
    model: rule.model ?? '', reasoningEffort: rule.reasoningEffort ?? '', concurrencyPolicy: rule.concurrencyPolicy,
    labels: (rule.filters.labels ?? []).join(', '),
    assignees: (rule.filters.assignees ?? []).join(', '),
    reviewers: (rule.filters.reviewers ?? []).join(', '),
    projectTitle: rule.filters.project?.projectTitle ?? '', projectField: rule.filters.project?.fieldName ?? '',
    projectValue: rule.filters.project?.value ?? '',
  };
  focusRuleDialog();
}

function cancelEditRule() {
  editingRule.value = undefined;
}

async function saveRule() {
  const form = editingRule.value;
  if (!form) return;
  savingRule.value = true;
  error.value = '';
  try {
    const labels = parseCommaSeparated(form.labels);
    const assignees = parseCommaSeparated(form.assignees);
    const reviewers = parseCommaSeparated(form.reviewers);
    const request = {
      workspace: form.repository,
      name: form.name,
      enabled: form.enabled,
      eventKinds: form.eventKinds,
      filters: {
        ...(labels.length ? { labels } : {}),
        ...(assignees.length ? { assignees } : {}),
        ...(form.eventKinds.includes('pull_request') && reviewers.length ? { reviewers } : {}),
        ...(form.projectTitle && form.projectField
          ? { project: { projectTitle: form.projectTitle, fieldName: form.projectField, value: form.projectValue } }
          : {}),
      },
      promptTemplate: form.promptTemplate,
      includeBody: form.includeBody,
      provider: form.provider,
      ...(form.model ? { model: form.model } : {}),
      ...(form.reasoningEffort ? { reasoningEffort: form.reasoningEffort } : {}),
      concurrencyPolicy: form.concurrencyPolicy,
    };
    if (form.id) {
      await api.updateGitHubTriggerRule(form.id, request);
    } else {
      await api.createGitHubTriggerRule(request);
    }
    editingRule.value = undefined;
    rules.value = await api.listGitHubTriggerRules();
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    savingRule.value = false;
  }
}

function parseCommaSeparated(value: string): string[] {
  return [...new Set(value.split(',').map((entry) => entry.trim()).filter(Boolean))];
}

async function deleteRule(rule: GitHubTriggerRule) {
  if (!confirm(`ルール「${rule.name}」を削除しますか？`)) return;
  error.value = '';
  try {
    await api.deleteGitHubTriggerRule(rule.id);
    rules.value = await api.listGitHubTriggerRules();
  } catch (cause) {
    error.value = describeError(cause);
  }
}

function describeError(cause: unknown): string {
  if (cause instanceof AgentApiError) return cause.message;
  return cause instanceof Error ? cause.message : String(cause);
}

onMounted(() => void refresh());
</script>

<template>
  <div class="github-view">
    <h1 class="github-view-title">GitHub監視設定</h1>

    <p v-if="error" class="github-error">{{ error }}</p>
    <p v-if="loading" class="github-hint">読み込み中…</p>

    <section class="github-card">
      <h2>共通設定</h2>
      <p class="github-hint">ポーリング間隔とcoalesce保留上限は全リポジトリで共通です。remoteは各ローカルリポジトリのGit remoteが自動的に使われ、ここでは変更できません。</p>
      <div class="github-form-row">
        <label>ポーリング間隔（秒）<input v-model.number="commonPollIntervalSeconds" type="number" min="60" /></label>
        <label>coalesce保留上限<input v-model.number="commonCoalesceQueueLimit" type="number" min="1" /></label>
      </div>
      <div class="github-form-actions">
        <button type="button" :disabled="applyingCommonSettings || !repositories.length" @click="applyCommonSettings">全リポジトリに適用</button>
      </div>
    </section>

    <section class="github-card">
      <h2>対象リポジトリ</h2>
      <p v-if="!repositories.length" class="github-hint">まだリポジトリが登録されていません。</p>
      <table v-else class="github-table">
        <thead>
          <tr>
            <th>ローカルパス</th><th>GitHub</th><th>プロジェクト名</th><th>有効</th><th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="monitor in repositories" :key="monitor.repository">
            <td :title="monitor.repository">{{ monitor.repository }}</td>
            <td>{{ repositoryLabel(monitor) }}</td>
            <td>
              <div class="github-inline-field">
                <input v-model="projectNameDrafts[monitor.repository]" placeholder="Roadmap（任意）" :aria-label="`プロジェクト名 (${monitor.repository})`" />
                <button type="button" :disabled="savingProjectName === monitor.repository" @click="saveProjectName(monitor)">保存</button>
              </div>
            </td>
            <td>
              <label class="github-checkbox">
                <input type="checkbox" :checked="monitor.enabled" @change="toggleEnabled(monitor)" />
              </label>
            </td>
            <td>
              <div class="github-form-actions">
                <button type="button" :disabled="syncingRepository === monitor.repository" @click="syncNow(monitor)">今すぐ同期</button>
                <button type="button" class="github-danger" @click="deleteMonitor(monitor)">削除</button>
              </div>
              <p v-if="monitor.lastError" class="github-error">{{ monitor.lastError }}</p>
            </td>
          </tr>
        </tbody>
      </table>

      <div class="github-add-repository">
        <form class="github-form-row" @submit.prevent="resolveNewRepository">
          <label>ローカルパスを追加
            <input v-model="newRepositoryPath" placeholder="C:/path/to/repository" />
          </label>
          <button type="submit" :disabled="resolvingNewRepository || !newRepositoryPath.trim()">解決</button>
        </form>
        <p v-if="addError" class="github-error">{{ addError }}</p>
        <p v-else-if="addAlreadyRegistered" class="github-hint">このリポジトリは既に登録されています。</p>
        <p v-else-if="addHasNoRemote" class="github-error">このリポジトリにはGitHubを指すremoteが見つかりません。</p>
        <template v-else-if="addResolution && addResolution.candidates.length">
          <div v-if="addResolution.candidates.length > 1" class="github-form-row">
            <label>remote（複数見つかったため選択してください）
              <select v-model="addRemoteChoice">
                <option v-for="candidate in addResolution.candidates" :key="candidate.remoteName" :value="candidate.remoteName">
                  {{ candidate.remoteName }} ({{ candidate.owner }}/{{ candidate.name }})
                </option>
              </select>
            </label>
          </div>
          <p v-else class="github-meta">
            remote: {{ addSingleCandidate?.remoteName }} ({{ addSingleCandidate?.owner }}/{{ addSingleCandidate?.name }})
          </p>
          <div class="github-form-actions">
            <button type="button" :disabled="registeringRepository" @click="registerNewRepository">このリポジトリを登録する</button>
          </div>
        </template>
      </div>
    </section>

    <section class="github-card">
      <div class="github-card-header">
        <h2>監視ルール</h2>
        <button type="button" :disabled="!repositories.length" @click="startCreateRule">新しいルール</button>
      </div>

      <ul v-if="rules.length" class="github-rule-list">
        <li v-for="rule in rules" :key="rule.id" class="github-rule">
          <div>
            <strong>{{ rule.name }}</strong>
            <span class="github-meta">{{ ruleRepositoryLabel(rule.repository) }} / {{ rule.eventKinds.join(', ') }} / {{ rule.provider }} / {{ rule.concurrencyPolicy }}</span>
            <span v-if="!rule.enabled" class="github-badge">無効</span>
          </div>
          <div class="github-form-actions">
            <button type="button" @click="startEditRule(rule)">編集</button>
            <button type="button" class="github-danger" @click="deleteRule(rule)">削除</button>
          </div>
        </li>
      </ul>
      <p v-else class="github-hint">ルールはまだありません。</p>
    </section>

    <div v-if="editingRule" class="github-modal-backdrop" @click.self="cancelEditRule">
      <section
        ref="ruleDialog"
        class="github-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="github-rule-dialog-title"
        @keydown.esc="cancelEditRule"
      >
        <form class="github-rule-form" @submit.prevent="saveRule">
          <div class="github-modal-header">
            <h2 id="github-rule-dialog-title">{{ editingRule.id ? '監視ルールを編集' : '監視ルールを作成' }}</h2>
            <button type="button" aria-label="閉じる" @click="cancelEditRule">×</button>
          </div>
          <label>ローカルパス
            <select v-if="!editingRule.id" v-model="editingRule.repository" required>
              <option v-for="monitor in repositories" :key="monitor.repository" :value="monitor.repository">
                {{ monitor.repository }} ({{ repositoryLabel(monitor) }})
              </option>
            </select>
            <input v-else :value="editingRule.repository" disabled />
          </label>
          <label>ルール名<input v-model="editingRule.name" required /></label>
          <fieldset class="github-checkbox-group">
            <legend>対象</legend>
            <label class="github-checkbox"><input v-model="editingRule.eventKinds" type="checkbox" value="issue" /> Issue</label>
            <label class="github-checkbox"><input v-model="editingRule.eventKinds" type="checkbox" value="pull_request" /> Pull Request</label>
          </fieldset>
          <div v-if="includesIssues || includesPullRequests" class="github-form-row">
            <label>アサイン（GitHub login、カンマ区切り）
              <input v-model="editingRule.assignees" aria-label="アサイン" placeholder="octocat, hubot" />
            </label>
            <label v-if="includesPullRequests">レビューア（GitHub login、カンマ区切り）
              <input v-model="editingRule.reviewers" aria-label="レビューア" placeholder="reviewer1, reviewer2" />
            </label>
          </div>
          <label>label条件（カンマ区切り、任意）<input v-model="editingRule.labels" placeholder="bug, needs-design" /></label>
          <div class="github-form-row">
            <label>Project名（任意）<input v-model="editingRule.projectTitle" placeholder="Roadmap" /></label>
            <label>フィールド名<input v-model="editingRule.projectField" placeholder="Status" /></label>
            <label>値<input v-model="editingRule.projectValue" placeholder="Ready" /></label>
          </div>
          <label>Promptテンプレート
            <textarea v-model="editingRule.promptTemplate" rows="4" required placeholder="Design {{.Title}} (#{{.Number}})"></textarea>
          </label>
          <label class="github-checkbox"><input v-model="editingRule.includeBody" type="checkbox" /> Issue/PR本文をPromptに含める</label>
          <div class="github-form-row">
            <label>Provider
              <select v-model="editingRule.provider">
                <option value="codex">codex</option>
                <option value="claude">claude</option>
                <option value="copilot">copilot</option>
              </select>
            </label>
            <label>model（任意）<input v-model="editingRule.model" /></label>
            <label>reasoningEffort（任意）<input v-model="editingRule.reasoningEffort" /></label>
          </div>
          <label>同時実行時の扱い
            <select v-model="editingRule.concurrencyPolicy">
              <option value="coalesce">coalesce（保留して後で再評価）</option>
              <option value="skip">skip（今回はスキップ）</option>
            </select>
          </label>
          <label class="github-checkbox"><input v-model="editingRule.enabled" type="checkbox" /> 有効</label>
          <div class="github-form-actions github-modal-actions">
            <button type="button" @click="cancelEditRule">キャンセル</button>
            <button type="submit" :disabled="savingRule">保存</button>
          </div>
        </form>
      </section>
    </div>
  </div>
</template>
