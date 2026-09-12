<script setup lang="ts">
import type { UpstreamConfig, UpstreamStatus } from '@maatgen/protocol';
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { AgentApiError } from '../api';
import { useAgentApi } from '../github/useAgentApi';
import { refreshNodes } from '../nodes';

const api = useAgentApi();

interface UpstreamRow {
  id: string;
  enabled: boolean;
  host: string;
  port: string;
  nodeId: string;
  nodeName: string;
  nodeToken: string;
  state: UpstreamStatus['state'];
  lastError: string | undefined;
  lastConnectedAt: string | undefined;
  nextAttemptAt: string | undefined;
  saving: boolean;
  saved: boolean;
  deleting: boolean;
}

const rows = ref<UpstreamRow[]>([]);
const loading = ref(false);
const error = ref('');

const newEnabled = ref(true);
const newHost = ref('');
const newPort = ref('3101');
const newNodeId = ref('');
const newNodeName = ref('');
const newNodeToken = ref('');
const creating = ref(false);
const createError = ref('');

let pollTimer: number | undefined;

const stateLabel: Record<UpstreamStatus['state'], string> = {
  disabled: '無効',
  connecting: '接続試行中',
  connected: '接続済み',
  waiting: '待機中（上位サーバに接続できないため間隔を空けて再試行しています）',
};

function parseUpstreamUrl(url: string): { host: string; port: string } {
  const match = url.match(/^ws:\/\/([^:]+):(\d+)/);
  return match ? { host: match[1]!, port: match[2]! } : { host: '', port: '3101' };
}

function toRow(status: UpstreamStatus, previous?: Pick<UpstreamRow, 'saving' | 'saved' | 'deleting'>): UpstreamRow {
  const { host, port } = parseUpstreamUrl(status.config.upstreamUrl);
  return {
    id: status.config.id ?? '',
    enabled: status.config.enabled,
    host,
    port,
    nodeId: status.config.nodeId,
    nodeName: status.config.nodeName,
    nodeToken: status.config.nodeToken,
    state: status.state,
    lastError: status.lastError,
    lastConnectedAt: status.lastConnectedAt,
    nextAttemptAt: status.nextAttemptAt,
    saving: previous?.saving ?? false,
    saved: previous?.saved ?? false,
    deleting: previous?.deleting ?? false,
  };
}

async function loadInitial() {
  loading.value = true;
  error.value = '';
  try {
    const statuses = await api.listUpstreams();
    rows.value = statuses.map((status) => toRow(status));
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    loading.value = false;
  }
}

// Only refreshes each row's live status display (state/lastError/timestamps),
// not its form fields: a background poll must never overwrite an
// in-progress edit the operator has not saved yet.
async function pollStatus() {
  try {
    const statuses = await api.listUpstreams();
    for (const status of statuses) {
      const row = rows.value.find((candidate) => candidate.id === status.config.id);
      if (!row) continue;
      row.state = status.state;
      row.lastError = status.lastError;
      row.lastConnectedAt = status.lastConnectedAt;
      row.nextAttemptAt = status.nextAttemptAt;
    }
  } catch {
    // Best-effort background polling; a transient failure here should not
    // interrupt whatever the operator is doing on this screen.
  }
}

function toConfig(fields: { enabled: boolean; host: string; port: string; nodeId: string; nodeName: string; nodeToken: string }): UpstreamConfig {
  return {
    enabled: fields.enabled,
    upstreamUrl: `ws://${fields.host.trim()}:${fields.port.trim()}/api/relay/connect`,
    nodeId: fields.nodeId.trim(),
    nodeName: fields.nodeName.trim(),
    nodeToken: fields.nodeToken,
  };
}

async function saveRow(row: UpstreamRow) {
  row.saving = true;
  row.saved = false;
  error.value = '';
  try {
    const status = await api.updateUpstream(row.id, toConfig(row));
    const index = rows.value.findIndex((candidate) => candidate.id === row.id);
    if (index !== -1) rows.value[index] = toRow(status, row);
    row.saved = true;
    window.setTimeout(() => { row.saved = false; }, 1500);
    await refreshNodes(api);
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    row.saving = false;
  }
}

async function deleteRow(row: UpstreamRow) {
  row.deleting = true;
  error.value = '';
  try {
    await api.deleteUpstream(row.id);
    rows.value = rows.value.filter((candidate) => candidate.id !== row.id);
    await refreshNodes(api);
  } catch (cause) {
    error.value = describeError(cause);
    row.deleting = false;
  }
}

function resetNewForm() {
  newEnabled.value = true;
  newHost.value = '';
  newPort.value = '3101';
  newNodeId.value = '';
  newNodeName.value = '';
  newNodeToken.value = '';
}

async function createUpstream() {
  creating.value = true;
  createError.value = '';
  try {
    const status = await api.createUpstream(toConfig({
      enabled: newEnabled.value,
      host: newHost.value,
      port: newPort.value,
      nodeId: newNodeId.value,
      nodeName: newNodeName.value,
      nodeToken: newNodeToken.value,
    }));
    rows.value.push(toRow(status));
    resetNewForm();
    await refreshNodes(api);
  } catch (cause) {
    createError.value = describeError(cause);
  } finally {
    creating.value = false;
  }
}

function formatTimestamp(iso?: string): string {
  if (!iso) return '';
  return new Date(iso).toLocaleString('ja-JP');
}

function describeError(cause: unknown): string {
  if (cause instanceof AgentApiError) return cause.message;
  return cause instanceof Error ? cause.message : String(cause);
}

onMounted(async () => {
  await loadInitial();
  pollTimer = window.setInterval(() => void pollStatus(), 5_000);
});

onBeforeUnmount(() => {
  window.clearInterval(pollTimer);
});
</script>

<template>
  <div class="github-view">
    <h1 class="github-view-title">サーバ設定</h1>
    <p class="github-hint">
      このAgent Managerを下位ノードとして、複数の上位ノードへ同時に接続できます。上位ノード側で`--relay-listen`が有効になっている必要があります。
    </p>

    <p v-if="error" class="github-error">{{ error }}</p>
    <p v-if="loading" class="github-hint">読み込み中…</p>

    <section v-for="row in rows" :key="row.id" class="github-card">
      <h2>{{ row.nodeName || row.nodeId || '(名称未設定)' }}</h2>
      <p class="github-hint">
        <strong>{{ stateLabel[row.state] }}</strong>
        <template v-if="row.lastConnectedAt"> ・最終接続: {{ formatTimestamp(row.lastConnectedAt) }}</template>
        <template v-if="row.nextAttemptAt"> ・次の接続試行: {{ formatTimestamp(row.nextAttemptAt) }}</template>
      </p>
      <p v-if="row.lastError" class="github-error">{{ row.lastError }}</p>

      <div class="github-form-row">
        <label>
          <input v-model="row.enabled" type="checkbox" :disabled="row.saving || row.deleting" />
          有効にする
        </label>
      </div>
      <div class="github-form-row">
        <label>上位サーバホスト名/IPアドレス
          <input v-model="row.host" type="text" placeholder="upper-host" :disabled="row.saving || row.deleting" />
        </label>
        <label>ポート
          <input v-model="row.port" type="text" placeholder="3101" :disabled="row.saving || row.deleting" />
        </label>
      </div>
      <div class="github-form-row">
        <label>Node ID
          <input v-model="row.nodeId" type="text" placeholder="linux-dev" :disabled="row.saving || row.deleting" />
        </label>
        <label>Node Name
          <input v-model="row.nodeName" type="text" placeholder="Linux dev box" :disabled="row.saving || row.deleting" />
        </label>
      </div>
      <div class="github-form-row">
        <label>Node Token（任意）
          <input v-model="row.nodeToken" type="password" placeholder="上位サーバで発行されたノードトークン" :disabled="row.saving || row.deleting" />
        </label>
      </div>
      <div class="github-form-actions">
        <button
          type="button"
          :disabled="row.saving || row.deleting || (row.enabled && (!row.host.trim() || !row.port.trim() || !row.nodeId.trim()))"
          @click="saveRow(row)"
        >保存</button>
        <button type="button" class="github-danger" :disabled="row.saving || row.deleting" @click="deleteRow(row)">削除</button>
        <span v-if="row.saved" class="github-hint">保存しました</span>
      </div>
    </section>

    <section class="github-card">
      <h2>新規サーバを追加</h2>
      <p v-if="createError" class="github-error">{{ createError }}</p>
      <div class="github-form-row">
        <label>
          <input v-model="newEnabled" type="checkbox" :disabled="creating" />
          有効にする
        </label>
      </div>
      <div class="github-form-row">
        <label>上位サーバホスト名/IPアドレス
          <input v-model="newHost" type="text" placeholder="upper-host" :disabled="creating" />
        </label>
        <label>ポート
          <input v-model="newPort" type="text" placeholder="3101" :disabled="creating" />
        </label>
      </div>
      <div class="github-form-row">
        <label>Node ID
          <input v-model="newNodeId" type="text" placeholder="linux-dev" :disabled="creating" />
        </label>
        <label>Node Name
          <input v-model="newNodeName" type="text" placeholder="Linux dev box" :disabled="creating" />
        </label>
      </div>
      <div class="github-form-row">
        <label>Node Token（任意）
          <input v-model="newNodeToken" type="password" placeholder="上位サーバで発行されたノードトークン" :disabled="creating" />
        </label>
      </div>
      <div class="github-form-actions">
        <button
          type="button"
          :disabled="creating || (newEnabled && (!newHost.trim() || !newPort.trim() || !newNodeId.trim()))"
          @click="createUpstream"
        >追加</button>
      </div>
    </section>
  </div>
</template>
