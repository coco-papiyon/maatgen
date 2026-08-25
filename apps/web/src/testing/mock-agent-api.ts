import type {
  AgentRun,
  AgentSession,
  ChangeSet,
  CommandApproval,
  CreateGitHubMonitorRequest,
  CreateSessionRequest,
  GitHubItem,
  GitHubItemListResponse,
  GitHubMonitorEvent,
  GitHubRepositoryMonitor,
  GitHubRepositoryResolution,
  GitHubSyncResult,
  GitHubTriggerRule,
  GitHubTriggerRuleRequest,
  ProviderUsage,
  RestoreStatus,
  SendMessageRequest,
  SessionEvent,
  UpdateGitHubMonitorRequest,
  UsageSummary,
  WsTicketResponse,
} from '@maatgen/protocol';
import { AgentApiError, type AgentApi, type GitHubItemQuery, type SessionStatusFilter, type SessionUsage, type SourceStats, type UsageGranularity, type WorkspaceFileContent, type WorkspaceFileNode } from '../api';
import type { EventStreamFactory } from '../event-stream';

const GITHUB_DEMO_WORKSPACE = 'C:/demo/current-repository';

type EventListener = (event: SessionEvent) => void;

const now = '2026-08-15T00:00:00Z';

export class MockAgentApi implements AgentApi {
  private readonly sessions = new Map<string, AgentSession>();
  private readonly events = new Map<string, SessionEvent[]>();
  private readonly changes = new Map<string, ChangeSet>();
  private readonly sourceStats = new Map<string, SourceStats>();
  private readonly listeners = new Map<string, Set<EventListener>>();
  private readonly runs = new Map<string, AgentRun>();
  private readonly runTimers = new Map<string, number[]>();

  private readonly githubMonitors = new Map<string, GitHubRepositoryMonitor>();
  private readonly githubRules = new Map<string, GitHubTriggerRule>();
  private readonly githubEvents = new Map<string, GitHubMonitorEvent>();
  private readonly githubIssues: GitHubItem[] = mockGitHubIssues();
  private readonly githubPulls: GitHubItem[] = mockGitHubPullRequests();

  constructor() {
    for (const scenario of mockScenarios()) {
      this.sessions.set(scenario.session.id, scenario.session);
      this.events.set(scenario.session.id, scenario.events);
      this.changes.set(scenario.session.id, scenario.changes);
      this.sourceStats.set(scenario.session.id, scenario.sourceStats);
    }
    for (const monitor of mockGitHubMonitors()) {
      this.githubMonitors.set(monitor.repository, monitor);
    }
    for (const rule of mockGitHubTriggerRules()) {
      this.githubRules.set(rule.id, rule);
    }
    for (const githubEvent of mockGitHubMonitorEvents()) {
      this.githubEvents.set(githubEvent.id, githubEvent);
    }
  }

  async getDefaultWorkspace() {
    return 'C:/demo/current-repository';
  }

  async listProviders() {
    return {
      providers: [
        { id: 'codex' as const, label: 'Codex', models: ['gpt-5.6-sol'] },
        { id: 'claude' as const, label: 'Claude Code', models: ['claude-opus-5', 'claude-sonnet-5', 'claude-sonnet-4-6', 'claude-haiku-4-5'] },
      ],
    };
  }

  async setProviderModel(_provider: string, _model: string) {
    return undefined;
  }

  async listSessions(cursor?: string, limit = 25, status: SessionStatusFilter = 'active') {
    const offset = cursor ? Number.parseInt(cursor, 10) : 0;
    const sessions = [...this.sessions.values()].filter((session) => status === 'all' || session.status === status);
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
      status: 'active',
      triggerSource: 'manual',
      createdAt: new Date().toISOString(),
    };
    this.sessions.set(id, session);
    this.events.set(id, []);
    this.changes.set(id, emptyChanges(id));
    this.sourceStats.set(id, emptySourceStats(id));
    return clone(session);
  }

  async getSession(id: string): Promise<AgentSession> {
    return clone(this.requireSession(id));
  }

  async closeSession(id: string): Promise<AgentSession> {
    const session = this.requireSession(id);
    session.status = 'closed';
    session.closedAt = new Date().toISOString();
    return clone(session);
  }

  async reopenSession(id: string): Promise<AgentSession> {
    const session = this.requireSession(id);
    session.status = 'active';
    delete session.closedAt;
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

  async getSourceStats(id: string): Promise<SourceStats> {
    this.requireSession(id);
    return clone(this.sourceStats.get(id) ?? emptySourceStats(id));
  }

  async getUsage(id: string): Promise<SessionUsage> {
    const events = this.events.get(id) ?? [];
    const usageEvents = events.filter((event) => event.type === 'usage_reported');
    const runs = usageEvents.map((event) => ({
      run: { id: event.runId ?? `mock-usage-${event.sequence}`, sessionId: id, status: 'completed' as const, prompt: 'Mock usage run', finishedAt: event.timestamp },
      usage: event.data as SessionUsage['summary'],
    }));
    const summary: SessionUsage['summary'] = { source: 'unknown' };
    for (const entry of runs) {
      for (const key of ['inputTokens', 'cachedInputTokens', 'outputTokens', 'reasoningOutputTokens', 'totalTokens'] as const) {
        const value = entry.usage?.[key];
        if (typeof value !== 'number') continue;
        summary[key] = (summary[key] ?? 0) + value;
      }
      if (typeof entry.usage?.aiCredits === 'number') {
        summary.aiCredits = (summary.aiCredits ?? 0) + entry.usage.aiCredits;
      }
      if (typeof entry.usage?.actualModel === 'string') {
        summary.actualModel = summary.actualModel && summary.actualModel !== entry.usage.actualModel ? 'multiple' : entry.usage.actualModel;
      }
      if (typeof entry.usage?.costUsd === 'number') {
        summary.costUsd = (summary.costUsd ?? 0) + entry.usage.costUsd;
      }
    }
    return clone({ sessionId: id, summary, runs });
  }

  async getProviderUsage(_id: string): Promise<ProviderUsage | undefined> {
    return undefined;
  }

  async getAllProviderUsage(_id: string): Promise<ProviderUsage[]> {
    return [];
  }

  async getUsageSummary(granularity: UsageGranularity, provider?: string, model?: string): Promise<UsageSummary> {
    const seriesBy: 'provider' | 'model' = provider ? 'model' : 'provider';
    type Bucket = {
      costUsd: number;
      aiCredits: number;
      totalTokens: number;
      inputTokens: number;
      cachedInputTokens: number;
      outputTokens: number;
      reasoningOutputTokens: number;
      series: Map<string, { costUsd: number; aiCredits: number; totalTokens: number }>;
    };
    const buckets = new Map<string, Bucket>();
    for (const [sessionId, events] of this.events.entries()) {
      const sessionAgent = this.sessions.get(sessionId)?.agent;
      if (provider && sessionAgent !== provider) continue;
      for (const evt of events) {
        if (evt.type !== 'usage_reported') continue;
        const data = evt.data as {
          model?: string;
          actualModel?: string;
          costUsd?: number;
          aiCredits?: number;
          totalTokens?: number;
          inputTokens?: number;
          cachedInputTokens?: number;
          outputTokens?: number;
          reasoningOutputTokens?: number;
        };
        const usageModel = data.actualModel ?? data.model;
        if (model && usageModel !== model) continue;
        const seriesKey = seriesBy === 'provider' ? sessionAgent : usageModel;
        if (!seriesKey) continue;
        const period = usagePeriodKey(evt.timestamp, granularity);
        const bucket = buckets.get(period) ?? { costUsd: 0, aiCredits: 0, totalTokens: 0, inputTokens: 0, cachedInputTokens: 0, outputTokens: 0, reasoningOutputTokens: 0, series: new Map() };
        const cost = data.costUsd ?? 0;
        const credits = data.aiCredits ?? 0;
        const tokens = data.totalTokens ?? 0;
        bucket.costUsd += cost;
        bucket.aiCredits += credits;
        bucket.totalTokens += tokens;
        bucket.inputTokens += data.inputTokens ?? 0;
        bucket.cachedInputTokens += data.cachedInputTokens ?? 0;
        bucket.outputTokens += data.outputTokens ?? 0;
        bucket.reasoningOutputTokens += data.reasoningOutputTokens ?? 0;
        const point = bucket.series.get(seriesKey) ?? { costUsd: 0, aiCredits: 0, totalTokens: 0 };
        point.costUsd += cost;
        point.aiCredits += credits;
        point.totalTokens += tokens;
        bucket.series.set(seriesKey, point);
        buckets.set(period, bucket);
      }
    }
    const periods = [...buckets.entries()]
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([period, bucket]) => ({
        period,
        costUsd: bucket.costUsd,
        aiCredits: bucket.aiCredits,
        totalTokens: bucket.totalTokens,
        inputTokens: bucket.inputTokens,
        cachedInputTokens: bucket.cachedInputTokens,
        outputTokens: bucket.outputTokens,
        reasoningOutputTokens: bucket.reasoningOutputTokens,
        series: [...bucket.series.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([key, values]) => ({ key, ...values })),
      }));
    return clone({
      granularity,
      seriesBy,
      ...(provider ? { provider: provider as NonNullable<UsageSummary['provider']> } : {}),
      ...(model ? { model } : {}),
      periods,
    });
  }

  async getUsageProviders(): Promise<string[]> {
    const providers = new Set<string>();
    for (const [sessionId, events] of this.events.entries()) {
      if (!events.some((evt) => evt.type === 'usage_reported')) continue;
      const agent = this.sessions.get(sessionId)?.agent;
      if (agent) providers.add(agent);
    }
    return [...providers].sort();
  }

  async getUsageModels(provider?: string): Promise<string[]> {
    const models = new Set<string>();
    for (const [sessionId, events] of this.events.entries()) {
      const sessionAgent = this.sessions.get(sessionId)?.agent;
      if (provider && sessionAgent !== provider) continue;
      for (const evt of events) {
        if (evt.type !== 'usage_reported') continue;
        const data = evt.data as { model?: string; actualModel?: string };
        const usageModel = data.actualModel ?? data.model;
        if (usageModel) models.add(usageModel);
      }
    }
    return [...models].sort();
  }

  async listApprovals(_id: string): Promise<CommandApproval[]> {
    return [];
  }

  async decideApproval(_sessionId: string, _approvalId: string, _request: import('@maatgen/protocol').ApprovalDecisionRequest): Promise<CommandApproval> {
    throw new Error('mock approval was not found');
  }

  restoreHunk(sessionId: string, _checkpointId: string, hunkId: string): Promise<ChangeSet> {
    return this.restoreChange(sessionId, hunkId);
  }

  restoreFile(sessionId: string, _checkpointId: string, fileId: string): Promise<ChangeSet> {
    return this.restoreChange(sessionId, fileId);
  }

  restoreAllChanges(sessionId: string, _checkpointId: string): Promise<ChangeSet> {
    return this.restoreAll(sessionId);
  }

  subscribe(sessionId: string, listener: EventListener): () => void {
    const listeners = this.listeners.get(sessionId) ?? new Set<EventListener>();
    listeners.add(listener);
    this.listeners.set(sessionId, listeners);
    return () => listeners.delete(listener);
  }

  private async restoreChange(sessionId: string, changeId: string): Promise<ChangeSet> {
    const changeSet = this.requireChangeSet(sessionId);
    const file = changeSet.files.find((candidate) => candidate.id === changeId || candidate.hunks.some((hunk) => hunk.id === changeId));
    if (!file) throw new Error('change was not found');
    const hunk = file.hunks.find((candidate) => candidate.id === changeId);
    if (hunk) hunk.status = 'restored';
    else file.status = 'restored';
    if (file.restoreMode === 'hunk') file.status = aggregateStatus(file.hunks.map((item) => item.status));
    this.appendEvent(sessionId, 'change_restored', 'manager', { changeId });
    return clone(changeSet);
  }

  private async restoreAll(sessionId: string): Promise<ChangeSet> {
    const changeSet = this.requireChangeSet(sessionId);
    for (const file of changeSet.files) {
      file.status = 'restored';
      for (const hunk of file.hunks) hunk.status = 'restored';
    }
    this.appendEvent(sessionId, 'change_restored', 'manager', { all: true });
    return clone(changeSet);
  }

  private appendEvent(sessionId: string, type: SessionEvent['type'], source: SessionEvent['source'], data: unknown, runId?: string) {
    const events = this.events.get(sessionId) ?? [];
    const event: SessionEvent = {
      id: `${sessionId}-event-${events.length + 1}`,
      sessionId,
      sequence: events.length + 1,
      timestamp: new Date().toISOString(),
      schemaVersion: 2,
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

  async searchWorkspaceFiles(): Promise<string[]> {
    return [];
  }

  async getWorkspaceFileTree(id: string, path = ''): Promise<WorkspaceFileNode[]> {
    this.requireSession(id);
    return clone(mockWorkspaceDirectory(path));
  }

  async readWorkspaceFile(id: string, path: string): Promise<WorkspaceFileContent> {
    this.requireSession(id);
    const content = mockWorkspaceFileContents()[path];
    if (content === undefined) throw new AgentApiError('file was not found', 404, 'not_found');
    return clone({ path, content, binary: false, truncated: false });
  }

  async resolveGitHubRepository(workspace: string): Promise<GitHubRepositoryResolution> {
    const selected = workspace === GITHUB_DEMO_WORKSPACE
      ? { host: 'github.com', owner: 'octo-demo', name: 'example-repo', remoteName: 'origin' }
      : { host: 'github.com', owner: 'octo-demo', name: mockRepoNameFromWorkspace(workspace), remoteName: 'origin' };
    const monitor = this.githubMonitors.get(workspace);
    return { repository: workspace, candidates: [selected], selected, ...(monitor ? { monitor: clone(monitor) } : {}) };
  }

  async listGitHubMonitors(): Promise<GitHubRepositoryMonitor[]> {
    return [...this.githubMonitors.values()].map(clone);
  }

  async getGitHubMonitor(workspace: string): Promise<GitHubRepositoryMonitor> {
    const monitor = this.githubMonitors.get(workspace);
    if (!monitor) throw new AgentApiError('github monitor was not found', 404, 'not_found');
    return clone(monitor);
  }

  async createGitHubMonitor(request: CreateGitHubMonitorRequest): Promise<GitHubRepositoryMonitor> {
    const nowIso = now;
    const name = request.workspace === GITHUB_DEMO_WORKSPACE ? 'example-repo' : mockRepoNameFromWorkspace(request.workspace);
    const monitor: GitHubRepositoryMonitor = {
      repository: request.workspace, host: 'github.com', owner: 'octo-demo', name,
      remoteName: request.remoteName ?? 'origin', enabled: true, projectName: request.projectName ?? '',
      pollIntervalSeconds: request.pollIntervalSeconds, coalesceQueueLimit: request.coalesceQueueLimit ?? 20,
      createdAt: nowIso, updatedAt: nowIso,
    };
    this.githubMonitors.set(request.workspace, monitor);
    return clone(monitor);
  }

  async updateGitHubMonitor(request: UpdateGitHubMonitorRequest): Promise<GitHubRepositoryMonitor> {
    const existing = this.githubMonitors.get(request.workspace);
    if (!existing) throw new AgentApiError('github monitor was not found', 404, 'not_found');
    const updated: GitHubRepositoryMonitor = {
      ...existing, enabled: request.enabled, pollIntervalSeconds: request.pollIntervalSeconds,
      coalesceQueueLimit: request.coalesceQueueLimit, remoteName: request.remoteName ?? existing.remoteName,
      projectName: request.projectName ?? existing.projectName ?? '',
      updatedAt: now,
    };
    this.githubMonitors.set(request.workspace, updated);
    return clone(updated);
  }

  async deleteGitHubMonitor(workspace: string): Promise<void> {
    this.githubMonitors.delete(workspace);
  }

  async syncGitHubMonitorNow(workspace: string): Promise<GitHubSyncResult> {
    const monitor = this.githubMonitors.get(workspace);
    if (monitor) {
      monitor.lastSyncedAt = now;
      monitor.nextSyncAt = now;
    }
    return { issuesProcessed: this.githubIssues.length, pullRequestsProcessed: this.githubPulls.length, eventsMatched: 0 };
  }

  async listGitHubTriggerRules(workspace?: string): Promise<GitHubTriggerRule[]> {
    const rules = [...this.githubRules.values()];
    return (workspace ? rules.filter((rule) => rule.repository === workspace) : rules).map(clone);
  }

  async createGitHubTriggerRule(request: GitHubTriggerRuleRequest): Promise<GitHubTriggerRule> {
    const id = `mock-rule-${this.githubRules.size + 1}`;
    const rule: GitHubTriggerRule = {
      id, repository: request.workspace, name: request.name, enabled: request.enabled,
      eventKinds: request.eventKinds, filters: request.filters, promptTemplate: request.promptTemplate,
      includeBody: request.includeBody, provider: request.provider, concurrencyPolicy: request.concurrencyPolicy,
      priority: request.priority,
      ...(request.model !== undefined ? { model: request.model } : {}),
      ...(request.reasoningEffort !== undefined ? { reasoningEffort: request.reasoningEffort } : {}),
      createdAt: now, updatedAt: now,
    };
    this.githubRules.set(id, rule);
    return clone(rule);
  }

  async getGitHubTriggerRule(id: string): Promise<GitHubTriggerRule> {
    const rule = this.githubRules.get(id);
    if (!rule) throw new AgentApiError('trigger rule was not found', 404, 'not_found');
    return clone(rule);
  }

  async updateGitHubTriggerRule(id: string, request: GitHubTriggerRuleRequest): Promise<GitHubTriggerRule> {
    const existing = this.githubRules.get(id);
    if (!existing) throw new AgentApiError('trigger rule was not found', 404, 'not_found');
    const { model: existingModel, reasoningEffort: existingReasoningEffort, ...existingRest } = existing;
    void existingModel;
    void existingReasoningEffort;
    const updated: GitHubTriggerRule = {
      ...existingRest, repository: request.workspace, name: request.name, enabled: request.enabled, eventKinds: request.eventKinds,
      filters: request.filters, promptTemplate: request.promptTemplate, includeBody: request.includeBody,
      provider: request.provider, concurrencyPolicy: request.concurrencyPolicy, priority: request.priority,
      ...(request.model !== undefined ? { model: request.model } : {}),
      ...(request.reasoningEffort !== undefined ? { reasoningEffort: request.reasoningEffort } : {}),
      updatedAt: now,
    };
    this.githubRules.set(id, updated);
    return clone(updated);
  }

  async deleteGitHubTriggerRule(id: string): Promise<void> {
    this.githubRules.delete(id);
  }

  async listGitHubMonitorEvents(workspace: string, limit = 100): Promise<GitHubMonitorEvent[]> {
    return [...this.githubEvents.values()]
      .filter((githubEvent) => githubEvent.repository === workspace)
      .slice(0, limit)
      .map(clone);
  }

  async replayGitHubMonitorEvent(eventId: string): Promise<GitHubMonitorEvent> {
    const original = this.githubEvents.get(eventId);
    if (!original) throw new AgentApiError('monitor event was not found', 404, 'not_found');
    const { deliveryKey: _deliveryKey, sessionId: _sessionId, runId: _runId, ...rest } = original;
    const id = `${eventId}-replay-${this.githubEvents.size + 1}`;
    const replay: GitHubMonitorEvent = { ...rest, id, status: 'queued', replayOfEventId: eventId, createdAt: now, updatedAt: now };
    this.githubEvents.set(id, replay);
    return clone(replay);
  }

  async skipGitHubMonitorEvent(eventId: string): Promise<GitHubMonitorEvent> {
    const event = this.githubEvents.get(eventId);
    if (!event) throw new AgentApiError('monitor event was not found', 404, 'not_found');
    if (!['detected', 'matched', 'queued'].includes(event.status)) {
      throw new AgentApiError('monitor event is no longer pending', 409, 'conflict');
    }
    const skipped: GitHubMonitorEvent = { ...event, status: 'skipped', skipReason: 'manually skipped by user', updatedAt: now };
    this.githubEvents.set(eventId, skipped);
    return clone(skipped);
  }

  async listGitHubIssues(_workspace: string, query?: GitHubItemQuery): Promise<GitHubItemListResponse> {
    return { items: this.githubIssues.filter((item) => matchesMockGitHubQuery(item, query)).map(clone), fetchedAt: now };
  }

  async getGitHubIssue(_workspace: string, itemNumber: number): Promise<GitHubItem> {
    const item = this.githubIssues.find((candidate) => candidate.number === itemNumber);
    if (!item) throw new AgentApiError('issue was not found', 404, 'not_found');
    return clone(item);
  }

  async listGitHubPullRequests(_workspace: string, query?: GitHubItemQuery): Promise<GitHubItemListResponse> {
    return { items: this.githubPulls.filter((item) => matchesMockGitHubQuery(item, query)).map(clone), fetchedAt: now };
  }

  async getGitHubPullRequest(_workspace: string, itemNumber: number): Promise<GitHubItem> {
    const item = this.githubPulls.find((candidate) => candidate.number === itemNumber);
    if (!item) throw new AgentApiError('pull request was not found', 404, 'not_found');
    return clone(item);
  }
}

export function createMockEnvironment(agentApi: MockAgentApi = new MockAgentApi()): { agentApi: AgentApi; eventStreamFactory: EventStreamFactory } {
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

function aggregateStatus(statuses: Array<'changed' | 'restored' | 'conflict'>): RestoreStatus {
  if (statuses.every((status) => status === 'restored')) return 'restored';
  if (statuses.some((status) => status === 'restored')) return 'partially_restored';
  if (statuses.some((status) => status === 'conflict')) return 'conflict';
  return 'changed';
}

function mockScenarios(): Array<{ session: AgentSession; events: SessionEvent[]; changes: ChangeSet; sourceStats: SourceStats }> {
  return [
    scenario('mock-success', 'C:/demo/success', [
      event('mock-success', 1, 'command_started', { command: 'npm test' }),
      event('mock-success', 2, 'file_change_reported', { text: 'file_change_reported' }),
      event('mock-success', 3, 'assistant_message', { text: '認証処理を確認し、テストを追加しました。' }),
      event('mock-success', 4, 'usage_reported', { model: 'gpt-5.6-sol', totalTokens: 2400, costUsd: 0.4 }),
    ], oneHunk('mock-success'), 'codex', {
      sessionId: 'mock-success',
      languages: [
        { language: 'TypeScript', files: 32, blank: 231, comment: 92, code: 2385 },
        { language: 'Go', files: 61, blank: 851, comment: 60, code: 9894 },
      ],
      total: { language: '', files: 93, blank: 1082, comment: 152, code: 12279 },
    }),
    scenario('mock-failure', 'C:/demo/failure', [event('mock-failure', 1, 'run_failed', { code: 'codex_unavailable', message: 'Mock: Codex CLI is unavailable' })], emptyChanges('mock-failure')),
    scenario('mock-copilot-failure', 'C:/demo/copilot-failure', [
      event('mock-copilot-failure', 1, 'usage_reported', { model: 'auto', actualModel: 'gpt-5.4', aiCredits: 0.125, costUsd: 0.15 }),
      event('mock-copilot-failure', 2, 'run_failed', { code: 'copilot_unavailable', message: 'Mock: GitHub Copilot CLI is unavailable' }),
    ], emptyChanges('mock-copilot-failure'), 'copilot'),
    scenario('mock-claude-failure', 'C:/demo/claude-failure', [
      event('mock-claude-failure', 1, 'usage_reported', { model: 'default', actualModel: 'claude-sonnet-5', inputTokens: 1000, cachedInputTokens: 850, outputTokens: 200, totalTokens: 1200, costUsd: 0.25 }),
      event('mock-claude-failure', 2, 'run_failed', { code: 'claude_unavailable', message: 'Mock: Claude Code CLI is unavailable' }),
    ], emptyChanges('mock-claude-failure'), 'claude'),
    scenario('mock-cancelled', 'C:/demo/cancelled', [event('mock-cancelled', 1, 'run_cancelled', {})], emptyChanges('mock-cancelled')),
    scenario('mock-multi-hunk', 'C:/demo/multi-hunk', [event('mock-multi-hunk', 1, 'assistant_message', { text: '2つのHunkを個別に戻せます。' })], multiHunk('mock-multi-hunk')),
  ];
}

function scenario(
  id: string, workspace: string, events: SessionEvent[], changes: ChangeSet,
  agent: AgentSession['agent'] = 'codex', sourceStats: SourceStats = emptySourceStats(id),
) {
  return {
    session: {
      id,
      agent,
      workspace,
      status: 'active' as const,
      triggerSource: 'manual' as const,
      createdAt: now,
    },
    events,
    changes,
    sourceStats,
  };
}

function event(sessionId: string, sequence: number, type: SessionEvent['type'], data: unknown): SessionEvent {
  return { id: `${sessionId}-event-${sequence}`, sessionId, sequence, timestamp: now, schemaVersion: 2, source: 'manager', type, data };
}

function emptyChanges(sessionId: string): ChangeSet {
  return { sessionId, runId: `${sessionId}-run`, checkpointId: `${sessionId}-checkpoint`, beforeTree: 'before', afterTree: 'after', files: [] };
}

function emptySourceStats(sessionId: string): SourceStats {
  return { sessionId, languages: [], total: { language: '', files: 0, blank: 0, comment: 0, code: 0 } };
}

function mockWorkspaceDirectory(path: string): WorkspaceFileNode[] {
  const tree: Record<string, WorkspaceFileNode[]> = {
    '': [
      { name: 'src', path: 'src', type: 'dir', hasChildren: true },
      { name: 'README.md', path: 'README.md', type: 'file' },
      { name: 'package.json', path: 'package.json', type: 'file' },
    ],
    src: [
      { name: 'auth.ts', path: 'src/auth.ts', type: 'file' },
      { name: 'config.ts', path: 'src/config.ts', type: 'file' },
    ],
  };
  return tree[path] ?? [];
}

function mockWorkspaceFileContents(): Record<string, string> {
  return {
    'src/auth.ts': 'export const enabled = true;\n',
    'src/config.ts': 'export const timeout = 30;\nexport const retries = 2;\n',
    'README.md': '# Mock Repository\n\nこれはWeb版のFileタブ用のモックデータです。\n\n- ツリー表示\n- Markdown変換表示\n',
    'package.json': '{\n  "name": "mock-repo",\n  "version": "1.0.0"\n}\n',
  };
}

function oneHunk(sessionId: string): ChangeSet {
  return {
    sessionId, runId: `${sessionId}-run`, checkpointId: `${sessionId}-checkpoint`, beforeTree: 'before', afterTree: 'after',
    files: [{
      id: `${sessionId}-file`, oldPath: 'src/auth.ts', newPath: 'src/auth.ts', kind: 'modify', restoreMode: 'hunk', status: 'changed',
      original: 'export const enabled = false;\n', modified: 'export const enabled = true;\n',
      hunks: [{ id: `${sessionId}-hunk`, oldStart: 1, oldLines: 1, newStart: 1, newLines: 1, originalText: 'export const enabled = false;\n', modifiedText: 'export const enabled = true;\n', status: 'changed' }],
    }],
  };
}

function multiHunk(sessionId: string): ChangeSet {
  const changeSet = oneHunk(sessionId);
  const file = changeSet.files[0]!;
  file.newPath = 'src/config.ts';
  file.oldPath = 'src/config.ts';
  file.hunks.push({ id: `${sessionId}-hunk-2`, oldStart: 8, oldLines: 1, newStart: 8, newLines: 2, originalText: 'timeout: 10\n', modifiedText: 'timeout: 30\nretries: 2\n', status: 'changed' });
  return changeSet;
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function mockRepoNameFromWorkspace(workspace: string): string {
  const segment = workspace.split(/[/\\]/).filter(Boolean).pop() ?? workspace;
  return segment.toLowerCase().replace(/[^a-z0-9-]+/g, '-');
}

function matchesMockGitHubQuery(item: GitHubItem, query?: GitHubItemQuery): boolean {
  if (!query) return true;
  if (query.state && query.state !== 'all' && item.state !== query.state) return false;
  if (query.assignee && !item.assignees.some((assignee) => assignee.login.toLowerCase() === query.assignee!.toLowerCase())) return false;
  if (query.author && item.author.login.toLowerCase() !== query.author.toLowerCase()) return false;
  if (query.labels?.length && !query.labels.every((label) => item.labels.some((itemLabel) => itemLabel.name.toLowerCase() === label.toLowerCase()))) {
    return false;
  }
  if (query.text) {
    const needle = query.text.toLowerCase();
    if (!item.title.toLowerCase().includes(needle) && !item.body.toLowerCase().includes(needle)) return false;
  }
  if (query.project || query.status) {
    const matched = (item.projectFields ?? []).some((field) => {
      if (query.project && field.projectTitle.toLowerCase() !== query.project.toLowerCase()) return false;
      if (query.status && (field.fieldName.toLowerCase() !== 'status' || field.value.toLowerCase() !== query.status.toLowerCase())) return false;
      return true;
    });
    if (!matched) return false;
  }
  return true;
}

function mockGitHubMonitors(): GitHubRepositoryMonitor[] {
  return [{
    repository: GITHUB_DEMO_WORKSPACE, host: 'github.com', owner: 'octo-demo', name: 'example-repo',
    remoteName: 'origin', enabled: true, pollIntervalSeconds: 300, coalesceQueueLimit: 20,
    lastSyncedAt: now, nextSyncAt: now, createdAt: now, updatedAt: now,
  }];
}

function mockGitHubTriggerRules(): GitHubTriggerRule[] {
  return [{
    id: 'mock-rule-1', repository: GITHUB_DEMO_WORKSPACE, name: 'Ready になったら設計する', enabled: true,
    eventKinds: ['issue'], filters: { project: { projectTitle: 'Roadmap', fieldName: 'Status', value: 'Ready' } },
    promptTemplate: 'Design {{.Title}} (#{{.Number}})\n\n{{.ExternalDataBlock}}', includeBody: false, provider: 'codex',
    concurrencyPolicy: 'coalesce', priority: 'medium', createdAt: now, updatedAt: now,
  }, {
    id: 'mock-rule-2', repository: GITHUB_DEMO_WORKSPACE, name: 'PRが作成されたらレビューする', enabled: true,
    eventKinds: ['pull_request'], filters: { actions: ['opened'] },
    promptTemplate: 'Review pull request #{{.Number}}: {{.Title}}', includeBody: false, provider: 'claude',
    concurrencyPolicy: 'skip', priority: 'high', createdAt: now, updatedAt: now,
  }];
}

function mockGitHubMonitorEvents(): GitHubMonitorEvent[] {
  return [
    {
      id: 'mock-event-1', repository: GITHUB_DEMO_WORKSPACE, ruleId: 'mock-rule-1', kind: 'issue', number: 42,
      action: 'updated', afterStateHash: 'hash-1', status: 'completed',
      itemSnapshot: mockGitHubIssues()[0]!, sessionId: 'mock-success', runId: 'mock-success-run',
      createdAt: now, updatedAt: now,
    },
    {
      id: 'mock-event-2', repository: GITHUB_DEMO_WORKSPACE, ruleId: 'mock-rule-2', kind: 'pull_request', number: 7,
      action: 'opened', afterStateHash: 'hash-2', status: 'skipped', skipReason: 'repository execution lock is held by another run',
      itemSnapshot: mockGitHubPullRequests()[0]!,
      createdAt: now, updatedAt: now,
    },
    {
      id: 'mock-event-3', repository: GITHUB_DEMO_WORKSPACE, ruleId: 'mock-rule-1', kind: 'issue', number: 43,
      action: 'opened', afterStateHash: 'hash-3', status: 'queued',
      itemSnapshot: mockGitHubIssues()[1]!,
      createdAt: now, updatedAt: now,
    },
  ];
}

function mockGitHubIssues(): GitHubItem[] {
  return [
    {
      kind: 'issue', number: 42, title: 'ログイン画面のバリデーションを強化する', body: '不正な入力でエラーメッセージが表示されない。',
      state: 'open', author: { login: 'alice' }, assignees: [{ login: 'bob' }], labels: [{ name: 'bug' }],
      createdAt: now, updatedAt: now, url: 'https://github.com/octo-demo/example-repo/issues/42',
      projectFields: [{ projectTitle: 'Roadmap', fieldName: 'Status', value: 'Ready' }],
    },
    {
      kind: 'issue', number: 43, title: 'ダークモードのコントラストを改善する', body: '',
      state: 'open', author: { login: 'carol' }, assignees: [], labels: [{ name: 'enhancement' }],
      createdAt: now, updatedAt: now, url: 'https://github.com/octo-demo/example-repo/issues/43',
      projectsError: 'GraphQL error: Resource not accessible by integration',
    },
  ];
}

function mockGitHubPullRequests(): GitHubItem[] {
  return [{
    kind: 'pull_request', number: 7, title: 'Add retry logic to the sync client', body: '',
    state: 'open', author: { login: 'dave' }, assignees: [{ login: 'alice' }], labels: [],
    createdAt: now, updatedAt: now, url: 'https://github.com/octo-demo/example-repo/pull/7',
    pullRequest: {
      draft: false,
      base: { ref: 'main', sha: 'abc123' },
      head: { ref: 'feature/retry', sha: 'def456' },
      requestedReviewers: [{ login: 'bob' }],
    },
  }];
}

function usagePeriodKey(timestamp: string, granularity: UsageGranularity): string {
  const date = new Date(timestamp);
  const year = date.getUTCFullYear();
  const month = String(date.getUTCMonth() + 1).padStart(2, '0');
  const day = String(date.getUTCDate()).padStart(2, '0');
  if (granularity === 'month') return `${year}-${month}`;
  if (granularity === 'week') {
    const startOfYear = Date.UTC(year, 0, 1);
    const week = Math.ceil(((date.getTime() - startOfYear) / 86_400_000 + new Date(startOfYear).getUTCDay() + 1) / 7);
    return `${year}-W${String(week).padStart(2, '0')}`;
  }
  return `${year}-${month}-${day}`;
}
