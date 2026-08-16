import type { AgentRun as GeneratedAgentRun } from './generated/agent-run.js';
import type { AgentSession as GeneratedAgentSession } from './generated/agent-session.js';
import type { ApiErrorResponse as GeneratedApiErrorResponse } from './generated/api-error.js';
import type {
  ChangeHunk as GeneratedChangeHunk,
  ChangeSet as GeneratedChangeSet,
  FileChange as GeneratedFileChange,
  HunkStatus as GeneratedHunkStatus,
  RestoreStatus as GeneratedRestoreStatus,
} from './generated/change-set.js';
import type { CreateSessionRequest as GeneratedCreateSessionRequest } from './generated/create-session-request.js';
import type { CommandApproval as GeneratedCommandApproval } from './generated/command-approval.js';
import type { EventListResponse as GeneratedEventListResponse } from './generated/event-list.js';
import type { ProviderListResponse as GeneratedProviderListResponse } from './generated/provider-list.js';
import type { SendMessageRequest as GeneratedSendMessageRequest } from './generated/send-message-request.js';
import type { SessionEvent as GeneratedSessionEvent } from './generated/session-event.js';
import type { SessionListResponse as GeneratedSessionListResponse } from './generated/session-list.js';
import type { TokenUsage as GeneratedTokenUsage } from './generated/token-usage.js';
import type { WsTicketResponse as GeneratedWsTicketResponse } from './generated/ws-ticket.js';

export const SCHEMA_VERSION = 2 as const;

export type AgentSession = GeneratedAgentSession;
export type AgentName = AgentSession['agent'];
export type SessionStatus = AgentSession['status'];

export type AgentRun = GeneratedAgentRun;
export type RunStatus = AgentRun['status'];

export type TokenUsage = GeneratedTokenUsage;
export type CommandApproval = GeneratedCommandApproval;
export type ApprovalDecision = NonNullable<CommandApproval['decision']>;
export interface ApprovalListResponse { approvals: CommandApproval[]; }
export interface ApprovalDecisionRequest { decision: ApprovalDecision; ruleArgv?: string[]; }

export interface RunUsageEntry {
  run: AgentRun;
  usage?: TokenUsage;
}

export interface SessionUsage {
  sessionId: string;
  summary: TokenUsage;
  runs: RunUsageEntry[];
}

export type EventSource = GeneratedSessionEvent['source'];
export type SessionEventType = GeneratedSessionEvent['type'];
export type SessionEvent<TData = unknown> = Omit<GeneratedSessionEvent, 'data'> & { data: TData };

export type ChangeSet = GeneratedChangeSet;
export type FileChange = GeneratedFileChange;
export type ChangeHunk = GeneratedChangeHunk;
export type FileChangeKind = FileChange['kind'];
export type RestoreStatus = GeneratedRestoreStatus;
export type HunkStatus = GeneratedHunkStatus;

export type CreateSessionRequest = GeneratedCreateSessionRequest;
export type SendMessageRequest = GeneratedSendMessageRequest;
export type ApiErrorResponse = GeneratedApiErrorResponse;
export type ApiErrorBody = ApiErrorResponse['error'];
export type SessionListResponse = GeneratedSessionListResponse;
export type EventListResponse = GeneratedEventListResponse;
export type ProviderListResponse = GeneratedProviderListResponse;
export type Provider = ProviderListResponse['providers'][number];
export type WsTicketResponse = GeneratedWsTicketResponse;
