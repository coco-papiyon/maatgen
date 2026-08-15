import type {
  AgentRun,
  AgentSession,
  ChangeSet,
  CreateSessionRequest,
  SendMessageRequest,
  SessionEvent,
  SessionListResponse,
  WsTicketResponse,
} from '@maatgen/protocol';

export interface AgentApi {
  listSessions(cursor?: string, limit?: number): Promise<SessionListResponse>;
  createSession(request: CreateSessionRequest): Promise<AgentSession>;
  getSession(id: string): Promise<AgentSession>;
  closeSession(id: string): Promise<AgentSession>;
  sendMessage(id: string, request: SendMessageRequest): Promise<AgentRun>;
  cancelRun(id: string): Promise<void>;
  getEvents(id: string, afterSequence?: number): Promise<SessionEvent[]>;
  issueWebSocketTicket(): Promise<WsTicketResponse>;
  getChanges(id: string): Promise<ChangeSet>;
  acceptChange(sessionId: string, changeId: string): Promise<ChangeSet>;
  rejectChange(sessionId: string, changeId: string): Promise<ChangeSet>;
  acceptAllChanges(sessionId: string): Promise<ChangeSet>;
  rejectAllChanges(sessionId: string): Promise<ChangeSet>;
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
  acceptChange(sessionId, changeId) {
    return request(`/api/v1/sessions/${encodeURIComponent(sessionId)}/changes/${encodeURIComponent(changeId)}/accept`, { method: 'POST' });
  },
  rejectChange(sessionId, changeId) {
    return request(`/api/v1/sessions/${encodeURIComponent(sessionId)}/changes/${encodeURIComponent(changeId)}/reject`, { method: 'POST' });
  },
  acceptAllChanges(sessionId) {
    return request(`/api/v1/sessions/${encodeURIComponent(sessionId)}/changes/accept-all`, { method: 'POST' });
  },
  rejectAllChanges(sessionId) {
    return request(`/api/v1/sessions/${encodeURIComponent(sessionId)}/changes/reject-all`, { method: 'POST' });
  },
};
