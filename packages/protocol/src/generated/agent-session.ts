/* eslint-disable */
/**
 * Generated from packages/protocol/schema. Do not edit by hand.
 * Run `corepack pnpm --filter @maatgen/protocol generate` after changing a schema.
 */

export interface AgentSession {
  id: string;
  agent: 'codex' | 'claude' | 'copilot';
  workspace: string;
  agentThreadId?: string;
  status: 'active' | 'closed';
  createdAt: string;
  closedAt?: string;
}
