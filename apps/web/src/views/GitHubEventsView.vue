<script setup lang="ts">
import { onMounted, ref } from 'vue';
import type { GitHubMonitorEvent, GitHubRepositoryMonitor, GitHubTriggerRule, Provider } from '@maatgen/protocol';
import { AgentApiError } from '../api';
import { useAgentApi } from '../github/useAgentApi';
import { priorityLabel } from '../github/priority';

const api = useAgentApi();

const events = ref<GitHubMonitorEvent[]>([]);
const rules = ref<GitHubTriggerRule[]>([]);
const monitors = ref<GitHubRepositoryMonitor[]>([]);
const providers = ref<Provider[]>([]);
const loading = ref(false);
const error = ref('');
const replayingId = ref('');
const skippingId = ref('');

const REPLAYABLE = new Set(['skipped', 'failed', 'cancelled']);
const SKIPPABLE = new Set(['detected', 'matched', 'queued']);

function eventRepository(repository: string): string {
  const monitor = monitors.value.find((candidate) => candidate.repository === repository);
  return monitor ? `${monitor.owner}/${monitor.name}` : repository;
}

function ruleName(ruleId: string | undefined): string {
  if (!ruleId) return '（削除されたルール）';
  return rules.value.find((rule) => rule.id === ruleId)?.name ?? ruleId;
}

function rulePriority(ruleId: string | undefined): string {
  if (!ruleId) return '—';
  const rule = rules.value.find((candidate) => candidate.id === ruleId);
  return rule ? priorityLabel(rule.priority) : '—';
}

function ruleProvider(ruleId: string | undefined): string {
  if (!ruleId) return '—';
  const rule = rules.value.find((candidate) => candidate.id === ruleId);
  if (!rule) return '—';
  return providers.value.find((candidate) => candidate.id === rule.provider)?.label ?? rule.provider;
}

function canReplay(event: GitHubMonitorEvent): boolean {
  return REPLAYABLE.has(event.status);
}

function canSkip(event: GitHubMonitorEvent): boolean {
  return SKIPPABLE.has(event.status);
}

async function refresh() {
  loading.value = true;
  error.value = '';
  try {
    const [eventList, ruleList, monitorList, providerList] = await Promise.all([
      api.listGitHubMonitorEvents(undefined, 200),
      api.listGitHubTriggerRules().catch(() => []),
      api.listGitHubMonitors().catch(() => []),
      api.listProviders().catch(() => ({ providers: [] })),
    ]);
    events.value = eventList;
    rules.value = ruleList;
    monitors.value = monitorList;
    providers.value = providerList.providers;
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    loading.value = false;
  }
}

async function replay(event: GitHubMonitorEvent) {
  replayingId.value = event.id;
  error.value = '';
  try {
    await api.replayGitHubMonitorEvent(event.id);
    await refresh();
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    replayingId.value = '';
  }
}

async function skip(event: GitHubMonitorEvent) {
  skippingId.value = event.id;
  error.value = '';
  try {
    await api.skipGitHubMonitorEvent(event.id);
    await refresh();
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    skippingId.value = '';
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
    <div class="github-card-header">
      <h1 class="github-view-title">Job</h1>
      <button type="button" :disabled="loading" @click="refresh">更新</button>
    </div>

    <p v-if="error" class="github-error">{{ error }}</p>
    <p v-else-if="loading" class="github-hint">読み込み中…</p>
    <p v-else-if="!events.length" class="github-hint">イベントはまだありません。</p>

    <table v-else class="github-table">
      <thead>
        <tr>
          <th>状態</th>
          <th>リポジトリ</th>
          <th>種別</th>
          <th>Item</th>
          <th>action</th>
          <th>ルール</th>
          <th>プロバイダー</th>
          <th>優先度</th>
          <th>Session / Run</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="event in events" :key="event.id">
          <td>
            <span class="github-badge" :class="`status-${event.status}`">{{ event.status }}</span>
            <div v-if="event.skipReason" class="github-meta">{{ event.skipReason }}</div>
            <div v-if="event.lastError" class="github-error">{{ event.lastError }}</div>
            <div v-if="event.replayOfEventId" class="github-meta">replay of {{ event.replayOfEventId }}</div>
          </td>
          <td>{{ eventRepository(event.repository) }}</td>
          <td>{{ event.kind }}</td>
          <td>
            <a :href="event.itemSnapshot.url" target="_blank" rel="noopener">#{{ event.number }} {{ event.itemSnapshot.title }}</a>
          </td>
          <td>{{ event.action }}</td>
          <td>{{ ruleName(event.ruleId) }}</td>
          <td>{{ ruleProvider(event.ruleId) }}</td>
          <td>{{ rulePriority(event.ruleId) }}</td>
          <td>
            <RouterLink v-if="event.sessionId" :to="`/?session=${event.sessionId}`">Sessionを見る</RouterLink>
            <span v-else class="github-meta">—</span>
          </td>
          <td>
            <button v-if="canSkip(event)" type="button" :disabled="skippingId === event.id" @click="skip(event)">
              スキップ
            </button>
            <button v-if="canReplay(event)" type="button" :disabled="replayingId === event.id" @click="replay(event)">
              このイベントを実行
            </button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
