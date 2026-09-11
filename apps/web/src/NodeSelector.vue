<script setup lang="ts">
import type { RelayNode } from '@maatgen/protocol';
import { onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useAgentApi } from './github/useAgentApi';
import { initializeNodes, nodes, refreshNodes, selectedNode, selectedNodeId, selectNode, upstreamNodeId } from './nodes';

const api = useAgentApi();
const open = ref(false);
const addDialogOpen = ref(false);
const addNodeName = ref('');
const addNodeBusy = ref(false);
const addNodeError = ref('');
const addNodeResult = ref<RelayNode>();
const commandCopied = ref(false);
let pollTimer: number | undefined;

function nodeStatusIcon(status: RelayNode['status']): string {
  if (status === 'connected') return '●';
  if (status === 'pending') return '◐';
  return '○';
}

function nodeStatusLabel(status: RelayNode['status']): string {
  if (status === 'connected') return '接続中';
  if (status === 'pending') return '登録待ち';
  return '切断中';
}

function chooseNode(nodeId: string) {
  open.value = false;
  selectNode(nodeId);
}

function showAddDialog() {
  open.value = false;
  addDialogOpen.value = true;
  addNodeName.value = '';
  addNodeError.value = '';
  addNodeResult.value = undefined;
  commandCopied.value = false;
}

function closeAddDialog() {
  addDialogOpen.value = false;
}

async function submitAddNode() {
  const name = addNodeName.value.trim();
  if (!name || addNodeBusy.value) return;
  addNodeBusy.value = true;
  addNodeError.value = '';
  try {
    addNodeResult.value = await api.createNode(name);
    await refreshNodes(api);
  } catch (cause) {
    addNodeError.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    addNodeBusy.value = false;
  }
}

async function copyCommand() {
  if (!addNodeResult.value?.startupCommand) return;
  await navigator.clipboard.writeText(addNodeResult.value.startupCommand);
  commandCopied.value = true;
  window.setTimeout(() => { commandCopied.value = false; }, 1500);
}

async function deleteNode(nodeId: string) {
  await api.deleteNode(nodeId);
  await refreshNodes(api);
}

watch(nodes, (updated) => {
  if (!addDialogOpen.value || !addNodeResult.value) return;
  const match = updated.find((node) => node.id === addNodeResult.value?.id);
  if (match?.status === 'connected') {
    addDialogOpen.value = false;
    selectNode(match.id);
  }
});

onMounted(async () => {
  try {
    await initializeNodes(api);
  } catch {
    // The selector remains visible with its Local fallback when listing fails.
  }
  pollTimer = window.setInterval(() => void refreshNodes(api).catch(() => undefined), 5_000);
});

onBeforeUnmount(() => window.clearInterval(pollTimer));
</script>

<template>
  <div class="node-selector">
    <button
      type="button"
      class="node-selector-toggle"
      :class="selectedNode?.status ?? 'connected'"
      aria-haspopup="listbox"
      :aria-expanded="open"
      @click="open = !open"
    >
      <span class="node-status-dot" :class="selectedNode?.status ?? 'connected'">{{ nodeStatusIcon(selectedNode?.status ?? 'connected') }}</span>
      Server: {{ selectedNode?.name ?? 'Local' }}
    </button>
    <ul v-if="open" class="node-selector-list" role="listbox">
      <li v-for="node in nodes" :key="node.id" role="option" :aria-selected="node.id === selectedNodeId">
        <button type="button" class="node-option" @click="chooseNode(node.id)">
          <span class="node-status-dot" :class="node.status">{{ nodeStatusIcon(node.status) }}</span>
          <span class="node-option-name">{{ node.name }}</span>
          <span v-if="node.status !== 'connected'" class="node-option-status">({{ nodeStatusLabel(node.status) }})</span>
        </button>
        <button
          v-if="node.id !== 'local' && node.id !== upstreamNodeId && node.status !== 'connected'"
          type="button"
          class="node-option-delete"
          title="履歴から削除"
          aria-label="履歴から削除"
          @click.stop="deleteNode(node.id)"
        >×</button>
      </li>
      <li class="node-selector-divider" role="presentation" />
      <li role="presentation"><button type="button" class="node-option node-option-add" @click="showAddDialog">＋ ノードを追加</button></li>
    </ul>
  </div>

  <div v-if="addDialogOpen" class="usage-summary-overlay" @click.self="closeAddDialog">
    <div class="usage-summary-modal add-node-modal" role="dialog" aria-modal="true" aria-label="ノードを追加">
      <header class="usage-summary-modal-header">
        <h2>ノードを追加</h2>
        <button type="button" class="icon-button" aria-label="閉じる" @click="closeAddDialog">×</button>
      </header>
      <template v-if="!addNodeResult">
        <label class="add-node-name-field">表示名
          <input v-model="addNodeName" type="text" placeholder="Linux dev box" :disabled="addNodeBusy" @keydown.enter="submitAddNode" />
        </label>
        <div v-if="addNodeError" class="error-banner" role="alert">{{ addNodeError }}</div>
        <div class="add-node-actions">
          <button type="button" class="quiet-button" :disabled="addNodeBusy" @click="closeAddDialog">キャンセル</button>
          <button type="button" :disabled="addNodeBusy || !addNodeName.trim()" @click="submitAddNode">追加</button>
        </div>
      </template>
      <template v-else>
        <p>下記コマンドをこのノードの端末で実行してください。</p>
        <div class="add-node-command-row">
          <code class="add-node-command">{{ addNodeResult.startupCommand }}</code>
          <button type="button" class="quiet-button" @click="copyCommand">{{ commandCopied ? 'Copied' : 'コピー' }}</button>
        </div>
        <p class="add-node-waiting">接続を待っています…（接続され次第、自動的に閉じます）</p>
        <div class="add-node-actions"><button type="button" class="quiet-button" @click="closeAddDialog">閉じる</button></div>
      </template>
    </div>
  </div>
</template>
