<script setup lang="ts">
import type { RelayNode } from '@maatgen/protocol';
import { onBeforeUnmount, onMounted, ref } from 'vue';
import { useAgentApi } from './github/useAgentApi';
import { initializeNodes, isUpstreamNodeId, nodes, refreshNodes, selectedNode, selectedNodeId, selectNode } from './nodes';

const api = useAgentApi();
const open = ref(false);
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

async function deleteNode(nodeId: string) {
  await api.deleteNode(nodeId);
  await refreshNodes(api);
}

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
          v-if="node.id !== 'local' && !isUpstreamNodeId(node.id) && node.status !== 'connected'"
          type="button"
          class="node-option-delete"
          title="履歴から削除"
          aria-label="履歴から削除"
          @click.stop="deleteNode(node.id)"
        >×</button>
      </li>
    </ul>
  </div>
</template>
