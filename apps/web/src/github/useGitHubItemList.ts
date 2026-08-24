import { onMounted, ref, watch } from 'vue';
import type { GitHubItem } from '@maatgen/protocol';
import { AgentApiError, type GitHubItemQuery } from '../api';
import { useAgentApi } from './useAgentApi';
import { githubWorkspace } from './workspace';

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
    if (!githubWorkspace.value) {
      items.value = [];
      return;
    }
    loading.value = true;
    error.value = '';
    try {
      const response = kind === 'issue'
        ? await api.listGitHubIssues(githubWorkspace.value, buildQuery())
        : await api.listGitHubPullRequests(githubWorkspace.value, buildQuery());
      items.value = response.items;
      projectsUnavailable.value = response.projectsUnavailable ?? false;
      fetchedAt.value = response.fetchedAt;
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

  watch(githubWorkspace, () => void refresh());
  onMounted(() => void refresh());

  return {
    items, loading, error, projectsUnavailable, fetchedAt,
    state, assignee, author, labelsText, text, project, status,
    refresh,
  };
}
