import type {
  AgentRun,
  AgentSession,
  ChangeSet,
  CreateSessionRequest,
  ReviewStatus,
  SendMessageRequest,
  SessionEvent,
  WsTicketResponse,
} from '@maatgen/protocol';
import type { AgentApi } from '../api';
import type { EventStreamFactory } from '../event-stream';

type EventListener = (event: SessionEvent) => void;

const now = '2026-08-15T00:00:00Z';

export class MockAgentApi implements AgentApi {
  private readonly sessions = new Map<string, AgentSession>();
  private readonly events = new Map<string, SessionEvent[]>();
  private readonly changes = new Map<string, ChangeSet>();
  private readonly listeners = new Map<string, Set<EventListener>>();
  private readonly runs = new Map<string, AgentRun>();
  private readonly runTimers = new Map<string, number[]>();

  constructor() {
    for (const scenario of mockScenarios()) {
      this.sessions.set(scenario.session.id, scenario.session);
      this.events.set(scenario.session.id, scenario.events);
      this.changes.set(scenario.session.id, scenario.changes);
    }
  }

  async listProviders() {
    return { providers: [{ id: 'codex' as const, label: 'Codex', models: ['gpt-5.6-sol'] }] };
  }

  async listSessions(cursor?: string, limit = 25) {
    const offset = cursor ? Number.parseInt(cursor, 10) : 0;
    const sessions = [...this.sessions.values()];
    const page = sessions.slice(offset, offset + limit);
    const nextOffset = offset + page.length;
    return clone({
      sessions: page,
      ...(nextOffset < sessions.length ? { nextCursor: String(nextOffset) } : {}),
    });
  }

  async createSession(request: CreateSessionRequest): Promise<AgentSession> {
    const id = `mock-${this.sessions.size + 1}`;
    const session: AgentSession = {
      id,
      agent: request.agent,
      workspace: request.workspace,
      worktree: `C:/maatgen/mock/worktrees/${id}`,
      baseCommit: '0123456789abcdef',
      status: 'active',
      createdAt: new Date().toISOString(),
      cleanupStatus: 'not_started',
    };
    this.sessions.set(id, session);
    this.events.set(id, []);
    this.changes.set(id, { sessionId: id, files: [] });
    return clone(session);
  }

  async getSession(id: string): Promise<AgentSession> {
    return clone(this.requireSession(id));
  }

  async closeSession(id: string): Promise<AgentSession> {
    const session = this.requireSession(id);
    session.status = 'closed';
    session.closedAt = new Date().toISOString();
    session.cleanupStatus = 'completed';
    return clone(session);
  }

  async sendMessage(id: string, request: SendMessageRequest): Promise<AgentRun> {
    this.requireSession(id);
    const run: AgentRun = {
      id: `run-${Date.now()}`,
      sessionId: id,
      status: 'running',
      prompt: request.message,
      startedAt: new Date().toISOString(),
    };
    this.runs.set(run.id, run);
    this.appendEvent(id, 'user_prompt', 'user', { text: request.message }, run.id);
    this.appendEvent(id, 'run_started', 'manager', {}, run.id);
    const timers = [
      window.setTimeout(() => {
        this.appendEvent(id, 'assistant_message', 'codex', { text: 'Mock Codexが変更案を作成しました。' }, run.id);
      }, 300),
      window.setTimeout(() => {
        run.status = 'completed';
        run.finishedAt = new Date().toISOString();
        this.appendEvent(id, 'change_detected', 'manager', { files: this.changes.get(id)?.files.length ?? 0 }, run.id);
        this.appendEvent(id, 'run_completed', 'manager', {}, run.id);
        this.runTimers.delete(run.id);
      }, 700),
    ];
    this.runTimers.set(run.id, timers);
    return clone(run);
  }

  async cancelRun(id: string): Promise<void> {
    const run = this.runs.get(id);
    if (!run) throw new Error('run was not found');
    for (const timer of this.runTimers.get(id) ?? []) window.clearTimeout(timer);
    this.runTimers.delete(id);
    run.status = 'cancelled';
    run.finishedAt = new Date().toISOString();
    this.appendEvent(run.sessionId, 'run_cancelled', 'manager', {}, run.id);
  }

  async getEvents(id: string, afterSequence = 0): Promise<SessionEvent[]> {
    return clone((this.events.get(id) ?? []).filter((event) => event.sequence > afterSequence));
  }

  async issueWebSocketTicket(): Promise<WsTicketResponse> {
    return { ticket: 'mock-ticket', expiresAt: new Date(Date.now() + 30_000).toISOString() };
  }

  async getChanges(id: string): Promise<ChangeSet> {
    const changeSet = this.changes.get(id);
    if (!changeSet) throw new Error('change set was not found');
    return clone(changeSet);
  }

  acceptChange(sessionId: string, changeId: string): Promise<ChangeSet> {
    return this.reviewChange(sessionId, changeId, 'accepted');
  }

  rejectChange(sessionId: string, changeId: string): Promise<ChangeSet> {
    return this.reviewChange(sessionId, changeId, 'rejected');
  }

  acceptAllChanges(sessionId: string): Promise<ChangeSet> {
    return this.reviewAll(sessionId, 'accepted');
  }

  rejectAllChanges(sessionId: string): Promise<ChangeSet> {
    return this.reviewAll(sessionId, 'rejected');
  }

  subscribe(sessionId: string, listener: EventListener): () => void {
    const listeners = this.listeners.get(sessionId) ?? new Set<EventListener>();
    listeners.add(listener);
    this.listeners.set(sessionId, listeners);
    return () => listeners.delete(listener);
  }

  private async reviewChange(sessionId: string, changeId: string, status: 'accepted' | 'rejected'): Promise<ChangeSet> {
    const changeSet = this.requireChangeSet(sessionId);
    const file = changeSet.files.find((candidate) => candidate.id === changeId || candidate.hunks.some((hunk) => hunk.id === changeId));
    if (!file) throw new Error('change was not found');
    const hunk = file.hunks.find((candidate) => candidate.id === changeId);
    if (hunk) hunk.status = status;
    else file.status = status;
    if (file.reviewMode === 'hunk') file.status = aggregateStatus(file.hunks.map((item) => item.status));
    this.appendEvent(sessionId, 'change_reviewed', 'manager', { changeId, decision: status });
    this.closeWhenReviewed(sessionId, changeSet);
    return clone(changeSet);
  }

  private async reviewAll(sessionId: string, status: 'accepted' | 'rejected'): Promise<ChangeSet> {
    const changeSet = this.requireChangeSet(sessionId);
    for (const file of changeSet.files) {
      if (file.reviewMode === 'file' && file.status === 'pending') file.status = status;
      for (const hunk of file.hunks) if (hunk.status === 'pending') hunk.status = status;
      if (file.reviewMode === 'hunk') file.status = aggregateStatus(file.hunks.map((item) => item.status));
    }
    this.appendEvent(sessionId, 'change_reviewed', 'manager', { decision: status, all: true });
    this.closeWhenReviewed(sessionId, changeSet);
    return clone(changeSet);
  }

  private closeWhenReviewed(sessionId: string, changeSet: ChangeSet) {
    const pending = changeSet.files.some((file) => file.status === 'pending' || file.hunks.some((hunk) => hunk.status === 'pending'));
    if (!pending) void this.closeSession(sessionId);
  }

  private appendEvent(sessionId: string, type: SessionEvent['type'], source: SessionEvent['source'], data: unknown, runId?: string) {
    const events = this.events.get(sessionId) ?? [];
    const event: SessionEvent = {
      id: `${sessionId}-event-${events.length + 1}`,
      sessionId,
      sequence: events.length + 1,
      timestamp: new Date().toISOString(),
      schemaVersion: 1,
      source,
      type,
      data,
      ...(runId ? { runId } : {}),
    };
    events.push(event);
    this.events.set(sessionId, events);
    for (const listener of this.listeners.get(sessionId) ?? []) listener(clone(event));
  }

  private requireSession(id: string): AgentSession {
    const session = this.sessions.get(id);
    if (!session) throw new Error('session was not found');
    return session;
  }

  private requireChangeSet(id: string): ChangeSet {
    const changeSet = this.changes.get(id);
    if (!changeSet) throw new Error('change set was not found');
    return changeSet;
  }
}

export function createMockEnvironment(): { agentApi: AgentApi; eventStreamFactory: EventStreamFactory } {
  const agentApi = new MockAgentApi();
  return {
    agentApi,
    eventStreamFactory: (options) => {
      let unsubscribe: (() => void) | undefined;
      return {
        start() {
          options.onState('connected');
          unsubscribe = agentApi.subscribe(options.sessionId, options.onEvent);
        },
        stop() {
          unsubscribe?.();
          options.onState('disconnected');
        },
      };
    },
  };
}

function aggregateStatus(statuses: Array<'pending' | 'accepted' | 'rejected'>): ReviewStatus {
  if (statuses.every((status) => status === 'accepted')) return 'accepted';
  if (statuses.every((status) => status === 'rejected')) return 'rejected';
  if (statuses.every((status) => status === 'pending')) return 'pending';
  return 'partially_accepted';
}

function mockScenarios(): Array<{ session: AgentSession; events: SessionEvent[]; changes: ChangeSet }> {
  return [
    scenario('mock-success', 'C:/demo/success', [
      event('mock-success', 1, 'assistant_message', { text: '認証処理を確認し、テストを追加しました。' }),
      event('mock-success', 2, 'usage_reported', { totalTokens: 2400 }),
    ], oneHunk('mock-success')),
    scenario('mock-failure', 'C:/demo/failure', [event('mock-failure', 1, 'run_failed', { code: 'codex_unavailable', message: 'Mock: Codex CLI is unavailable' })], emptyChanges('mock-failure')),
    scenario('mock-cancelled', 'C:/demo/cancelled', [event('mock-cancelled', 1, 'run_cancelled', {})], emptyChanges('mock-cancelled')),
    scenario('mock-multi-hunk', 'C:/demo/multi-hunk', [event('mock-multi-hunk', 1, 'assistant_message', { text: '2つのHunkをReviewできます。' })], multiHunk('mock-multi-hunk')),
  ];
}

function scenario(id: string, workspace: string, events: SessionEvent[], changes: ChangeSet) {
  return {
    session: {
      id,
      agent: 'codex' as const,
      workspace,
      worktree: `C:/maatgen/mock/worktrees/${id}`,
      baseCommit: '0123456789abcdef',
      status: 'active' as const,
      createdAt: now,
      cleanupStatus: 'not_started' as const,
    },
    events,
    changes,
  };
}

function event(sessionId: string, sequence: number, type: SessionEvent['type'], data: unknown): SessionEvent {
  return { id: `${sessionId}-event-${sequence}`, sessionId, sequence, timestamp: now, schemaVersion: 1, source: 'manager', type, data };
}

function emptyChanges(sessionId: string): ChangeSet {
  return { sessionId, files: [] };
}

function oneHunk(sessionId: string): ChangeSet {
  return {
    sessionId,
    files: [{
      id: `${sessionId}-file`, oldPath: 'src/auth.ts', newPath: 'src/auth.ts', kind: 'modify', reviewMode: 'hunk', status: 'pending',
      original: 'export const enabled = false;\n', modified: 'export const enabled = true;\n',
      hunks: [{ id: `${sessionId}-hunk`, oldStart: 1, oldLines: 1, newStart: 1, newLines: 1, originalText: 'export const enabled = false;\n', modifiedText: 'export const enabled = true;\n', status: 'pending' }],
    }],
  };
}

function multiHunk(sessionId: string): ChangeSet {
  const changeSet = oneHunk(sessionId);
  const file = changeSet.files[0]!;
  file.newPath = 'src/config.ts';
  file.oldPath = 'src/config.ts';
  file.hunks.push({ id: `${sessionId}-hunk-2`, oldStart: 8, oldLines: 1, newStart: 8, newLines: 2, originalText: 'timeout: 10\n', modifiedText: 'timeout: 30\nretries: 2\n', status: 'pending' });
  return changeSet;
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}
