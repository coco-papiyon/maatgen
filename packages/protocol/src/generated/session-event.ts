/* eslint-disable */
/**
 * Generated from packages/protocol/schema. Do not edit by hand.
 * Run `corepack pnpm --filter @maatgen/protocol generate` after changing a schema.
 */

export interface SessionEvent {
  id: string;
  sessionId: string;
  runId?: string;
  sequence: number;
  timestamp: string;
  schemaVersion: 1;
  source: 'user' | 'codex' | 'manager';
  type:
    | 'user_prompt'
    | 'assistant_message'
    | 'reasoning_summary'
    | 'command_started'
    | 'command_completed'
    | 'file_change_reported'
    | 'usage_reported'
    | 'change_detected'
    | 'checkpoint_created'
    | 'change_restored'
    | 'run_started'
    | 'run_completed'
    | 'run_failed'
    | 'run_cancelled'
    | 'error';
  data: unknown;
}
