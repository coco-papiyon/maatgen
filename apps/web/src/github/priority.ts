import type { GitHubJobPriority } from '@maatgen/protocol';

const PRIORITY_LABELS: Record<GitHubJobPriority, string> = {
  high: '高',
  medium: '中',
  low: '低',
};

export function priorityLabel(priority: GitHubJobPriority): string {
  return PRIORITY_LABELS[priority] ?? priority;
}
