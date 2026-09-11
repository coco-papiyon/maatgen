import type { RelayNode, UpstreamStatus } from '@maatgen/protocol';
import { computed, ref } from 'vue';
import { setApiBasePath, type AgentApi } from './api';

export const nodes = ref<RelayNode[]>([]);
export const selectedNodeId = ref('local');
export const selectedNode = computed(() => nodes.value.find((node) => node.id === selectedNodeId.value));
export const upstreamNodeId = 'upstream';
let configuredUpstream: UpstreamStatus | undefined;

function presentNodes(listedNodes: RelayNode[]): RelayNode[] {
  const upstreamName = configuredUpstream?.config.nodeName.trim();
  const presented = listedNodes.filter((node) => (
    node.id !== upstreamNodeId && (!upstreamName || node.id === 'local' || node.name !== upstreamName || node.status !== 'pending')
  ));
  if (upstreamName) {
    presented.push({
      id: upstreamNodeId,
      name: upstreamName,
      status: configuredUpstream?.state === 'connected' ? 'connected' : 'disconnected',
      createdAt: '',
      ...(configuredUpstream?.lastConnectedAt ? { connectedAt: configuredUpstream.lastConnectedAt } : {}),
    });
  }
  return presented;
}

export async function refreshNodes(api: AgentApi): Promise<void> {
  const listedNodes = await api.listNodes();
  try {
    configuredUpstream = await api.getUpstreamStatus();
  } catch {
    // Keep the last known upstream state while its status endpoint is transiently unavailable.
  }
  nodes.value = presentNodes(listedNodes);
}

export async function initializeNodes(api: AgentApi): Promise<void> {
  await refreshNodes(api);
  const requestedNode = new URLSearchParams(window.location.search).get('node');
  const retainedNode = nodes.value.some((node) => node.id === selectedNodeId.value) ? selectedNodeId.value : 'local';
  const initialNode = requestedNode && nodes.value.some((node) => node.id === requestedNode) ? requestedNode : retainedNode;
  selectedNodeId.value = initialNode;
  setApiBasePath(nodeApiBasePath(initialNode));
}

export function selectNode(nodeId: string): void {
  if (nodeId === selectedNodeId.value) return;
  selectedNodeId.value = nodeId;
  setApiBasePath(nodeApiBasePath(nodeId));

  const url = new URL(window.location.href);
  if (nodeId === 'local') url.searchParams.delete('node');
  else url.searchParams.set('node', nodeId);
  window.history.replaceState(window.history.state, '', url);
}

export function setConfiguredUpstream(status: UpstreamStatus): void {
  configuredUpstream = status;
  nodes.value = presentNodes(nodes.value);
  if (nodes.value.length > 0 && !nodes.value.some((node) => node.id === selectedNodeId.value)) {
    selectNode('local');
  }
}

function nodeApiBasePath(nodeId: string): string {
  if (nodeId === 'local') return '';
  if (nodeId === upstreamNodeId) return '/api/upstream';
  return `/api/nodes/${encodeURIComponent(nodeId)}`;
}
