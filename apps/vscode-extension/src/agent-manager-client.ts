export interface AgentSession { id: string; agent: string; workspace: string; status: string; createdAt: string; closedAt?: string; }
export interface AgentProvider { id: string; label: string; models: string[]; defaultModel?: string; }
export interface AgentRun { id: string; sessionId: string; status: string; prompt: string; startedAt?: string; finishedAt?: string; exitCode?: number; }
export interface SessionEvent { id: string; sessionId: string; runId?: string; sequence: number; timestamp: string; source: string; type: string; data: Record<string, unknown>; }
export interface ChangeFile {
  id: string;
  oldPath?: string;
  newPath?: string;
  kind: 'modify' | 'add' | 'delete' | 'rename' | 'binary' | 'mode_change';
  original?: string;
  modified?: string;
  status: 'changed' | 'partially_restored' | 'restored' | 'conflict';
  restoreMode: 'hunk' | 'file';
  hunks: Array<{
    id: string;
    oldStart: number;
    oldLines: number;
    newStart: number;
    newLines: number;
    originalText: string;
    modifiedText: string;
    status: 'changed' | 'restored' | 'conflict';
  }>;
}
export interface ChangeSet { sessionId: string; runId?: string; checkpointId: string; beforeTree?: string; afterTree?: string; files: ChangeFile[]; }
export interface CommandApproval {
  id: string;
  sessionId: string;
  runId: string;
  command: string;
  status: string;
  risk?: string;
  summary?: string;
  factors: string[];
  segments: Array<{ index: number; command: string; argv: string[] }>;
}
export interface ApprovalDecisionRequest { decision: 'allow_once' | 'allow_session' | 'allow_permanent' | 'deny'; ruleArgv?: string[]; }
interface CreateSessionRequest { agent: string; workspace: string; }
export type ReasoningEffort = 'low' | 'medium' | 'high' | 'xhigh' | 'max';
interface SendMessageRequest { message: string; model?: string; reasoningEffort?: ReasoningEffort; timeoutSeconds?: number; }

export interface SessionUsage {
  sessionId: string;
  summary: Record<string, number | string | undefined>;
  runs: Array<{ run: AgentRun; usage?: Record<string, number | string | undefined> }>;
}
export interface ProviderUsageWindow { name: string; usedPercent: number; remainingPercent: number; resetLabel?: string; }
export interface ProviderUsage { provider: string; windows: ProviderUsageWindow[]; fetchedAt: string; }

export class AgentManagerError extends Error {
  constructor(message: string, readonly status: number, readonly code?: string) {
    super(message);
    this.name = 'AgentManagerError';
  }
}

interface SessionListResponse { sessions: AgentSession[]; nextCursor?: string; }
interface ProviderListResponse { providers: AgentProvider[]; }

export class AgentManagerClient {
  constructor(private readonly baseUrl: string) {}

  listProviders(): Promise<AgentProvider[]> {
    return this.request<ProviderListResponse>('/api/v1/providers').then((response) => response.providers);
  }

  async listSessions(): Promise<AgentSession[]> {
    const sessions: AgentSession[] = [];
    let cursor = '';
    do {
      const query = new URLSearchParams({ limit: '100', status: 'all' });
      if (cursor) query.set('cursor', cursor);
      const response = await this.request<SessionListResponse>(`/api/v1/sessions?${query.toString()}`);
      sessions.push(...response.sessions);
      cursor = response.nextCursor ?? '';
    } while (cursor);
    return sessions;
  }

  createSession(request: CreateSessionRequest): Promise<AgentSession> {
    return this.request('/api/v1/sessions', { method: 'POST', body: JSON.stringify(request) });
  }

  getSession(id: string): Promise<AgentSession> {
    return this.request(`/api/v1/sessions/${encodeURIComponent(id)}`);
  }

  getEvents(id: string, afterSequence = 0): Promise<SessionEvent[]> {
    return this.request<{ events: SessionEvent[] }>(
      `/api/v1/sessions/${encodeURIComponent(id)}/events?afterSequence=${afterSequence}&limit=1000`,
    ).then((response) => response.events);
  }

  getChanges(id: string): Promise<ChangeSet> {
    return this.request(`/api/v1/sessions/${encodeURIComponent(id)}/changes`);
  }

  restoreHunk(sessionId: string, checkpointId: string, hunkId: string): Promise<ChangeSet> {
    return this.request(`/api/v1/sessions/${encodeURIComponent(sessionId)}/checkpoints/${encodeURIComponent(checkpointId)}/hunks/${encodeURIComponent(hunkId)}/restore`, { method: 'POST' });
  }

  restoreFile(sessionId: string, checkpointId: string, fileId: string): Promise<ChangeSet> {
    return this.request(`/api/v1/sessions/${encodeURIComponent(sessionId)}/checkpoints/${encodeURIComponent(checkpointId)}/files/${encodeURIComponent(fileId)}/restore`, { method: 'POST' });
  }

  restoreAllChanges(sessionId: string, checkpointId: string): Promise<ChangeSet> {
    return this.request(`/api/v1/sessions/${encodeURIComponent(sessionId)}/checkpoints/${encodeURIComponent(checkpointId)}/restore`, { method: 'POST' });
  }

  getUsage(id: string): Promise<SessionUsage> {
    return this.request(`/api/v1/sessions/${encodeURIComponent(id)}/usage`);
  }

  getProviderUsage(id: string): Promise<ProviderUsage> {
    return this.request(`/api/v1/sessions/${encodeURIComponent(id)}/provider-usage`);
  }

  getAllProviderUsage(id: string): Promise<ProviderUsage[]> {
    return this.request<{ providers: ProviderUsage[] }>(
      `/api/v1/sessions/${encodeURIComponent(id)}/provider-usage/all`,
    ).then((response) => response.providers);
  }

  listApprovals(id: string): Promise<CommandApproval[]> {
    return this.request<{ approvals: CommandApproval[] }>(
      `/api/v1/sessions/${encodeURIComponent(id)}/approvals?status=pending`,
    ).then((response) => response.approvals);
  }

  decideApproval(sessionId: string, approvalId: string, request: ApprovalDecisionRequest): Promise<CommandApproval> {
    return this.request(`/api/v1/sessions/${encodeURIComponent(sessionId)}/approvals/${encodeURIComponent(approvalId)}/decision`, {
      method: 'POST', body: JSON.stringify(request),
    });
  }

  sendMessage(id: string, request: SendMessageRequest): Promise<AgentRun> {
    return this.request(`/api/v1/sessions/${encodeURIComponent(id)}/messages`, {
      method: 'POST', body: JSON.stringify(request),
    });
  }

  cancelRun(id: string): Promise<void> {
    return this.request(`/api/v1/runs/${encodeURIComponent(id)}/cancel`, { method: 'POST' });
  }

  closeSession(id: string): Promise<AgentSession> {
    return this.request(`/api/v1/sessions/${encodeURIComponent(id)}/close`, { method: 'POST' });
  }

  private async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const response = await fetch(`${this.baseUrl}${path}`, {
      ...init,
      headers: { ...(init.headers ?? {}), ...(init.body ? { 'Content-Type': 'application/json' } : {}) },
    });
    if (!response.ok) {
      let message = `${response.status} ${response.statusText}`;
      let code: string | undefined;
      try {
        const body = await response.json() as { error?: { code?: string; message?: string } };
        message = body.error?.message ?? message;
        code = body.error?.code;
      } catch { /* retain HTTP status */ }
      throw new AgentManagerError(message, response.status, code);
    }
    if (response.status === 204) return undefined as T;
    return await response.json() as T;
  }
}
