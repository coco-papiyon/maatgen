import { ref, watch } from 'vue';
import type { GitHubItem, GitHubMonitorEvent } from '@maatgen/protocol';
import { AgentApiError } from '../api';
import { selectedNodeId } from '../nodes';
import { useAgentApi } from './useAgentApi';
import { selectedRepository } from './repositories';

// Backs GitHubIssueDetailView / GitHubPullDetailView. The item itself is
// fetched fresh from GitHub (ADR-007 section 2, "画面表示取得"); related
// monitor events come from Maatgen's own history and are matched by
// (kind, number) since there is no dedicated "events for this item"
// endpoint yet — acceptable for the event volumes ADR-007 targets.
export function useGitHubItemDetail(kind: 'issue' | 'pull_request', number: () => number) {
  const api = useAgentApi();
  const item = ref<GitHubItem>();
  const relatedEvents = ref<GitHubMonitorEvent[]>([]);
  const loading = ref(false);
  const error = ref('');
  let refreshSequence = 0;

  async function refresh() {
    const sequence = ++refreshSequence;
    const nodeId = selectedNodeId.value;
    const currentNumber = number();
    if (!selectedRepository.value || !Number.isFinite(currentNumber)) {
      item.value = undefined;
      relatedEvents.value = [];
      return;
    }
    loading.value = true;
    error.value = '';
    try {
      const [fetchedItem, events] = await Promise.all([
        kind === 'issue'
          ? api.getGitHubIssue(selectedRepository.value, currentNumber)
          : api.getGitHubPullRequest(selectedRepository.value, currentNumber),
        api.listGitHubMonitorEvents(selectedRepository.value, 200).catch(() => []),
      ]);
      if (sequence !== refreshSequence || nodeId !== selectedNodeId.value) return;
      item.value = fetchedItem;
      relatedEvents.value = events.filter((event) => event.kind === kind && event.number === currentNumber);
    } catch (cause) {
      if (sequence !== refreshSequence || nodeId !== selectedNodeId.value) return;
      error.value = describeError(cause);
    } finally {
      if (sequence === refreshSequence) loading.value = false;
    }
  }

  function describeError(cause: unknown): string {
    if (cause instanceof AgentApiError) return cause.message;
    return cause instanceof Error ? cause.message : String(cause);
  }

  watch([selectedRepository, number, selectedNodeId], () => {
    item.value = undefined;
    relatedEvents.value = [];
    void refresh();
  }, { immediate: true });

  return { item, relatedEvents, loading, error, refresh };
}
