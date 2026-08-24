/* eslint-disable */
/**
 * Generated from packages/protocol/schema. Do not edit by hand.
 * Run `corepack pnpm --filter @maatgen/protocol generate` after changing a schema.
 */

export interface SessionListResponse {
  sessions: AgentSession[];
  nextCursor?: string;
}
export interface AgentSession {
  id: string;
  agent: 'codex' | 'claude' | 'copilot';
  workspace: string;
  agentThreadId?: string;
  status: 'active' | 'closed';
  triggerSource: 'manual' | 'github_monitor';
  githubMonitorEvent?: string;
  githubRuleId?: string;
  githubItemKind?: 'issue' | 'pull_request';
  githubItemNumber?: number;
  createdAt: string;
  closedAt?: string;
}
