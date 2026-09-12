import { ref, watch } from 'vue';
import type { GitHubItem } from '@maatgen/protocol';
import { AgentApiError, type GitHubItemQuery } from '../api';
import { selectedNodeId } from '../nodes';
import { useAgentApi } from './useAgentApi';
import { selectedRepository } from './repositories';

// Backs both GitHubIssuesView and GitHubPullsView: both are "画面表示取得"
// screens (ADR-007 section 2) that fetch fresh from GitHub on every filter
// change and never persist the result. The filter set matches the
// controller's ItemQuery (apps/agent-manager/internal/githubcontroller) —
// simple structured fields rather than the full GitHub-style search-text
// grammar ADR-007 section 7 describes, which is deferred past this v1.
export function useGitHubItemList(kind: 'issue' | 'pull_request') {
  const api = useAgentApi();
  const items = ref<GitHubItem[]>([]);
  const loading = ref(false);
  const error = ref('');
  const projectsUnavailable = ref(false);
  const fetchedAt = ref('');

  const state = ref<'open' | 'closed' | 'all'>('open');
  const assignee = ref('');
  const author = ref('');
  const labelsText = ref('');
  const text = ref('');
  const project = ref('');
  const status = ref('');
  let refreshSequence = 0;

  function buildQuery(): GitHubItemQuery {
    const labels = labelsText.value.split(',').map((label) => label.trim()).filter(Boolean);
    return {
      state: state.value,
      ...(assignee.value ? { assignee: assignee.value } : {}),
      ...(author.value ? { author: author.value } : {}),
      ...(labels.length ? { labels } : {}),
      ...(text.value ? { text: text.value } : {}),
      ...(project.value ? { project: project.value } : {}),
      ...(status.value ? { status: status.value } : {}),
    };
  }

  async function refresh() {
    const sequence = ++refreshSequence;
    const nodeId = selectedNodeId.value;
    if (!selectedRepository.value) {
      items.value = [];
      fetchedAt.value = '';
      return;
    }
    loading.value = true;
    error.value = '';
    try {
      const response = kind === 'issue'
        ? await api.listGitHubIssues(selectedRepository.value, buildQuery())
        : await api.listGitHubPullRequests(selectedRepository.value, buildQuery());
      if (sequence !== refreshSequence || nodeId !== selectedNodeId.value) return;
      items.value = response.items;
      projectsUnavailable.value = response.projectsUnavailable ?? false;
      fetchedAt.value = response.fetchedAt;
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

  watch([selectedRepository, selectedNodeId], () => {
    items.value = [];
    fetchedAt.value = '';
    void refresh();
  }, { immediate: true });

  return {
    items, loading, error, projectsUnavailable, fetchedAt,
    state, assignee, author, labelsText, text, project, status,
    refresh,
  };
}
