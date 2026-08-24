import type {
  AgentRun,
  AgentSession,
  ChangeSet,
  CreateGitHubMonitorRequest,
  CreateSessionRequest,
  GitHubItem,
  GitHubItemListResponse,
  GitHubMonitorEvent,
  GitHubMonitorEventListResponse,
  GitHubRepositoryMonitor,
  GitHubRepositoryMonitorListResponse,
  GitHubRepositoryResolution,
  GitHubSyncResult,
  GitHubTriggerRule,
  GitHubTriggerRuleListResponse,
  GitHubTriggerRuleRequest,
  SendMessageRequest,
  SessionEvent,
  SessionListResponse,
  ProviderListResponse,
  ProviderUsage,
  UpdateGitHubMonitorRequest,
  WsTicketResponse,
  CommandApproval,
  ApprovalDecisionRequest,
  UsageSummary,
  UsageModelListResponse,
  UsageProviderListResponse,
} from '@maatgen/protocol';

export type UsageGranularity = 'day' | 'week' | 'month';
export type ReasoningEffort = 'low' | 'medium' | 'high' | 'xhigh' | 'max';

export type SessionStatusFilter = 'active' | 'closed' | 'all';

export interface SessionUsage {
  sessionId: string;
  summary: import('@maatgen/protocol').TokenUsage;
  runs: Array<{ run: AgentRun; usage?: import('@maatgen/protocol').TokenUsage }>;
}

export interface SourceStatsLanguage {
  language: string;
  files: number;
  blank: number;
  comment: number;
  code: number;
}

export interface SourceStats {
  sessionId: string;
  languages: SourceStatsLanguage[];
  total: SourceStatsLanguage;
}

export interface GitHubItemQuery {
  state?: 'open' | 'closed' | 'all';
  assignee?: string;
  author?: string;
  labels?: string[];
  text?: string;
  project?: string;
  status?: string;
}

export interface AgentApi {
  getDefaultWorkspace(): Promise<string>;
  listProviders(): Promise<ProviderListResponse>;
  setProviderModel(provider: string, model: string): Promise<void>;
  listSessions(cursor?: string, limit?: number, status?: SessionStatusFilter): Promise<SessionListResponse>;
  createSession(request: CreateSessionRequest): Promise<AgentSession>;
  getSession(id: string): Promise<AgentSession>;
  closeSession(id: string): Promise<AgentSession>;
  reopenSession(id: string): Promise<AgentSession>;
  sendMessage(id: string, request: SendMessageRequest): Promise<AgentRun>;
  cancelRun(id: string): Promise<void>;
  getEvents(id: string, afterSequence?: number): Promise<SessionEvent[]>;
  issueWebSocketTicket(): Promise<WsTicketResponse>;
  getChanges(id: string): Promise<ChangeSet>;
  getUsage(id: string): Promise<SessionUsage>;
  getProviderUsage(id: string): Promise<ProviderUsage | undefined>;
  getUsageSummary(granularity: UsageGranularity, provider?: string, model?: string): Promise<UsageSummary>;
  getUsageProviders(): Promise<string[]>;
  getUsageModels(provider?: string): Promise<string[]>;
  getSourceStats(id: string): Promise<SourceStats>;
  listApprovals(id: string, pendingOnly?: boolean): Promise<CommandApproval[]>;
  decideApproval(sessionId: string, approvalId: string, request: ApprovalDecisionRequest): Promise<CommandApproval>;
  restoreHunk(sessionId: string, checkpointId: string, hunkId: string): Promise<ChangeSet>;
  restoreFile(sessionId: string, checkpointId: string, fileId: string): Promise<ChangeSet>;
  restoreAllChanges(sessionId: string, checkpointId: string): Promise<ChangeSet>;
  searchWorkspaceFiles(sessionId: string, query: string): Promise<string[]>;

  // GitHub monitoring (ADR-007).
  resolveGitHubRepository(workspace: string): Promise<GitHubRepositoryResolution>;
  listGitHubMonitors(): Promise<GitHubRepositoryMonitor[]>;
  getGitHubMonitor(workspace: string): Promise<GitHubRepositoryMonitor>;
  createGitHubMonitor(request: CreateGitHubMonitorRequest): Promise<GitHubRepositoryMonitor>;
  updateGitHubMonitor(request: UpdateGitHubMonitorRequest): Promise<GitHubRepositoryMonitor>;
  deleteGitHubMonitor(workspace: string): Promise<void>;
  syncGitHubMonitorNow(workspace: string): Promise<GitHubSyncResult>;
  // Omitting workspace lists trigger rules across every registered repository
  // (the Settings screen's cross-repository rule table).
  listGitHubTriggerRules(workspace?: string): Promise<GitHubTriggerRule[]>;
  createGitHubTriggerRule(request: GitHubTriggerRuleRequest): Promise<GitHubTriggerRule>;
  getGitHubTriggerRule(id: string): Promise<GitHubTriggerRule>;
  updateGitHubTriggerRule(id: string, request: GitHubTriggerRuleRequest): Promise<GitHubTriggerRule>;
  deleteGitHubTriggerRule(id: string): Promise<void>;
  listGitHubMonitorEvents(workspace: string, limit?: number): Promise<GitHubMonitorEvent[]>;
  skipGitHubMonitorEvent(eventId: string): Promise<GitHubMonitorEvent>;
  replayGitHubMonitorEvent(eventId: string): Promise<GitHubMonitorEvent>;
  listGitHubIssues(workspace: string, query?: GitHubItemQuery): Promise<GitHubItemListResponse>;
  getGitHubIssue(workspace: string, number: number): Promise<GitHubItem>;
  listGitHubPullRequests(workspace: string, query?: GitHubItemQuery): Promise<GitHubItemListResponse>;
  getGitHubPullRequest(workspace: string, number: number): Promise<GitHubItem>;
}

interface ApiErrorEnvelope {
  error?: { code?: string; message?: string };
}

export class AgentApiError extends Error {
  constructor(message: string, readonly status: number, readonly code?: string) {
    super(message);
    this.name = 'AgentApiError';
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
  });
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`;
    let code: string | undefined;
    try {
      const body = (await response.json()) as ApiErrorEnvelope;
      message = body.error?.message ?? message;
      code = body.error?.code;
    } catch {
      // Keep the HTTP status when the response is not JSON.
    }
    throw new AgentApiError(message, response.status, code);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export const httpAgentApi: AgentApi = {
  async getDefaultWorkspace() {
    const config = await request<{ defaultWorkspace: string }>('/api/v1/runtime-config');
    return config.defaultWorkspace;
  },
  listProviders() {
    return request('/api/v1/providers');
  },
  setProviderModel(provider, model) {
    return request(`/api/v1/providers/${encodeURIComponent(provider)}/model`, {
      method: 'PUT',
      body: JSON.stringify({ model }),
    });
  },
  listSessions(cursor, limit = 25, status = 'active') {
    const query = new URLSearchParams({ limit: String(limit), status });
    if (cursor) query.set('cursor', cursor);
    return request(`/api/v1/sessions?${query}`);
  },
  createSession(requestBody) {
    return request('/api/v1/sessions', { method: 'POST', body: JSON.stringify(requestBody) });
  },
  getSession(id) {
    return request(`/api/v1/sessions/${encodeURIComponent(id)}`);
  },
  closeSession(id) {
    return request(`/api/v1/sessions/${encodeURIComponent(id)}/close`, { method: 'POST' });
  },
  reopenSession(id) {
    return request(`/api/v1/sessions/${encodeURIComponent(id)}/reopen`, { method: 'POST' });
  },
  sendMessage(id, requestBody) {
    return request(`/api/v1/sessions/${encodeURIComponent(id)}/messages`, {
      method: 'POST',
      body: JSON.stringify(requestBody),
    });
  },
  cancelRun(id) {
    return request(`/api/v1/runs/${encodeURIComponent(id)}/cancel`, { method: 'POST' });
  },
  async getEvents(id, afterSequence = 0) {
    const response = await request<{ events: SessionEvent[] }>(
      `/api/v1/sessions/${encodeURIComponent(id)}/events?afterSequence=${afterSequence}&limit=1000`,
    );
    return response.events;
  },
  issueWebSocketTicket() {
    return request('/api/v1/ws-tickets', { method: 'POST' });
  },
  getChanges(id) {
    return request(`/api/v1/sessions/${encodeURIComponent(id)}/changes`);
  },
  getUsage(id) {
    return request(`/api/v1/sessions/${encodeURIComponent(id)}/usage`);
  },
  getProviderUsage(id) {
    return request(`/api/v1/sessions/${encodeURIComponent(id)}/provider-usage`);
  },
  getUsageSummary(granularity, provider, model) {
    const query = new URLSearchParams({ granularity });
    if (provider) query.set('provider', provider);
    if (model) query.set('model', model);
    return request(`/api/v1/usage/summary?${query}`);
  },
  async getUsageProviders() {
    const response = await request<UsageProviderListResponse>('/api/v1/usage/providers');
    return response.providers;
  },
  async getUsageModels(provider) {
    const query = provider ? `?provider=${encodeURIComponent(provider)}` : '';
    const response = await request<UsageModelListResponse>(`/api/v1/usage/models${query}`);
    return response.models;
  },
  getSourceStats(id) {
    return request(`/api/v1/sessions/${encodeURIComponent(id)}/source-stats`);
  },
  async listApprovals(id, pendingOnly = true) {
    const suffix = pendingOnly ? '?status=pending' : '';
    const response = await request<{ approvals: CommandApproval[] }>(`/api/v1/sessions/${encodeURIComponent(id)}/approvals${suffix}`);
    return response.approvals;
  },
  decideApproval(sessionId, approvalId, requestBody) {
    return request(`/api/v1/sessions/${encodeURIComponent(sessionId)}/approvals/${encodeURIComponent(approvalId)}/decision`, {
      method: 'POST', body: JSON.stringify(requestBody),
    });
  },
  restoreHunk(sessionId, checkpointId, hunkId) {
    return request(`/api/v1/sessions/${encodeURIComponent(sessionId)}/checkpoints/${encodeURIComponent(checkpointId)}/hunks/${encodeURIComponent(hunkId)}/restore`, { method: 'POST' });
  },
  restoreFile(sessionId, checkpointId, fileId) {
    return request(`/api/v1/sessions/${encodeURIComponent(sessionId)}/checkpoints/${encodeURIComponent(checkpointId)}/files/${encodeURIComponent(fileId)}/restore`, { method: 'POST' });
  },
  restoreAllChanges(sessionId, checkpointId) {
    return request(`/api/v1/sessions/${encodeURIComponent(sessionId)}/checkpoints/${encodeURIComponent(checkpointId)}/restore`, { method: 'POST' });
  },
  async searchWorkspaceFiles(sessionId, query) {
    const response = await request<{ files: string[] }>(
      `/api/v1/sessions/${encodeURIComponent(sessionId)}/workspace-files?query=${encodeURIComponent(query)}`,
    );
    return response.files;
  },

  resolveGitHubRepository(workspace) {
    return request(`/api/v1/github/repository?workspace=${encodeURIComponent(workspace)}`);
  },
  async listGitHubMonitors() {
    const response = await request<GitHubRepositoryMonitorListResponse>('/api/v1/github/monitors');
    return response.monitors;
  },
  getGitHubMonitor(workspace) {
    return request(`/api/v1/github/monitor?workspace=${encodeURIComponent(workspace)}`);
  },
  createGitHubMonitor(requestBody) {
    return request('/api/v1/github/monitor', { method: 'POST', body: JSON.stringify(requestBody) });
  },
  updateGitHubMonitor(requestBody) {
    return request('/api/v1/github/monitor', { method: 'PUT', body: JSON.stringify(requestBody) });
  },
  deleteGitHubMonitor(workspace) {
    return request(`/api/v1/github/monitor?workspace=${encodeURIComponent(workspace)}`, { method: 'DELETE' });
  },
  syncGitHubMonitorNow(workspace) {
    return request(`/api/v1/github/monitor/sync?workspace=${encodeURIComponent(workspace)}`, { method: 'POST' });
  },
  async listGitHubTriggerRules(workspace) {
    const query = workspace ? `?workspace=${encodeURIComponent(workspace)}` : '';
    const response = await request<GitHubTriggerRuleListResponse>(`/api/v1/github/rules${query}`);
    return response.rules;
  },
  createGitHubTriggerRule(requestBody) {
    return request('/api/v1/github/rules', { method: 'POST', body: JSON.stringify(requestBody) });
  },
  getGitHubTriggerRule(id) {
    return request(`/api/v1/github/rules/${encodeURIComponent(id)}`);
  },
  updateGitHubTriggerRule(id, requestBody) {
    return request(`/api/v1/github/rules/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(requestBody) });
  },
  deleteGitHubTriggerRule(id) {
    return request(`/api/v1/github/rules/${encodeURIComponent(id)}`, { method: 'DELETE' });
  },
  async listGitHubMonitorEvents(workspace, limit = 100) {
    const query = new URLSearchParams({ workspace, limit: String(limit) });
    const response = await request<GitHubMonitorEventListResponse>(`/api/v1/github/events?${query}`);
    return response.events;
  },
  replayGitHubMonitorEvent(eventId) {
    return request(`/api/v1/github/events/${encodeURIComponent(eventId)}/replay`, { method: 'POST' });
  },
  skipGitHubMonitorEvent(eventId) {
    return request(`/api/v1/github/events/${encodeURIComponent(eventId)}/skip`, { method: 'POST' });
  },
  listGitHubIssues(workspace, query) {
    return request(`/api/v1/github/issues?${githubItemQueryString(workspace, query)}`);
  },
  getGitHubIssue(workspace, number) {
    return request(`/api/v1/github/issues/${number}?workspace=${encodeURIComponent(workspace)}`);
  },
  listGitHubPullRequests(workspace, query) {
    return request(`/api/v1/github/pulls?${githubItemQueryString(workspace, query)}`);
  },
  getGitHubPullRequest(workspace, number) {
    return request(`/api/v1/github/pulls/${number}?workspace=${encodeURIComponent(workspace)}`);
  },
};

function githubItemQueryString(workspace: string, query?: GitHubItemQuery): string {
  const params = new URLSearchParams({ workspace });
  if (query?.state) params.set('state', query.state);
  if (query?.assignee) params.set('assignee', query.assignee);
  if (query?.author) params.set('author', query.author);
  if (query?.text) params.set('text', query.text);
  if (query?.project) params.set('project', query.project);
  if (query?.status) params.set('status', query.status);
  for (const label of query?.labels ?? []) params.append('label', label);
  return params.toString();
}
