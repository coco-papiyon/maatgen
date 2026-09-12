import type { RelayNode, UpstreamStatus } from '@maatgen/protocol';
import { computed, ref } from 'vue';
import { setApiBasePath, type AgentApi } from './api';

export const nodes = ref<RelayNode[]>([]);
export const selectedNodeId = ref('local');
export const selectedNode = computed(() => nodes.value.find((node) => node.id === selectedNodeId.value));

// Each configured upstream (ADR-009, extended to allow several upper nodes
// at once) is presented in the selector as its own virtual RelayNode, keyed
// by this prefix plus the upstream's own id so it never collides with a
// downstream node id from /api/nodes.
const upstreamNodePrefix = 'upstream:';

export function upstreamNodeId(upstreamId: string): string {
  return upstreamNodePrefix + upstreamId;
}

export function isUpstreamNodeId(nodeId: string): boolean {
  return nodeId.startsWith(upstreamNodePrefix);
}

export function upstreamIdFromNodeId(nodeId: string): string {
  return nodeId.slice(upstreamNodePrefix.length);
}

let configuredUpstreams: UpstreamStatus[] = [];

function presentNodes(listedNodes: RelayNode[]): RelayNode[] {
  const upstreamNames = new Set(
    configuredUpstreams.map((upstream) => upstream.config.nodeName.trim()).filter((name) => name !== ''),
  );
  const presented = listedNodes.filter((node) => (
    !isUpstreamNodeId(node.id) && (node.id === 'local' || node.status !== 'pending' || !upstreamNames.has(node.name))
  ));
  for (const upstream of configuredUpstreams) {
    const id = upstream.config.id;
    if (!id) continue;
    const name = upstream.config.nodeName.trim() || upstream.config.nodeId.trim();
    if (!name) continue;
    presented.push({
      id: upstreamNodeId(id),
      name,
      status: upstream.state === 'connected' ? 'connected' : 'disconnected',
      createdAt: '',
      ...(upstream.lastConnectedAt ? { connectedAt: upstream.lastConnectedAt } : {}),
    });
  }
  return presented;
}

export async function refreshNodes(api: AgentApi): Promise<void> {
  const listedNodes = await api.listNodes();
  try {
    configuredUpstreams = await api.listUpstreams();
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

function nodeApiBasePath(nodeId: string): string {
  if (nodeId === 'local') return '';
  if (isUpstreamNodeId(nodeId)) return `/api/upstreams/${encodeURIComponent(upstreamIdFromNodeId(nodeId))}`;
  return `/api/nodes/${encodeURIComponent(nodeId)}`;
}
