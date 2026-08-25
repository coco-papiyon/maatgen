import { computed, ref } from 'vue';
import type { GitHubRepositoryMonitor } from '@maatgen/protocol';
import type { AgentApi } from '../api';

// The full multi-repository registry (Settings screen's table) and the one
// repository currently selected for the GitHub views that can only show one
// repository at a time (Issue一覧, PR一覧, and the top-right selector in
// Shell.vue; the Job view shows every repository at once and does not read
// selectedRepository). Module-level singletons so every view and the
// common navigation stay in sync without prop drilling.
export const repositories = ref<GitHubRepositoryMonitor[]>([]);
export const selectedRepository = ref('');

export async function refreshRepositories(api: AgentApi): Promise<void> {
  repositories.value = await api.listGitHubMonitors();
  if (selectedRepository.value && !repositories.value.some((monitor) => monitor.repository === selectedRepository.value)) {
    selectedRepository.value = '';
  }
  const first = repositories.value[0];
  if (!selectedRepository.value && first) {
    selectedRepository.value = first.repository;
  }
}

export interface RemoteGroup {
  key: string;
  host: string;
  owner: string;
  name: string;
  repositories: string[];
}

// Repositories that share the same GitHub remote (e.g. two local clones of
// the same repository) collapse into a single dropdown entry, keyed on
// host/owner/name.
export const remoteGroups = computed<RemoteGroup[]>(() => {
  const groups = new Map<string, RemoteGroup>();
  for (const monitor of repositories.value) {
    const key = `${monitor.host}/${monitor.owner}/${monitor.name}`;
    const group = groups.get(key);
    if (group) {
      group.repositories.push(monitor.repository);
    } else {
      groups.set(key, { key, host: monitor.host, owner: monitor.owner, name: monitor.name, repositories: [monitor.repository] });
    }
  }
  return [...groups.values()];
});

export const selectedRemoteKey = computed(() => {
  const monitor = repositories.value.find((candidate) => candidate.repository === selectedRepository.value);
  return monitor ? `${monitor.host}/${monitor.owner}/${monitor.name}` : '';
});

export function selectRemote(key: string): void {
  const group = remoteGroups.value.find((candidate) => candidate.key === key);
  const first = group?.repositories[0];
  if (first) {
    selectedRepository.value = first;
  }
}
