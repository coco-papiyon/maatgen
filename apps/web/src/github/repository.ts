import { ref, watch } from 'vue';
import type { AgentApi } from '../api';
import { githubWorkspace } from './workspace';
import { refreshRepositories, selectedRepository } from './repositories';

export type GitHubRepositoryStatus = 'idle' | 'resolving' | 'resolved' | 'ambiguous' | 'unavailable';

// "github.com/owner/name", kept in sync with githubWorkspace. Displayed in
// the shell's common navigation (top-right) and usable by any view that
// wants it without re-resolving.
export const githubRepositoryLabel = ref('');
export const githubRepositoryStatus = ref<GitHubRepositoryStatus>('idle');

// Resolves githubWorkspace's GitHub remote whenever it changes, and — the
// first time a repository resolves with no monitor registered yet —
// auto-creates one so monitoring, the Issue/PR lists, and periodic
// polling all "just work" for whichever repository the current Session is
// backed by, without a separate manual setup step. Registering the
// monitor is still an ordinary, visible CreateGitHubMonitorRequest call
// (ADR-007's persistence and audit model is unchanged); this just
// triggers it automatically instead of requiring a trip to the Settings
// screen first. The Settings screen remains the place to adjust the poll
// interval, disable monitoring, or pick a different remote.
//
// A resolved repository also becomes the top-right selector's default
// selection (selectedRepository, github/repositories.ts) — switching
// Sessions follows you to that repository's Issues/PRs/Events, but the
// selector can still be changed independently to browse a different
// registered repository without switching Sessions.
//
// Call this from Shell.vue's setup (it needs a concrete AgentApi instance,
// which is only available via inject() inside a component). Vue ties the
// watcher this creates to Shell's own effect scope, so it is torn down
// automatically when Shell unmounts — safe to call once per Shell
// instance; Shell is the app root, so in production that's once, ever.
export function watchGitHubRepository(api: AgentApi): void {
  watch(
    githubWorkspace,
    async (workspace) => {
      if (!workspace) {
        githubRepositoryLabel.value = '';
        githubRepositoryStatus.value = 'idle';
        return;
      }
      githubRepositoryStatus.value = 'resolving';
      try {
        const resolution = await api.resolveGitHubRepository(workspace);
        if (!resolution.selected) {
          githubRepositoryLabel.value = '';
          githubRepositoryStatus.value = resolution.candidates.length > 1 ? 'ambiguous' : 'unavailable';
          return;
        }
        githubRepositoryLabel.value = `${resolution.selected.host}/${resolution.selected.owner}/${resolution.selected.name}`;
        githubRepositoryStatus.value = 'resolved';
        if (!resolution.monitor) {
          try {
            await api.createGitHubMonitor({
              workspace,
              remoteName: resolution.selected.remoteName,
              pollIntervalSeconds: 300,
            });
          } catch {
            // Best-effort: e.g. the GitHub token isn't configured yet. The
            // Settings screen surfaces the same resolution and lets the
            // user retry once it is.
          }
        }
        await refreshRepositories(api);
        selectedRepository.value = resolution.repository;
      } catch {
        githubRepositoryLabel.value = '';
        githubRepositoryStatus.value = 'unavailable';
      }
    },
    { immediate: true },
  );
}
