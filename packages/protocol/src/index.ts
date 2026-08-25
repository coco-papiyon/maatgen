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
import type { UsageModelListResponse as GeneratedUsageModelListResponse } from './generated/usage-model-list.js';
import type { UsageProviderListResponse as GeneratedUsageProviderListResponse } from './generated/usage-provider-list.js';
import type {
  UsagePeriod as GeneratedUsagePeriod,
  UsageSeriesPoint as GeneratedUsageSeriesPoint,
  UsageSummary as GeneratedUsageSummary,
} from './generated/usage-summary.js';
import type { WsTicketResponse as GeneratedWsTicketResponse } from './generated/ws-ticket.js';

export const SCHEMA_VERSION = 2 as const;

export type AgentSession = GeneratedAgentSession;
export type AgentName = AgentSession['agent'];
export type SessionStatus = AgentSession['status'];

export type AgentRun = GeneratedAgentRun;
export type RunStatus = AgentRun['status'];

export type TokenUsage = GeneratedTokenUsage;
export type UsageSummary = GeneratedUsageSummary;
export type UsagePeriod = GeneratedUsagePeriod;
export type UsageSeriesPoint = GeneratedUsageSeriesPoint;
export type UsageModelListResponse = GeneratedUsageModelListResponse;
export type UsageProviderListResponse = GeneratedUsageProviderListResponse;
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

export interface ProviderUsageWindow {
  name: string;
  usedPercent: number;
  remainingPercent: number;
  resetLabel?: string;
}

export interface ProviderUsage {
  provider: AgentName;
  windows: ProviderUsageWindow[];
  fetchedAt: string;
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

// GitHub monitoring (ADR-007). These mirror apps/agent-manager/internal/protocol/github.go
// by hand, following the same precedent as SessionUsage/ProviderUsage above: request/response
// DTOs that don't need Ajv-validated golden fixtures are hand-written here rather than
// generated from a JSON Schema. GitHubObservedItem is deliberately not mirrored: it is
// monitoring-only internal state (ADR-007 section 3) and must never back a UI screen.

export type GitHubItemKind = 'issue' | 'pull_request';
export type GitHubItemState = 'open' | 'closed';
export type GitHubConcurrencyPolicy = 'skip' | 'coalesce';
export type GitHubJobPriority = 'high' | 'medium' | 'low';
export type GitHubMonitorEventStatus =
  | 'detected'
  | 'matched'
  | 'queued'
  | 'session_created'
  | 'run_started'
  | 'skipped'
  | 'completed'
  | 'failed'
  | 'cancelled';

export interface GitHubUser {
  login: string;
}

export interface GitHubLabel {
  name: string;
}

export interface GitHubMilestone {
  title: string;
}

export interface GitHubBranchRef {
  ref: string;
  sha: string;
}

export interface GitHubProjectFieldValue {
  projectTitle: string;
  fieldName: string;
  value: string;
}

export interface GitHubPullRequestDetails {
  draft: boolean;
  base: GitHubBranchRef;
  head: GitHubBranchRef;
  requestedReviewers: GitHubUser[];
}

export interface GitHubItem {
  kind: GitHubItemKind;
  number: number;
  title: string;
  body: string;
  state: GitHubItemState;
  author: GitHubUser;
  assignees: GitHubUser[];
  labels: GitHubLabel[];
  milestone?: GitHubMilestone;
  createdAt: string;
  updatedAt: string;
  url: string;
  pullRequest?: GitHubPullRequestDetails;
  projectFields?: GitHubProjectFieldValue[];
  projectsError?: string;
}

export interface GitHubRepositoryMonitor {
  repository: string;
  host: string;
  owner: string;
  name: string;
  remoteName: string;
  projectName?: string;
  enabled: boolean;
  pollIntervalSeconds: number;
  coalesceQueueLimit: number;
  lastSyncedAt?: string;
  nextSyncAt?: string;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
}

export interface GitHubProjectFilterCondition {
  projectTitle: string;
  fieldName: string;
  value: string;
}

export interface GitHubMonitorFilters {
  actions?: string[];
  numbers?: number[];
  titleContains?: string;
  bodyContains?: string;
  authors?: string[];
  assignees?: string[];
  reviewers?: string[];
  labels?: string[];
  milestones?: string[];
  states?: GitHubItemState[];
  draft?: boolean;
  baseBranches?: string[];
  headBranches?: string[];
  project?: GitHubProjectFilterCondition;
  createdAfter?: string;
  createdBefore?: string;
  updatedAfter?: string;
  updatedBefore?: string;
}

export interface GitHubTriggerRule {
  id: string;
  repository: string;
  name: string;
  enabled: boolean;
  eventKinds: GitHubItemKind[];
  filters: GitHubMonitorFilters;
  promptTemplate: string;
  includeBody: boolean;
  provider: AgentName;
  model?: string;
  reasoningEffort?: string;
  concurrencyPolicy: GitHubConcurrencyPolicy;
  priority: GitHubJobPriority;
  createdAt: string;
  updatedAt: string;
}

export interface GitHubMonitorEvent {
  id: string;
  repository: string;
  ruleId?: string;
  kind: GitHubItemKind;
  number: number;
  action: string;
  beforeStateHash?: string;
  afterStateHash: string;
  deliveryKey?: string;
  status: GitHubMonitorEventStatus;
  skipReason?: string;
  replayOfEventId?: string;
  itemSnapshot: GitHubItem;
  sessionId?: string;
  runId?: string;
  lastError?: string;
  createdAt: string;
  updatedAt: string;
}

export interface GitHubRemoteCandidate {
  host: string;
  owner: string;
  name: string;
  remoteName: string;
}

export interface GitHubRepositoryResolution {
  repository: string;
  candidates: GitHubRemoteCandidate[];
  selected?: GitHubRemoteCandidate;
  monitor?: GitHubRepositoryMonitor;
}

export interface CreateGitHubMonitorRequest {
  workspace: string;
  remoteName?: string;
  projectName?: string;
  pollIntervalSeconds: number;
  coalesceQueueLimit?: number;
}

export interface UpdateGitHubMonitorRequest {
  workspace: string;
  enabled: boolean;
  pollIntervalSeconds: number;
  coalesceQueueLimit: number;
  remoteName?: string;
  projectName?: string;
}

export interface GitHubSyncResult {
  issuesProcessed: number;
  pullRequestsProcessed: number;
  eventsMatched: number;
}

export interface GitHubTriggerRuleRequest {
  workspace: string;
  name: string;
  enabled: boolean;
  eventKinds: GitHubItemKind[];
  filters: GitHubMonitorFilters;
  promptTemplate: string;
  includeBody: boolean;
  provider: AgentName;
  model?: string;
  reasoningEffort?: string;
  concurrencyPolicy: GitHubConcurrencyPolicy;
  priority: GitHubJobPriority;
}

export interface GitHubTriggerRuleListResponse {
  rules: GitHubTriggerRule[];
}

export interface GitHubRepositoryMonitorListResponse {
  monitors: GitHubRepositoryMonitor[];
}

export interface GitHubMonitorEventListResponse {
  events: GitHubMonitorEvent[];
}

export interface GitHubItemListResponse {
  items: GitHubItem[];
  fetchedAt: string;
  projectsUnavailable?: boolean;
}
