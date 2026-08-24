/* eslint-disable */
/**
 * Generated from packages/protocol/schema. Do not edit by hand.
 * Run `corepack pnpm --filter @maatgen/protocol generate` after changing a schema.
 */

export interface CreateSessionRequest {
  agent: 'codex' | 'claude' | 'copilot';
  workspace: string;
  triggerSource?: 'manual' | 'github_monitor';
  githubMonitorEvent?: string;
  githubRuleId?: string;
  githubItemKind?: 'issue' | 'pull_request';
  githubItemNumber?: number;
}
