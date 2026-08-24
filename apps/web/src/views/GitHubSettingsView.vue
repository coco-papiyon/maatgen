<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue';
import type {
  GitHubConcurrencyPolicy,
  GitHubItemState,
  GitHubItemKind,
  GitHubRepositoryMonitor,
  GitHubRepositoryResolution,
  GitHubTriggerRule,
} from '@maatgen/protocol';
import { AgentApiError } from '../api';
import { useAgentApi } from '../github/useAgentApi';
import { githubWorkspace } from '../github/workspace';

const api = useAgentApi();

const resolution = ref<GitHubRepositoryResolution>();
const rules = ref<GitHubTriggerRule[]>([]);
const loading = ref(false);
const error = ref('');
const syncing = ref(false);
const syncMessage = ref('');

const remoteNameChoice = ref('');
const pollIntervalSeconds = ref(300);
const coalesceQueueLimit = ref(20);
const projectName = ref('');
const monitorEnabled = ref(true);
const savingMonitor = ref(false);

const monitor = computed<GitHubRepositoryMonitor | undefined>(() => resolution.value?.monitor);
const hasAmbiguousRemote = computed(() => !monitor.value && !resolution.value?.selected && (resolution.value?.candidates.length ?? 0) > 1);
const hasNoRemote = computed(() => !monitor.value && (resolution.value?.candidates.length ?? 0) === 0 && resolution.value !== undefined);

async function refresh() {
  if (!githubWorkspace.value) {
    resolution.value = undefined;
    rules.value = [];
    return;
  }
  loading.value = true;
  error.value = '';
  try {
    resolution.value = await api.resolveGitHubRepository(githubWorkspace.value);
    if (resolution.value.monitor) {
      monitorEnabled.value = resolution.value.monitor.enabled;
      pollIntervalSeconds.value = resolution.value.monitor.pollIntervalSeconds;
      coalesceQueueLimit.value = resolution.value.monitor.coalesceQueueLimit;
      projectName.value = resolution.value.monitor.projectName ?? '';
      remoteNameChoice.value = resolution.value.monitor.remoteName;
      rules.value = await api.listGitHubTriggerRules(githubWorkspace.value);
    } else {
      rules.value = [];
      remoteNameChoice.value = resolution.value.selected?.remoteName ?? resolution.value.candidates[0]?.remoteName ?? '';
    }
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    loading.value = false;
  }
}

async function createMonitor() {
  savingMonitor.value = true;
  error.value = '';
  try {
    await api.createGitHubMonitor({
      workspace: githubWorkspace.value,
      ...(remoteNameChoice.value ? { remoteName: remoteNameChoice.value } : {}),
      pollIntervalSeconds: pollIntervalSeconds.value,
      coalesceQueueLimit: coalesceQueueLimit.value,
      ...(projectName.value.trim() ? { projectName: projectName.value.trim() } : {}),
    });
    await refresh();
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    savingMonitor.value = false;
  }
}

async function updateMonitor() {
  savingMonitor.value = true;
  error.value = '';
  try {
    await api.updateGitHubMonitor({
      workspace: githubWorkspace.value,
      enabled: monitorEnabled.value,
      pollIntervalSeconds: pollIntervalSeconds.value,
      coalesceQueueLimit: coalesceQueueLimit.value,
      ...(projectName.value.trim() ? { projectName: projectName.value.trim() } : { projectName: '' }),
      ...(remoteNameChoice.value ? { remoteName: remoteNameChoice.value } : {}),
    });
    await refresh();
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    savingMonitor.value = false;
  }
}

async function deleteMonitor() {
  if (!confirm('この監視設定を削除しますか？関連する監視ルールも削除されます。')) return;
  error.value = '';
  try {
    await api.deleteGitHubMonitor(githubWorkspace.value);
    await refresh();
  } catch (cause) {
    error.value = describeError(cause);
  }
}

async function syncNow() {
  syncing.value = true;
  syncMessage.value = '';
  error.value = '';
  try {
    const result = await api.syncGitHubMonitorNow(githubWorkspace.value);
    syncMessage.value = `Issue ${result.issuesProcessed}件、PR ${result.pullRequestsProcessed}件を確認し、${result.eventsMatched}件のイベントが一致しました。`;
    await refresh();
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    syncing.value = false;
  }
}

// --- Trigger Rule editor -------------------------------------------------

interface RuleForm {
  id: string;
  name: string;
  enabled: boolean;
  eventKinds: GitHubItemKind[];
  promptTemplate: string;
  includeBody: boolean;
  provider: 'codex' | 'claude' | 'copilot';
  model: string;
  reasoningEffort: string;
  concurrencyPolicy: GitHubConcurrencyPolicy;
  state: 'open' | 'closed' | 'all';
  labels: string;
  assignees: string;
  reviewers: string;
  projectTitle: string;
  projectField: string;
  projectValue: string;
}

function blankRuleForm(): RuleForm {
  return {
    id: '', name: '', enabled: true, eventKinds: ['issue'], promptTemplate: '', includeBody: false,
    provider: 'codex', model: '', reasoningEffort: '', concurrencyPolicy: 'coalesce', state: 'open',
    labels: '', assignees: '', reviewers: '', projectTitle: '', projectField: '', projectValue: '',
  };
}

const editingRule = ref<RuleForm>();
const savingRule = ref(false);
const ruleDialog = ref<HTMLElement>();
const includesIssues = computed(() => editingRule.value?.eventKinds.includes('issue') ?? false);
const includesPullRequests = computed(() => editingRule.value?.eventKinds.includes('pull_request') ?? false);

function focusRuleDialog() {
  void nextTick(() => ruleDialog.value?.querySelector<HTMLElement>('input')?.focus());
}

function startCreateRule() {
  editingRule.value = blankRuleForm();
  focusRuleDialog();
}

function startEditRule(rule: GitHubTriggerRule) {
  editingRule.value = {
    id: rule.id, name: rule.name, enabled: rule.enabled, eventKinds: [...rule.eventKinds],
    promptTemplate: rule.promptTemplate, includeBody: rule.includeBody, provider: rule.provider as RuleForm['provider'],
    model: rule.model ?? '', reasoningEffort: rule.reasoningEffort ?? '', concurrencyPolicy: rule.concurrencyPolicy,
    state: rule.filters.states?.length === 2 ? 'all' : rule.filters.states?.[0] === 'closed' ? 'closed' : 'open',
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
      workspace: githubWorkspace.value,
      name: form.name,
      enabled: form.enabled,
      eventKinds: form.eventKinds,
      filters: {
        states: (form.state === 'all' ? ['open', 'closed'] : [form.state]) as GitHubItemState[],
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
    rules.value = await api.listGitHubTriggerRules(githubWorkspace.value);
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
    rules.value = await api.listGitHubTriggerRules(githubWorkspace.value);
  } catch (cause) {
    error.value = describeError(cause);
  }
}

function describeError(cause: unknown): string {
  if (cause instanceof AgentApiError) return cause.message;
  return cause instanceof Error ? cause.message : String(cause);
}

watch(githubWorkspace, () => void refresh());
onMounted(() => void refresh());
</script>

<template>
  <div class="github-view">
    <h1 class="github-view-title">GitHub監視設定</h1>

    <p v-if="!githubWorkspace" class="github-empty-hint">
      Sessionを選択すると、そのリポジトリが自動的に対象になります（画面右上に表示されます）。
    </p>

    <template v-else>
      <p v-if="error" class="github-error">{{ error }}</p>
      <p v-if="loading" class="github-hint">読み込み中…</p>

      <section v-if="resolution" class="github-card">
        <h2>対象リポジトリ</h2>
        <dl class="github-kv">
          <dt>ローカルパス</dt><dd>{{ resolution.repository }}</dd>
          <dt v-if="resolution.selected || monitor">GitHub</dt>
          <dd v-if="monitor">{{ monitor.host }}/{{ monitor.owner }}/{{ monitor.name }} (remote: {{ monitor.remoteName }})</dd>
          <dd v-else-if="resolution.selected">{{ resolution.selected.host }}/{{ resolution.selected.owner }}/{{ resolution.selected.name }} (remote: {{ resolution.selected.remoteName }})</dd>
          <dt>プロジェクト名</dt>
          <dd>
            <div class="github-inline-field">
              <input v-model="projectName" placeholder="Roadmap（任意）" aria-label="プロジェクト名" />
              <button v-if="monitor" type="button" :disabled="savingMonitor" @click="updateMonitor">プロジェクト名を保存</button>
            </div>
            <small v-if="!monitor" class="github-field-hint">監視を開始すると保存できます。</small>
          </dd>
        </dl>

        <p v-if="hasNoRemote" class="github-error">このリポジトリにはGitHubを指すremoteが見つかりません。</p>

        <div v-if="hasAmbiguousRemote || !monitor" class="github-form-row">
          <label>
            remote
            <select v-model="remoteNameChoice">
              <option v-for="candidate in resolution.candidates" :key="candidate.remoteName" :value="candidate.remoteName">
                {{ candidate.remoteName }} ({{ candidate.owner }}/{{ candidate.name }})
              </option>
            </select>
          </label>
        </div>
      </section>

      <section v-if="resolution && resolution.candidates.length > 0" class="github-card">
        <h2>{{ monitor ? '監視設定' : '監視を開始' }}</h2>
        <div class="github-form-row">
          <label>ポーリング間隔（秒）<input v-model.number="pollIntervalSeconds" type="number" min="60" /></label>
          <label>coalesce保留上限<input v-model.number="coalesceQueueLimit" type="number" min="1" /></label>
          <label v-if="monitor" class="github-checkbox"><input v-model="monitorEnabled" type="checkbox" /> 有効</label>
        </div>
        <div class="github-form-actions">
          <button v-if="!monitor" type="button" :disabled="savingMonitor" @click="createMonitor">監視を開始する</button>
          <template v-else>
            <button type="button" :disabled="savingMonitor" @click="updateMonitor">設定を保存</button>
            <button type="button" :disabled="syncing" @click="syncNow">今すぐ同期</button>
            <button type="button" class="github-danger" @click="deleteMonitor">監視を削除</button>
          </template>
        </div>
        <p v-if="syncMessage" class="github-hint">{{ syncMessage }}</p>
        <p v-if="monitor?.lastSyncedAt" class="github-meta">最終同期: {{ monitor.lastSyncedAt }}</p>
        <p v-if="monitor?.lastError" class="github-error">直近のエラー: {{ monitor.lastError }}</p>
      </section>

      <section v-if="monitor" class="github-card">
        <div class="github-card-header">
          <h2>監視ルール</h2>
          <button type="button" @click="startCreateRule">新しいルール</button>
        </div>

        <ul v-if="rules.length" class="github-rule-list">
          <li v-for="rule in rules" :key="rule.id" class="github-rule">
            <div>
              <strong>{{ rule.name }}</strong>
              <span class="github-meta">{{ rule.eventKinds.join(', ') }} / {{ rule.provider }} / {{ rule.concurrencyPolicy }}</span>
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
            <label>状態
              <select v-model="editingRule.state">
                <option value="open">Open（デフォルト）</option>
                <option value="closed">Close</option>
                <option value="all">Open+Close</option>
              </select>
            </label>
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
    </template>
  </div>
</template>
