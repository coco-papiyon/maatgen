export interface AgentSession { id: string; agent: string; workspace: string; status: string; createdAt: string; closedAt?: string; }
export interface AgentProvider { id: string; label: string; models: string[]; defaultModel?: string; }
export interface AgentRun { id: string; sessionId: string; status: string; prompt: string; startedAt?: string; finishedAt?: string; exitCode?: number; }
export interface SessionEvent { id: string; sessionId: string; runId?: string; sequence: number; timestamp: string; source: string; type: string; data: Record<string, unknown>; }
export interface ChangeFile {
  id: string;
  oldPath?: string;
  newPath?: string;
  kind: string;
  status: string;
  restoreMode: string;
  hunks: Array<{ id: string; oldStart?: number; newStart?: number }>;
}
export interface ChangeSet { sessionId: string; checkpointId: string; files: ChangeFile[]; }
interface CreateSessionRequest { agent: string; workspace: string; }
interface SendMessageRequest { message: string; model?: string; timeoutSeconds?: number; }

export interface SessionUsage {
  sessionId: string;
  summary: Record<string, number | string | undefined>;
  runs: Array<{ run: AgentRun; usage?: Record<string, number | string | undefined> }>;
}

interface SessionListResponse { sessions: AgentSession[]; nextCursor?: string; }
interface ProviderListResponse { providers: AgentProvider[]; }

export class AgentManagerClient {
  constructor(private readonly baseUrl: string, private readonly authToken: string) {}

  listProviders(): Promise<AgentProvider[]> {
    return this.request<ProviderListResponse>('/api/v1/providers').then((response) => response.providers);
  }

  async listSessions(): Promise<AgentSession[]> {
    const sessions: AgentSession[] = [];
    let cursor = '';
    do {
      const query = new URLSearchParams({ limit: '100' });
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

  getUsage(id: string): Promise<SessionUsage> {
    return this.request(`/api/v1/sessions/${encodeURIComponent(id)}/usage`);
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
      headers: { Authorization: `Bearer ${this.authToken}`, ...(init.headers ?? {}), ...(init.body ? { 'Content-Type': 'application/json' } : {}) },
    });
    if (!response.ok) {
      let message = `${response.status} ${response.statusText}`;
      try {
        const body = await response.json() as { error?: { message?: string } };
        message = body.error?.message ?? message;
      } catch { /* retain HTTP status */ }
      throw new Error(message);
    }
    if (response.status === 204) return undefined as T;
    return await response.json() as T;
  }
}
