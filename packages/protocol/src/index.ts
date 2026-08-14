export const SCHEMA_VERSION = 1 as const;

export type AgentName = 'codex';

export type SessionStatus = 'active' | 'closed';

export type RunStatus =
  | 'queued'
  | 'starting'
  | 'running'
  | 'completed'
  | 'failed'
  | 'cancelled';

export interface AgentSession {
  id: string;
  agent: AgentName;
  workspace: string;
  worktree: string;
  baseCommit: string;
  codexThreadId?: string;
  status: SessionStatus;
  createdAt: string;
  closedAt?: string;
}

export interface AgentRun {
  id: string;
  sessionId: string;
  status: RunStatus;
  prompt: string;
  startedAt?: string;
  finishedAt?: string;
  exitCode?: number;
}

export interface TokenUsage {
  inputTokens?: number;
  cachedInputTokens?: number;
  outputTokens?: number;
  reasoningOutputTokens?: number;
  totalTokens?: number;
  source: 'cli' | 'unknown';
}

export type EventSource = 'user' | 'codex' | 'manager';

export type SessionEventType =
  | 'user_prompt'
  | 'assistant_message'
  | 'reasoning_summary'
  | 'command_started'
  | 'command_completed'
  | 'file_change_reported'
  | 'usage_reported'
  | 'change_detected'
  | 'change_reviewed'
  | 'run_started'
  | 'run_completed'
  | 'run_failed'
  | 'run_cancelled'
  | 'error';

export interface SessionEvent<TData = unknown> {
  id: string;
  sessionId: string;
  runId?: string;
  sequence: number;
  timestamp: string;
  schemaVersion: typeof SCHEMA_VERSION;
  source: EventSource;
  type: SessionEventType;
  data: TData;
}

export type FileChangeKind =
  | 'modify'
  | 'add'
  | 'delete'
  | 'rename'
  | 'binary'
  | 'mode_change';

export type ReviewStatus =
  | 'pending'
  | 'partially_accepted'
  | 'accepted'
  | 'rejected';

export interface ChangeHunk {
  id: string;
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  originalText: string;
  modifiedText: string;
  status: Exclude<ReviewStatus, 'partially_accepted'>;
}

export interface FileChange {
  id: string;
  oldPath?: string;
  newPath?: string;
  kind: FileChangeKind;
  original?: string;
  modified?: string;
  reviewMode: 'hunk' | 'file';
  status: ReviewStatus;
  hunks: ChangeHunk[];
}

export interface ChangeSet {
  sessionId: string;
  files: FileChange[];
}
