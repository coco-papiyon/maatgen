/* eslint-disable */
/**
 * Generated from packages/protocol/schema. Do not edit by hand.
 * Run `corepack pnpm --filter @maatgen/protocol generate` after changing a schema.
 */

export interface AgentRun {
  id: string;
  sessionId: string;
  status: 'queued' | 'starting' | 'running' | 'waiting_for_approval' | 'completed' | 'failed' | 'cancelled';
  prompt: string;
  startedAt?: string;
  finishedAt?: string;
  exitCode?: number;
  autoRetryOfRunId?: string;
  usageLimitRetryPendingAt?: string;
}
