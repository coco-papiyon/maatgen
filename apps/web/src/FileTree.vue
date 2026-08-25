<script setup lang="ts">
import { reactive, ref } from 'vue';
import type { WorkspaceFileNode } from './api';

const props = withDefaults(defineProps<{
  nodes: WorkspaceFileNode[];
  selectedPath?: string;
  loadChildren: (path: string) => Promise<WorkspaceFileNode[]>;
}>(), { selectedPath: '' });
const emit = defineEmits<{ select: [path: string] }>();

const expanded = ref<Set<string>>(new Set());
const loading = ref<Set<string>>(new Set());
const childrenByPath = reactive<Record<string, WorkspaceFileNode[]>>({});

async function toggle(node: WorkspaceFileNode) {
  if (expanded.value.has(node.path)) {
    expanded.value.delete(node.path);
    return;
  }
  expanded.value.add(node.path);
  if (childrenByPath[node.path] || !node.hasChildren) return;
  loading.value.add(node.path);
  try {
    childrenByPath[node.path] = await props.loadChildren(node.path);
  } finally {
    loading.value.delete(node.path);
  }
}
</script>

<template>
  <ul class="file-tree">
    <li v-for="node in nodes" :key="node.path">
      <template v-if="node.type === 'dir'">
        <button type="button" class="file-tree-dir" :class="{ open: expanded.has(node.path) }" @click="toggle(node)">
          <span class="file-tree-caret">{{ node.hasChildren ? (expanded.has(node.path) ? '▾' : '▸') : '·' }}</span>{{ node.name }}
        </button>
        <p v-if="expanded.has(node.path) && loading.has(node.path)" class="file-tree-loading">読み込み中…</p>
        <FileTree
          v-else-if="expanded.has(node.path) && childrenByPath[node.path]?.length"
          :nodes="childrenByPath[node.path]!"
          :selected-path="props.selectedPath"
          :load-children="props.loadChildren"
          @select="(path) => emit('select', path)"
        />
      </template>
      <button v-else type="button" class="file-tree-file" :class="{ selected: node.path === props.selectedPath }" @click="emit('select', node.path)">
        {{ node.name }}
      </button>
    </li>
  </ul>
</template>
