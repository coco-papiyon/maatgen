<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue';
import type { UpstreamStatus } from '@maatgen/protocol';
import { AgentApiError } from '../api';
import { useAgentApi } from '../github/useAgentApi';

const api = useAgentApi();

const status = ref<UpstreamStatus>();
const loading = ref(false);
const error = ref('');
const saving = ref(false);
const saved = ref(false);

const enabled = ref(false);
const upstreamUrl = ref('');
const nodeId = ref('');
const nodeName = ref('');
const nodeToken = ref('');

let pollTimer: number | undefined;

function applyStatusToForm(value: UpstreamStatus) {
  enabled.value = value.config.enabled;
  upstreamUrl.value = value.config.upstreamUrl;
  nodeId.value = value.config.nodeId;
  nodeName.value = value.config.nodeName;
  nodeToken.value = value.config.nodeToken;
}

const stateLabel: Record<UpstreamStatus['state'], string> = {
  disabled: '無効',
  connecting: '接続試行中',
  connected: '接続済み',
  waiting: '待機中（上位サーバに接続できないため間隔を空けて再試行しています）',
};

async function loadInitial() {
  loading.value = true;
  error.value = '';
  try {
    const value = await api.getUpstreamStatus();
    status.value = value;
    applyStatusToForm(value);
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    loading.value = false;
  }
}

// Only refreshes the live status display (state/lastError/timestamps), not
// the form fields: a background poll must never overwrite an in-progress
// edit the operator has not saved yet.
async function pollStatus() {
  try {
    status.value = await api.getUpstreamStatus();
  } catch {
    // Best-effort background polling; a transient failure here should not
    // interrupt whatever the operator is doing on this screen.
  }
}

async function save() {
  saving.value = true;
  saved.value = false;
  error.value = '';
  try {
    status.value = await api.setUpstreamConfig({
      enabled: enabled.value,
      upstreamUrl: upstreamUrl.value.trim(),
      nodeId: nodeId.value.trim(),
      nodeName: nodeName.value.trim(),
      nodeToken: nodeToken.value,
    });
    saved.value = true;
    window.setTimeout(() => { saved.value = false; }, 1500);
  } catch (cause) {
    error.value = describeError(cause);
  } finally {
    saving.value = false;
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
      このAgent Managerを下位ノードとして、別の上位ノードへ接続します（ADR-009）。上位ノード側で`--relay-listen`が有効になっている必要があります。
    </p>

    <p v-if="error" class="github-error">{{ error }}</p>
    <p v-if="loading" class="github-hint">読み込み中…</p>

    <section v-if="status" class="github-card">
      <h2>接続状態</h2>
      <p class="github-hint">
        <strong>{{ stateLabel[status.state] }}</strong>
        <template v-if="status.lastConnectedAt"> ・最終接続: {{ formatTimestamp(status.lastConnectedAt) }}</template>
        <template v-if="status.nextAttemptAt"> ・次の接続試行: {{ formatTimestamp(status.nextAttemptAt) }}</template>
      </p>
      <p v-if="status.lastError" class="github-error">{{ status.lastError }}</p>
    </section>

    <section class="github-card">
      <h2>上位サーバへの接続</h2>
      <div class="github-form-row">
        <label>
          <input v-model="enabled" type="checkbox" />
          有効にする
        </label>
      </div>
      <div class="github-form-row">
        <label>上位サーバURL
          <input v-model="upstreamUrl" type="text" placeholder="ws://upper-host:3101/api/relay/connect" :disabled="saving" />
        </label>
      </div>
      <div class="github-form-row">
        <label>Node ID
          <input v-model="nodeId" type="text" placeholder="linux-dev" :disabled="saving" />
        </label>
        <label>Node Name
          <input v-model="nodeName" type="text" placeholder="Linux dev box" :disabled="saving" />
        </label>
      </div>
      <div class="github-form-row">
        <label>Node Token（任意）
          <input v-model="nodeToken" type="password" placeholder="上位サーバの「ノードを追加」で発行されたトークン" :disabled="saving" />
        </label>
      </div>
      <div class="github-form-actions">
        <button type="button" :disabled="saving || (enabled && (!upstreamUrl.trim() || !nodeId.trim()))" @click="save">保存</button>
        <span v-if="saved" class="github-hint">保存しました</span>
      </div>
    </section>
  </div>
</template>
