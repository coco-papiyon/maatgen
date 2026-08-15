/* eslint-disable */
/**
 * Generated from packages/protocol/schema. Do not edit by hand.
 * Run `corepack pnpm --filter @maatgen/protocol generate` after changing a schema.
 */

export interface AgentSession {
  id: string;
  agent: 'codex';
  workspace: string;
  worktree: string;
  baseCommit: string;
  codexThreadId?: string;
  status: 'active' | 'closed';
  createdAt: string;
  closedAt?: string;
  cleanupStatus?: 'not_started' | 'pending' | 'completed' | 'failed';
  cleanupError?: string;
  cleanupAttempts?: number;
  cleanupUpdatedAt?: string;
}
