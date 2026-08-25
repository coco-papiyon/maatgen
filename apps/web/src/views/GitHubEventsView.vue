<script setup lang="ts">
import { onMounted, ref, watch } from 'vue';
import type { GitHubMonitorEvent, GitHubTriggerRule } from '@maatgen/protocol';
import { AgentApiError } from '../api';
import { useAgentApi } from '../github/useAgentApi';
import { selectedRepository } from '../github/repositories';
import { priorityLabel } from '../github/priority';

const api = useAgentApi();

const events = ref<GitHubMonitorEvent[]>([]);
const rules = ref<GitHubTriggerRule[]>([]);
const loading = ref(false);
const error = ref('');
const replayingId = ref('');
const skippingId = ref('');

const REPLAYABLE = new Set(['skipped', 'failed', 'cancelled']);
const SKIPPABLE = new Set(['detected', 'matched', 'queued']);

function ruleName(ruleId: string | undefined): string {
  if (!ruleId) return '（削除されたルール）';
  return rules.value.find((rule) => rule.id === ruleId)?.name ?? ruleId;
}

function rulePriority(ruleId: string | undefined): string {
  if (!ruleId) return '—';
  const rule = rules.value.find((candidate) => candidate.id === ruleId);
  return rule ? priorityLabel(rule.priority) : '—';
}

function canReplay(event: GitHubMonitorEvent): boolean {
  return REPLAYABLE.has(event.status);
}

function canSkip(event: GitHubMonitorEvent): boolean {
  return SKIPPABLE.has(event.status);
}

async function refresh() {
  if (!selectedRepository.value) {
    events.value = [];
    rules.value = [];
    return;
  }
  loading.value = true;
  error.value = '';
  try {
    const [eventList, ruleList] = await Promise.all([
      api.listGitHubMonitorEvents(selectedRepository.value, 200),
      api.listGitHubTriggerRules(selectedRepository.value).catch(() => []),
    ]);
    events.value = eventList;
    rules.value = ruleList;
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

watch(selectedRepository, () => void refresh());
onMounted(() => void refresh());
</script>

<template>
  <div class="github-view">
    <div class="github-card-header">
      <h1 class="github-view-title">イベント履歴</h1>
      <button type="button" :disabled="loading" @click="refresh">更新</button>
    </div>

    <p v-if="!selectedRepository" class="github-empty-hint">
      画面右上でリポジトリを選択してください。
    </p>
    <p v-else-if="error" class="github-error">{{ error }}</p>
    <p v-else-if="loading" class="github-hint">読み込み中…</p>
    <p v-else-if="!events.length" class="github-hint">イベントはまだありません。</p>

    <table v-else class="github-table">
      <thead>
        <tr>
          <th>状態</th>
          <th>種別</th>
          <th>Item</th>
          <th>action</th>
          <th>ルール</th>
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
          <td>{{ event.kind }}</td>
          <td>
            <a :href="event.itemSnapshot.url" target="_blank" rel="noopener">#{{ event.number }} {{ event.itemSnapshot.title }}</a>
          </td>
          <td>{{ event.action }}</td>
          <td>{{ ruleName(event.ruleId) }}</td>
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
