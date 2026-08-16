import type {
  AgentRun,
  AgentSession,
  ChangeSet,
  CreateSessionRequest,
  SendMessageRequest,
  SessionEvent,
  SessionListResponse,
  ProviderListResponse,
  WsTicketResponse,
  CommandApproval,
  ApprovalDecisionRequest,
} from '@maatgen/protocol';

export interface SessionUsage {
  sessionId: string;
  summary: import('@maatgen/protocol').TokenUsage;
  runs: Array<{ run: AgentRun; usage?: import('@maatgen/protocol').TokenUsage }>;
}

export interface AgentApi {
  getDefaultWorkspace(): Promise<string>;
  listProviders(): Promise<ProviderListResponse>;
  setProviderModel(provider: string, model: string): Promise<void>;
  listSessions(cursor?: string, limit?: number): Promise<SessionListResponse>;
  createSession(request: CreateSessionRequest): Promise<AgentSession>;
  getSession(id: string): Promise<AgentSession>;
  closeSession(id: string): Promise<AgentSession>;
  sendMessage(id: string, request: SendMessageRequest): Promise<AgentRun>;
  cancelRun(id: string): Promise<void>;
  getEvents(id: string, afterSequence?: number): Promise<SessionEvent[]>;
  issueWebSocketTicket(): Promise<WsTicketResponse>;
  getChanges(id: string): Promise<ChangeSet>;
  getUsage(id: string): Promise<SessionUsage>;
  listApprovals(id: string, pendingOnly?: boolean): Promise<CommandApproval[]>;
  decideApproval(sessionId: string, approvalId: string, request: ApprovalDecisionRequest): Promise<CommandApproval>;
  restoreHunk(sessionId: string, checkpointId: string, hunkId: string): Promise<ChangeSet>;
  restoreFile(sessionId: string, checkpointId: string, fileId: string): Promise<ChangeSet>;
  restoreAllChanges(sessionId: string, checkpointId: string): Promise<ChangeSet>;
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
  listSessions(cursor, limit = 25) {
    const query = new URLSearchParams({ limit: String(limit) });
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
};
