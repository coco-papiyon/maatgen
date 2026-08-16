/* eslint-disable */
/**
 * Generated from packages/protocol/schema. Do not edit by hand.
 * Run `corepack pnpm --filter @maatgen/protocol generate` after changing a schema.
 */

export interface CommandApproval {
  id: string;
  sessionId: string;
  runId: string;
  providerRequestId: string;
  command: string;
  shell: string;
  workingDirectory: string;
  segments: {
    index: number;
    command: string;
    argv: string[];
  }[];
  status: 'pending' | 'approved' | 'denied' | 'cancelled' | 'expired';
  risk?: 'safe' | 'low' | 'high' | 'critical';
  confidence?: number;
  summary?: string;
  factors: string[];
  decision?: 'allow_once' | 'allow_session' | 'allow_permanent' | 'deny';
  scope?: 'once' | 'session' | 'permanent';
  source?: 'config' | 'ai' | 'human' | 'system';
  ruleArgv?: string[];
  createdAt: string;
  decidedAt?: string;
}
