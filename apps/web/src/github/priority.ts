import type { GitHubJobPriority } from '@maatgen/protocol';

export function priorityLabel(priority: GitHubJobPriority): string {
  return priority;
}
