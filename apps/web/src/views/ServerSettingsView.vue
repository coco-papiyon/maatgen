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
  failureCount: number;
  lastError: string | undefined;
  lastConnectedAt: string | undefined;
  nextAttemptAt: string | undefined;
  saving: boolean;
  saved: boolean;
  deleting: boolean;
  reconnecting: boolean;
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
const retryMaxFailures = ref(3);
const activeMaxFailures = ref(3);
const retryIntervalMinutes = ref(5);
const retrySaving = ref(false);
const retrySaved = ref(false);
const creating = ref(false);
const createError = ref('');

let pollTimer: number | undefined;

const stateLabel: Record<UpstreamStatus['state'], string> = {
  disabled: '無効',
  connecting: '接続試行中',
  connected: '接続済み',
  waiting: '再試行待ち',
  stopped: '接続停止（失敗上限に到達）',
};

function parseUpstreamUrl(url: string): { host: string; port: string } {
  const match = url.match(/^ws:\/\/([^:]+):(\d+)/);
  return match ? { host: match[1]!, port: match[2]! } : { host: '', port: '3101' };
}

function toRow(status: UpstreamStatus, previous?: Pick<UpstreamRow, 'saving' | 'saved' | 'deleting' | 'reconnecting'>): UpstreamRow {
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
    failureCount: status.failureCount,
    lastError: status.lastError,
    lastConnectedAt: status.lastConnectedAt,
    nextAttemptAt: status.nextAttemptAt,
    saving: previous?.saving ?? false,
    saved: previous?.saved ?? false,
    deleting: previous?.deleting ?? false,
    reconnecting: previous?.reconnecting ?? false,
  };
}

async function loadInitial() {
  loading.value = true;
  error.value = '';
  try {
    const [statuses, retrySettings] = await Promise.all([api.listUpstreams(), api.getUpstreamRetrySettings()]);
    rows.value = statuses.map((status) => toRow(status));
    retryMaxFailures.value = retrySettings.maxFailures;
    activeMaxFailures.value = retrySettings.maxFailures;
    retryIntervalMinutes.value = retrySettings.retryIntervalMinutes;
    await refreshNodes(api);
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
    let nodeChanged = false;
    for (const status of statuses) {
      const row = rows.value.find((candidate) => candidate.id === status.config.id);
      if (!row) continue;
      if (row.state !== status.state && (row.state === 'stopped' || status.state === 'stopped')) nodeChanged = true;
      row.state = status.state;
      row.failureCount = status.failureCount;
      row.lastError = status.lastError;
      row.lastConnectedAt = status.lastConnectedAt;
      row.nextAttemptAt = status.nextAttemptAt;
    }
    if (nodeChanged) await refreshNodes(api);
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

async function saveRetrySettings() {
  retrySaving.value = true;
  retrySaved.value = false;
  error.value = '';
  try {
    const settings = await api.updateUpstreamRetrySettings({
      maxFailures: retryMaxFailures.value,
      retryIntervalMinutes: retryIntervalMinutes.value,
    });
    retryMaxFailures.value = settings.maxFailures;
    activeMaxFailures.value = settings.maxFailures;
    retryIntervalMinutes.value = settings.retryIntervalMinutes;
    retrySaved.value = true;
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    retrySaving.value = false;
  }
}

async function reconnectRow(row: UpstreamRow) {
  row.reconnecting = true;
  error.value = '';
  try {
    const status = await api.reconnectUpstream(row.id);
    row.state = status.state;
    row.failureCount = status.failureCount;
    row.lastError = status.lastError;
    row.nextAttemptAt = status.nextAttemptAt;
    await refreshNodes(api);
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    row.reconnecting = false;
  }
}

async function saveRow(row: UpstreamRow) {
  row.saving = true;
  row.saved = false;
  error.value = '';
  try {
    const status = await api.updateUpstream(row.id, toConfig(row));
    Object.assign(row, toRow(status, row));
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

    <section class="github-card server-retry-settings">
      <h2>共通の接続再試行設定</h2>
      <div class="github-form-row">
        <label>再試行間隔（分）
          <input v-model.number="retryIntervalMinutes" type="number" min="1" step="1" :disabled="retrySaving" />
        </label>
        <label>失敗上限（回）
          <input v-model.number="retryMaxFailures" type="number" min="2" step="1" :disabled="retrySaving" />
        </label>
      </div>
      <div class="github-form-actions">
        <button type="button" :disabled="retrySaving || !Number.isInteger(retryIntervalMinutes) || retryIntervalMinutes < 1 || !Number.isInteger(retryMaxFailures) || retryMaxFailures < 2" @click="saveRetrySettings">共通設定を保存</button>
        <span v-if="retrySaved" class="github-hint">保存しました</span>
      </div>
    </section>

    <section v-for="row in rows" :key="row.id" class="github-card server-settings-card">
      <h2>{{ row.nodeName || row.nodeId || '(名称未設定)' }}</h2>
      <p class="github-hint">
        <strong>{{ stateLabel[row.state] }}</strong>
        <template v-if="row.lastConnectedAt"> ・最終接続: {{ formatTimestamp(row.lastConnectedAt) }}</template>
        <template v-if="row.nextAttemptAt"> ・次の接続試行: {{ formatTimestamp(row.nextAttemptAt) }}</template>
        ・失敗回数: {{ row.failureCount }} / {{ activeMaxFailures }}
      </p>
      <p v-if="row.lastError" class="github-error">{{ row.lastError }}</p>

      <div class="server-settings-fields">
        <label class="server-settings-enabled">
          <input v-model="row.enabled" type="checkbox" :disabled="row.saving || row.deleting" />
          有効にする
        </label>
        <label>上位サーバホスト名/IPアドレス
          <input v-model="row.host" type="text" placeholder="upper-host" :disabled="row.saving || row.deleting" />
        </label>
        <label>ポート
          <input v-model="row.port" type="text" placeholder="3101" :disabled="row.saving || row.deleting" />
        </label>
        <label>Node ID
          <input v-model="row.nodeId" type="text" placeholder="linux-dev" :disabled="row.saving || row.deleting" />
        </label>
        <label>Node Name
          <input v-model="row.nodeName" type="text" placeholder="Linux dev box" :disabled="row.saving || row.deleting" />
        </label>
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
        <button v-if="row.state === 'stopped'" type="button" :disabled="row.saving || row.deleting || row.reconnecting" @click="reconnectRow(row)">再接続</button>
        <button type="button" class="github-danger" :disabled="row.saving || row.deleting" @click="deleteRow(row)">削除</button>
        <span v-if="row.saved" class="github-hint">保存しました</span>
      </div>
    </section>

    <section class="github-card server-settings-card">
      <h2>新規サーバを追加</h2>
      <p v-if="createError" class="github-error">{{ createError }}</p>
      <div class="server-settings-fields">
        <label class="server-settings-enabled">
          <input v-model="newEnabled" type="checkbox" :disabled="creating" />
          有効にする
        </label>
        <label>上位サーバホスト名/IPアドレス
          <input v-model="newHost" type="text" placeholder="upper-host" :disabled="creating" />
        </label>
        <label>ポート
          <input v-model="newPort" type="text" placeholder="3101" :disabled="creating" />
        </label>
        <label>Node ID
          <input v-model="newNodeId" type="text" placeholder="linux-dev" :disabled="creating" />
        </label>
        <label>Node Name
          <input v-model="newNodeName" type="text" placeholder="Linux dev box" :disabled="creating" />
        </label>
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

<style scoped>
.server-settings-fields {
  display: grid;
  grid-template-columns: 90px minmax(170px, 2fr) 76px minmax(125px, 1fr) minmax(145px, 1fr) minmax(170px, 1.5fr);
  gap: 12px;
  align-items: end;
  overflow-x: auto;
  padding-bottom: 4px;
}
.server-settings-fields label { display: flex; flex-direction: column; gap: 4px; min-width: 0; font-size: 12px; color: var(--muted); }
.server-settings-fields input:not([type='checkbox']) { width: 100%; min-width: 0; padding: 6px 8px; border: 1px solid var(--line); border-radius: 5px; color: inherit; background: var(--panel-soft); }
.server-settings-fields .server-settings-enabled { flex-direction: row; align-items: center; align-self: center; }
@media (max-width: 1100px) { .server-settings-fields { grid-template-columns: 90px 180px 76px 130px 150px 180px; } }
</style>
