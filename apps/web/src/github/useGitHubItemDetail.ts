import { onMounted, ref, watch } from 'vue';
import type { GitHubItem, GitHubMonitorEvent } from '@maatgen/protocol';
import { AgentApiError } from '../api';
import { useAgentApi } from './useAgentApi';
import { githubWorkspace } from './workspace';

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

  async function refresh() {
    const currentNumber = number();
    if (!githubWorkspace.value || !Number.isFinite(currentNumber)) {
      item.value = undefined;
      relatedEvents.value = [];
      return;
    }
    loading.value = true;
    error.value = '';
    try {
      const [fetchedItem, events] = await Promise.all([
        kind === 'issue'
          ? api.getGitHubIssue(githubWorkspace.value, currentNumber)
          : api.getGitHubPullRequest(githubWorkspace.value, currentNumber),
        api.listGitHubMonitorEvents(githubWorkspace.value, 200).catch(() => []),
      ]);
      item.value = fetchedItem;
      relatedEvents.value = events.filter((event) => event.kind === kind && event.number === currentNumber);
    } catch (cause) {
      error.value = describeError(cause);
    } finally {
      loading.value = false;
    }
  }

  function describeError(cause: unknown): string {
    if (cause instanceof AgentApiError) return cause.message;
    return cause instanceof Error ? cause.message : String(cause);
  }

  watch([githubWorkspace, number], () => void refresh());
  onMounted(() => void refresh());

  return { item, relatedEvents, loading, error, refresh };
}
