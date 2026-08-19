<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import type { AgentRun, AgentSession, ChangeSet, CommandApproval, Provider, SessionEvent, TokenUsage, ApprovalDecision } from '@maatgen/protocol';
import { AgentApiError, httpAgentApi, type AgentApi, type SessionUsage, type SourceStats } from './api';
import { SessionEventStream, type EventStreamFactory, type EventStreamLike, type EventStreamState } from './event-stream';
import { renderMarkdown } from './markdown';

const props = defineProps<{
  agentApi?: AgentApi;
  eventStreamFactory?: EventStreamFactory;
}>();
const api = props.agentApi ?? httpAgentApi;
const createEventStream = props.eventStreamFactory ?? ((options) => new SessionEventStream(options));

const sessions = ref<AgentSession[]>([]);
const providers = ref<Provider[]>([]);
const providerStorageKey = 'maatgen.provider';
const storedProvider = localStorage.getItem(providerStorageKey) as AgentSession['agent'] | null;
const newSessionProvider = ref<AgentSession['agent']>(storedProvider || 'codex');
const selectedModel = ref('');
const nextSessionCursor = ref('');
const loadingMoreSessions = ref(false);
const selected = ref<AgentSession>();
const events = ref<SessionEvent[]>([]);
const changes = ref<ChangeSet>(emptyChangeSet(''));
const usage = ref<SessionUsage>(emptySessionUsage(''));
const sourceStats = ref<SourceStats>(emptySourceStats(''));
const approvals = ref<CommandApproval[]>([]);
const approvalRule = ref('');
const workspace = ref('');
const prompt = ref('');
const activeRun = ref<AgentRun>();
const busy = ref(false);
const error = ref('');
const streamError = ref('');
const streamState = ref<EventStreamState>('disconnected');
const diagnostic = ref<{ kind: 'manager' | 'auth' | 'codex' | 'claude' | 'copilot'; title: string; message: string }>();
const selectedChangeId = ref('');
const selectedRunId = ref('');
type SidePanel = 'usage' | 'changes' | 'sourceStats';
const storedSidePanel = localStorage.getItem('maatgen.sidePanel');
const activeSidePanel = ref<SidePanel>(
  storedSidePanel === 'usage' || storedSidePanel === 'sourceStats' ? storedSidePanel : 'changes',
);
const showSystemMessages = ref(localStorage.getItem('maatgen.showSystemMessages') === 'true');
let sessionPollTimer: number | undefined;
let eventStream: EventStreamLike | undefined;
const timelineElement = ref<HTMLElement>();

const lastSequence = computed(() => events.value.at(-1)?.sequence ?? 0);
const visibleEvents = computed(() => showSystemMessages.value
  ? events.value
  : events.value.filter((event) => !['command_started', 'command_completed', 'file_change_reported'].includes(event.type)));
const isActive = computed(() => selected.value?.status === 'active');
const selectedChange = computed(() => changes.value.files.find((file) => file.id === selectedChangeId.value));
const selectedRunEntry = computed(() => usage.value.runs.find((entry) => entry.run.id === selectedRunId.value));
const selectedRunEvents = computed(() => events.value.filter((event) => event.runId === selectedRunId.value));
const selectedRunCommands = computed(() => {
  type Command = { id: string; command: string; status?: string; output?: string; error?: string; exitCode?: number };
  const commands = new Map<string, Command>();
  for (const event of selectedRunEvents.value) {
    if (event.type !== 'command_started' && event.type !== 'command_completed') continue;
    const data = event.data as Record<string, unknown> | undefined;
    const id = typeof data?.itemId === 'string' ? data.itemId : event.id;
    const current = commands.get(id) ?? { id, command: '' };
    if (typeof data?.command === 'string') current.command = data.command;
    if (typeof data?.status === 'string') current.status = data.status;
    if (typeof data?.aggregatedOutput === 'string') current.output = data.aggregatedOutput;
    if (data?.result !== undefined) current.output = typeof data.result === 'string' ? data.result : JSON.stringify(data.result, null, 2);
    if (data?.error !== undefined) current.error = typeof data.error === 'string' ? data.error : JSON.stringify(data.error, null, 2);
    if (typeof data?.exitCode === 'number') current.exitCode = data.exitCode;
    commands.set(id, current);
  }
  return [...commands.values()];
});
const activeProvider = computed(() => selected.value?.agent ?? newSessionProvider.value);
const pendingApproval = computed(() => approvals.value.find((approval) => approval.status === 'pending'));
const availableModels = computed(() => providers.value.find((provider) => provider.id === activeProvider.value)?.models ?? []);
const providerLabel = computed(() => providers.value.find((provider) => provider.id === activeProvider.value)?.label ?? activeProvider.value);
const restorableChanges = computed(() => changes.value.files.reduce((total, file) => {
  if (file.restoreMode === 'file') return total + (file.status !== 'restored' ? 1 : 0);
  return total + file.hunks.filter((hunk) => hunk.status !== 'restored').length;
}, 0));
const statusLabel = computed(() => {
  if (!selected.value) return '待機中';
  if (pendingApproval.value) return 'コマンド承認待ち';
  if (activeRun.value) return `${providerLabel.value} 実行中`;
  return selected.value.status === 'active' ? '準備完了' : '終了済み';
});
const streamLabel = computed(() => ({
  connecting: '接続中',
  connected: 'Live',
  reconnecting: '再接続中',
  disconnected: 'Offline',
})[streamState.value]);

function scrollTimelineToBottom() {
  void nextTick(() => {
    const element = timelineElement.value;
    if (element) element.scrollTop = element.scrollHeight;
  });
}

function restoreActiveRun(items: SessionEvent[]) {
  const terminalRunIDs = new Set(items
    .filter((event) => ['run_completed', 'run_failed', 'run_cancelled'].includes(event.type))
    .map((event) => event.runId)
    .filter((id): id is string => Boolean(id)));
  const started = [...items].reverse().find((event) => event.type === 'run_started'
    && Boolean(event.runId)
    && !terminalRunIDs.has(event.runId!));
  if (!started?.runId) {
    if (activeRun.value && terminalRunIDs.has(activeRun.value.id)) activeRun.value = undefined;
    return;
  }
  const userPrompt = [...items].reverse().find((event) => event.type === 'user_prompt' && event.runId === started.runId);
  activeRun.value = {
    id: started.runId,
    sessionId: started.sessionId,
    status: 'running',
    prompt: userPrompt ? eventText(userPrompt) : '',
    startedAt: started.timestamp,
  };
}

function toggleSystemMessages() {
  localStorage.setItem('maatgen.showSystemMessages', String(showSystemMessages.value));
}

function selectSidePanel(panel: SidePanel) {
  activeSidePanel.value = panel;
  localStorage.setItem('maatgen.sidePanel', panel);
}

function selectRun(runId: string) {
  selectedRunId.value = runId;
}

function closeRunDetail() {
  selectedRunId.value = '';
}

function persistNewSessionProvider() {
  localStorage.setItem(providerStorageKey, newSessionProvider.value);
}

function eventText(event: SessionEvent): string {
  const data = event.data as Record<string, unknown> | undefined;
  if (typeof data?.text === 'string') return data.text;
  if (typeof data?.message === 'string') return data.message;
  if (typeof data?.command === 'string') return data.command;
  if (event.type === 'run_started') return `${providerLabel.value}が作業を開始しました`;
  if (event.type === 'run_completed') return 'Runが完了しました';
  if (event.type === 'run_cancelled') return 'Runをキャンセルしました';
  if (event.type === 'usage_reported') {
    if (typeof data?.aiCredits === 'number') return `AI credits: ${formatCredits(data.aiCredits)}`;
    const total = data?.totalTokens ?? data?.total_tokens;
    return total ? `Token usage: ${String(total)}` : 'Token usageを更新しました';
  }
  return event.type.replaceAll('_', ' ');
}

function eventKind(event: SessionEvent): string {
  if (event.type === 'user_prompt') return 'user';
  if (event.type === 'assistant_message') return 'assistant';
  if (event.type.includes('failed') || event.type === 'error') return 'error';
  return 'system';
}

function eventHtml(event: SessionEvent): string {
  const data = event.data as Record<string, unknown> | undefined;
  if (event.type === 'assistant_message' || event.type === 'reasoning_summary') {
    return renderMarkdown(typeof data?.text === 'string' ? data.text : '');
  }
  return '';
}

function shortPath(path: string): string {
  const parts = path.replaceAll('\\', '/').split('/');
  return parts.slice(-2).join('/');
}

function changePath(file: ChangeSet['files'][number]): string {
  return file.newPath ?? file.oldPath ?? 'unknown';
}

function restoreTargetStatus(target: { status: string }): string {
  return target.status.replaceAll('_', ' ');
}

function emptyChangeSet(sessionId: string): ChangeSet {
  return { sessionId, runId: '', checkpointId: '', beforeTree: '', afterTree: '', files: [] };
}

function emptySessionUsage(sessionId: string): SessionUsage {
  return { sessionId, summary: { source: 'unknown' }, runs: [] };
}

function emptySourceStats(sessionId: string): SourceStats {
  return { sessionId, languages: [], total: { language: '', files: 0, blank: 0, comment: 0, code: 0 } };
}

function formatTokens(value?: number): string {
  return value === undefined ? '—' : value.toLocaleString('en-US');
}

function formatCredits(value?: number): string {
  return value === undefined ? '—' : value.toLocaleString('en-US', { maximumFractionDigits: 6 });
}

function formatCost(value?: number): string {
  return value === undefined ? '—' : `$${value.toFixed(6)}`;
}

function formatDuration(startedAt?: string, finishedAt?: string): string {
  if (!startedAt || !finishedAt) return '—';
  const milliseconds = Math.max(0, new Date(finishedAt).getTime() - new Date(startedAt).getTime());
  const seconds = Math.floor(milliseconds / 1000);
  return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
}

type TokenUsageKey = 'inputTokens' | 'cachedInputTokens' | 'outputTokens' | 'reasoningOutputTokens' | 'totalTokens';

function usageValue(usageData: TokenUsage | undefined, key: TokenUsageKey): string {
  return formatTokens(usageData?.[key]);
}

function formatApprovalRule(argv?: string[]): string {
  return (argv ?? []).map((value) => /\s|["']/.test(value) ? JSON.stringify(value) : value).join(' ');
}

function parseApprovalRule(value: string): string[] {
  const trimmed = value.trim();
  if (!trimmed) return [];
  if (trimmed.startsWith('[')) {
    const parsed = JSON.parse(trimmed) as unknown;
    if (!Array.isArray(parsed) || !parsed.every((item) => typeof item === 'string')) throw new Error('許可ルールは文字列のJSON配列で指定してください。');
    return parsed;
  }
  const tokens = trimmed.match(/"(?:\\.|[^"\\])*"|'[^']*'|\S+/g) ?? [];
  return tokens.map((token) => {
    if (token.startsWith('"')) return JSON.parse(token) as string;
    if (token.startsWith("'") && token.endsWith("'")) return token.slice(1, -1);
    return token;
  });
}

async function refreshSessions(reset = false) {
  const page = await api.listSessions(undefined, 25);
  if (reset || sessions.value.length === 0) {
    sessions.value = page.sessions;
    nextSessionCursor.value = page.nextCursor ?? '';
  } else {
    const newestIDs = new Set(page.sessions.map((session) => session.id));
    sessions.value = [...page.sessions, ...sessions.value.filter((session) => !newestIDs.has(session.id))];
  }
  if (selected.value) {
    selected.value = sessions.value.find((item) => item.id === selected.value?.id) ?? selected.value;
  }
}

async function loadMoreSessions() {
  if (!nextSessionCursor.value || loadingMoreSessions.value) return;
  loadingMoreSessions.value = true;
  try {
    const page = await api.listSessions(nextSessionCursor.value, 25);
    const known = new Set(sessions.value.map((session) => session.id));
    sessions.value.push(...page.sessions.filter((session) => !known.has(session.id)));
    nextSessionCursor.value = page.nextCursor ?? '';
  } catch (cause) {
    handleFailure(cause);
  } finally {
    loadingMoreSessions.value = false;
  }
}

async function selectSession(session: AgentSession) {
  eventStream?.stop();
  eventStream = undefined;
  selected.value = session;
  selectedRunId.value = '';
  const provider = providers.value.find((item) => item.id === session.agent);
  selectedModel.value = provider?.defaultModel && provider.models.includes(provider.defaultModel)
    ? provider.defaultModel
    : '';
  events.value = [];
  changes.value = emptyChangeSet(session.id);
  usage.value = emptySessionUsage(session.id);
  sourceStats.value = emptySourceStats(session.id);
  approvals.value = [];
  approvalRule.value = '';
  activeRun.value = undefined;
  error.value = '';
  diagnostic.value = undefined;
  await refreshSelected(true);
  // Counted once at session creation, so fetch it once here rather than on every poll.
  try {
    const stats = await api.getSourceStats(session.id);
    if (selected.value?.id === session.id) sourceStats.value = stats;
  } catch (cause) {
    handleFailure(cause);
  }
  if (selected.value?.id === session.id && selected.value.status === 'active') {
    startEventStream(session.id);
  }
}

async function persistSelectedModel() {
  try {
    await api.setProviderModel(activeProvider.value, selectedModel.value);
    const provider = providers.value.find((item) => item.id === activeProvider.value);
    if (provider) provider.defaultModel = selectedModel.value;
  } catch (cause) {
    handleFailure(cause);
  }
}

async function refreshSelected(full = false) {
  if (!selected.value) return;
  const id = selected.value.id;
  const [session, newEvents, changeSet, sessionUsage, pendingApprovals] = await Promise.all([
    api.getSession(id),
    api.getEvents(id, full ? 0 : lastSequence.value),
    api.getChanges(id),
    api.getUsage(id),
    api.listApprovals(id, true),
  ]);
  selected.value = session;
  if (full) events.value = newEvents;
  else events.value.push(...newEvents.filter((event) => event.sequence > lastSequence.value));
  restoreActiveRun(events.value);
  changes.value = changeSet;
  usage.value = sessionUsage;
  approvals.value = pendingApprovals;
  if (pendingApprovals[0] && !approvalRule.value) approvalRule.value = formatApprovalRule(pendingApprovals[0].segments[0]?.argv);
  updateDiagnosticFromEvents(newEvents);
  scrollTimelineToBottom();
}

async function refreshSelectedState(sessionId: string) {
  const [session, changeSet, sessionUsage, pendingApprovals] = await Promise.all([
    api.getSession(sessionId),
    api.getChanges(sessionId),
    api.getUsage(sessionId),
    api.listApprovals(sessionId, true),
  ]);
  if (selected.value?.id !== sessionId) return;
  selected.value = session;
  changes.value = changeSet;
  usage.value = sessionUsage;
  approvals.value = pendingApprovals;
  approvalRule.value = formatApprovalRule(pendingApprovals[0]?.segments[0]?.argv);
  if (session.status !== 'active') {
    eventStream?.stop();
    eventStream = undefined;
  }
  await refreshSessions();
}

function startEventStream(sessionId: string) {
  streamError.value = '';
  eventStream = createEventStream({
    api,
    sessionId,
    afterSequence: lastSequence.value,
    onState: (state) => {
      streamState.value = state;
      if (state === 'connected') streamError.value = '';
    },
    onError: (cause) => { streamError.value = cause.message; },
    onEvent: (event) => {
      if (selected.value?.id !== sessionId || event.sequence <= lastSequence.value) return;
      events.value.push(event);
      streamError.value = '';
      restoreActiveRun(events.value);
      updateDiagnosticFromEvents([event]);
      scrollTimelineToBottom();
      if (['change_detected', 'change_restored', 'run_completed', 'run_failed', 'run_cancelled', 'command_approval_requested', 'command_approval_decided'].includes(event.type)) {
        void refreshSelectedState(sessionId).catch((cause) => {
          handleFailure(cause);
        });
      }
    },
  });
  eventStream.start();
}

async function decideApproval(decision: ApprovalDecision) {
  if (!selected.value || !pendingApproval.value) return;
  await act(async () => {
    const ruleArgv = parseApprovalRule(approvalRule.value);
    await api.decideApproval(selected.value!.id, pendingApproval.value!.id, {
      decision,
      ...(['allow_session', 'allow_permanent'].includes(decision) ? { ruleArgv } : {}),
    });
    approvals.value = await api.listApprovals(selected.value!.id, true);
    approvalRule.value = formatApprovalRule(approvals.value[0]?.segments[0]?.argv);
  });
}

async function createSession() {
  if (!workspace.value.trim()) return;
  await act(async () => {
    const created = await api.createSession({ agent: newSessionProvider.value, workspace: workspace.value.trim() });
    // Keep the workspace input value so the Repository path remains after creating a session.
    await refreshSessions(true);
    await selectSession(created);
  });
}

async function sendPrompt() {
  if (!selected.value || !prompt.value.trim()) return;
  const message = prompt.value.trim();
  prompt.value = '';
  await act(async () => {
    activeRun.value = await api.sendMessage(selected.value!.id, {
      message,
      ...(selectedModel.value ? { model: selectedModel.value } : {}),
    });
    await refreshSelected();
    scrollTimelineToBottom();
  });
}

async function cancelRun() {
  if (!activeRun.value) return;
  await act(async () => {
    await api.cancelRun(activeRun.value!.id);
  });
}

async function closeSession() {
  if (!selected.value) return;
  await act(async () => {
    selected.value = await api.closeSession(selected.value!.id);
    eventStream?.stop();
    eventStream = undefined;
    await refreshSessions();
  });
}

async function restoreHunk(hunkId: string) {
  if (!selected.value) return;
  await act(async () => {
    changes.value = await api.restoreHunk(selected.value!.id, changes.value.checkpointId, hunkId);
  });
}

async function restoreFile(fileId: string) {
  if (!selected.value) return;
  await act(async () => {
    changes.value = await api.restoreFile(selected.value!.id, changes.value.checkpointId, fileId);
  });
}

async function restoreAll() {
  if (!selected.value || restorableChanges.value === 0) return;
  if (!window.confirm(`${restorableChanges.value}件の変更をRun開始時点へ戻しますか？`)) return;
  await act(async () => {
    changes.value = await api.restoreAllChanges(selected.value!.id, changes.value.checkpointId);
    selectedChangeId.value = '';
  });
}

async function act(action: () => Promise<void>) {
  busy.value = true;
  error.value = '';
  diagnostic.value = undefined;
  try {
    await action();
  } catch (cause) {
    handleFailure(cause);
  } finally {
    busy.value = false;
  }
}

function handleFailure(cause: unknown) {
  error.value = cause instanceof Error ? cause.message : String(cause);
  if (cause instanceof AgentApiError && cause.status === 401) {
    diagnostic.value = {
      kind: 'auth',
      title: 'Agent Managerの認証に失敗しました',
      message: 'Managerを再起動し、runtime metadataのtokenとWeb UIの接続設定を一致させてください。',
    };
  } else if (cause instanceof TypeError) {
    diagnostic.value = {
      kind: 'manager',
      title: 'Agent Managerに接続できません',
      message: 'npm run devでManagerを起動し、127.0.0.1:3100へ接続できることを確認してください。',
    };
  }
}

const cliDiagnostics: Record<string, { kind: 'codex' | 'claude' | 'copilot'; title: string; message: string }> = {
  codex_unavailable: {
    kind: 'codex',
    title: 'Codex CLIを利用できません',
    message: 'Codex CLIをインストールしてPATHを確認し、codex --versionが成功する状態にしてください。',
  },
  claude_unavailable: {
    kind: 'claude',
    title: 'Claude Code CLIを利用できません',
    message: 'Claude Code CLIをインストール・ログインしてPATHを確認し、claude --versionが成功する状態にしてください。',
  },
  copilot_unavailable: {
    kind: 'copilot',
    title: 'GitHub Copilot CLIを利用できません',
    message: 'GitHub Copilot CLIをインストール・ログインしてPATHを確認し、copilot --versionが成功する状態にしてください。',
  },
};

function updateDiagnosticFromEvents(items: SessionEvent[]) {
  const unavailable = items.find((event) => {
    const data = event.data as Record<string, unknown> | undefined;
    return event.type === 'run_failed' && String(data?.code) in cliDiagnostics;
  });
  if (unavailable) {
    const code = String((unavailable.data as Record<string, unknown> | undefined)?.code);
    diagnostic.value = cliDiagnostics[code];
  }
}

async function retryConnection() {
  await act(async () => {
    await refreshSessions(true);
    const target = selected.value
      ? sessions.value.find((session) => session.id === selected.value?.id)
      : sessions.value.find((session) => session.status === 'active') ?? sessions.value[0];
    if (target) await selectSession(target);
  });
}

function startSessionPolling() {
  sessionPollTimer = window.setInterval(() => {
    void refreshSessions().catch((cause) => {
      handleFailure(cause);
    });
  }, 10_000);
}

onMounted(async () => {
  await act(async () => {
    const [catalog, defaultWorkspace] = await Promise.all([
      api.listProviders(),
      api.getDefaultWorkspace(),
      refreshSessions(true),
    ]);
    workspace.value = defaultWorkspace;
    providers.value = catalog.providers;
    if (!providers.value.some((provider) => provider.id === newSessionProvider.value) && providers.value[0]) {
      newSessionProvider.value = providers.value[0].id;
    }
    persistNewSessionProvider();
    const firstActive = sessions.value.find((session) => session.status === 'active') ?? sessions.value[0];
    if (firstActive) await selectSession(firstActive);
  });
  startSessionPolling();
});

onBeforeUnmount(() => {
  eventStream?.stop();
  window.clearInterval(sessionPollTimer);
});
</script>

<template>
  <div class="app-shell">
    <header class="topbar">
      <div class="brand"><img src="/maat.png" class="brand-mark" alt="Maat"><span>maatgen</span></div>
      <div class="topbar-status">
        <label class="system-message-setting" title="コマンド実行やファイル編集のシステムメッセージを表示">
          <input v-model="showSystemMessages" type="checkbox" @change="toggleSystemMessages" />
          <span>System messages</span>
        </label>
        <div class="stream-state" :class="streamState" :title="streamError || `Event stream: ${streamLabel}`"><span />{{ streamLabel }}</div>
        <div class="run-state"><span class="status-dot" :class="{ working: activeRun }" />{{ statusLabel }}</div>
      </div>
    </header>

    <aside class="sidebar">
      <div class="section-heading">
        <span>Sessions</span><span class="count">{{ sessions.length }}</span>
      </div>
      <form class="new-session" @submit.prevent="createSession">
        <div class="provider-fields">
          <label>Provider
            <select v-model="newSessionProvider" :disabled="busy || providers.length < 2" @change="persistNewSessionProvider">
              <option v-for="provider in providers" :key="provider.id" :value="provider.id">{{ provider.label }}</option>
            </select>
          </label>
        </div>
        <label for="workspace">Repository path</label>
        <div class="field-row">
          <input id="workspace" v-model="workspace" placeholder="C:/path/to/repository" :disabled="busy" />
          <button type="submit" class="icon-button" :disabled="busy || !workspace.trim()" aria-label="Sessionを作成">＋</button>
        </div>
      </form>
      <nav class="session-list" aria-label="Session history">
        <button
          v-for="session in sessions"
          :key="session.id"
          class="session-item"
          :class="{ selected: selected?.id === session.id }"
          @click="selectSession(session)"
        >
          <span class="session-title">{{ shortPath(session.workspace) }}</span>
          <span class="session-meta"><span :class="['mini-dot', session.status]" />{{ session.agent }} · {{ session.status }}</span>
        </button>
      </nav>
      <button v-if="nextSessionCursor" class="load-more" :disabled="loadingMoreSessions" @click="loadMoreSessions">
        {{ loadingMoreSessions ? 'Loading…' : 'Load more' }}
      </button>
    </aside>

    <main class="conversation">
      <div v-if="selected" class="conversation-header">
        <div>
          <p class="eyebrow">DIRECT REPOSITORY SESSION</p>
          <h1>{{ shortPath(selected.workspace) }}</h1>
          <p class="path" :title="selected.workspace">{{ selected.workspace }}</p>
        </div>
        <button v-if="isActive" class="quiet-button" :disabled="busy || !!activeRun" @click="closeSession">Close session</button>
      </div>

      <section v-if="diagnostic" class="diagnostic-card" :class="diagnostic.kind" role="alert">
        <div><strong>{{ diagnostic.title }}</strong><p>{{ diagnostic.message }}</p></div>
        <button v-if="!['codex', 'claude', 'copilot'].includes(diagnostic.kind)" :disabled="busy" @click="retryConnection">再試行</button>
      </section>
      <div v-else-if="error" class="error-banner" role="alert">{{ error }}</div>

       <section v-if="selected && !selectedRunId" ref="timelineElement" class="timeline" aria-live="polite">
        <div v-if="visibleEvents.length === 0" class="empty-state compact">
          <span class="empty-symbol">⌁</span>
          <h2>{{ providerLabel }}に最初の指示を送る</h2>
          <p>対象Repositoryを直接編集します。各Run開始前にcheckpointを作成します。</p>
        </div>
        <article v-for="event in visibleEvents" :key="event.id" class="event" :class="eventKind(event)">
          <div class="event-label">{{ eventKind(event) === 'assistant' ? providerLabel.toUpperCase() : eventKind(event).toUpperCase() }}</div>
          <div v-if="event.type === 'assistant_message' || event.type === 'reasoning_summary'" class="event-body markdown-body" v-html="eventHtml(event)" />
          <div v-else class="event-body">{{ eventText(event) }}</div>
          <time>{{ new Date(event.timestamp).toLocaleTimeString('ja-JP', { hour: '2-digit', minute: '2-digit' }) }}</time>
        </article>
         <div v-if="activeRun" class="thinking"><span /><span /><span /> {{ providerLabel }} is working</div>
       </section>

       <section v-else-if="selectedRunEntry" class="run-detail" aria-live="polite">
         <header class="run-detail-header">
           <div>
             <p class="eyebrow">RUN DETAIL</p>
             <h2>{{ selectedRunEntry.run.status }} · {{ selectedRunEntry.run.id }}</h2>
             <p>{{ new Date(selectedRunEntry.run.startedAt ?? '').toLocaleString('ja-JP') }} · {{ formatDuration(selectedRunEntry.run.startedAt, selectedRunEntry.run.finishedAt) }}</p>
           </div>
           <button type="button" class="quiet-button" @click="closeRunDetail">チャットに戻る</button>
         </header>

         <div class="run-detail-scroll">
           <article class="run-detail-card run-prompt-card">
             <span class="detail-label">Prompt</span>
             <p>{{ selectedRunEntry.run.prompt }}</p>
           </article>
           <div class="run-detail-metrics">
             <article><span>Actual model</span><strong>{{ selectedRunEntry.usage?.actualModel ?? '—' }}</strong></article>
             <article><span>Exit code</span><strong>{{ selectedRunEntry.run.exitCode ?? '—' }}</strong></article>
             <article><span>Cost</span><strong>{{ formatCost(selectedRunEntry.usage?.costUsd) }}</strong></article>
           </div>
           <article class="run-detail-card">
             <h3>Usage</h3>
             <div v-if="activeProvider === 'copilot'" class="detail-token-grid">
               <div><span>AI credits</span><strong>{{ formatCredits(selectedRunEntry.usage?.aiCredits) }}</strong></div>
             </div>
             <div v-else class="detail-token-grid">
               <div><span>Input</span><strong>{{ usageValue(selectedRunEntry.usage, 'inputTokens') }}</strong></div>
               <div><span>Cached input</span><strong>{{ usageValue(selectedRunEntry.usage, 'cachedInputTokens') }}</strong></div>
               <div><span>Output</span><strong>{{ usageValue(selectedRunEntry.usage, 'outputTokens') }}</strong></div>
               <div><span>Reasoning output</span><strong>{{ usageValue(selectedRunEntry.usage, 'reasoningOutputTokens') }}</strong></div>
               <div><span>Total</span><strong>{{ usageValue(selectedRunEntry.usage, 'totalTokens') }}</strong></div>
             </div>
           </article>
           <article class="run-detail-card">
             <h3>Commands <span>{{ selectedRunCommands.length }}</span></h3>
             <div v-if="selectedRunCommands.length" class="command-list">
               <div v-for="command in selectedRunCommands" :key="command.id" class="command-detail">
                  <div class="command-detail-header"><code>{{ command.command || 'Tool execution' }}</code><span>{{ command.status ?? '—' }}{{ command.exitCode !== undefined ? ` · exit ${command.exitCode}` : '' }}</span></div>
                 <pre v-if="command.output || command.error">{{ command.error || command.output }}</pre>
               </div>
             </div>
             <p v-else class="detail-empty">このRunで実行されたコマンドはありません。</p>
           </article>
           <article v-if="selectedRunEvents.length" class="run-detail-card">
             <h3>Events <span>{{ selectedRunEvents.length }}</span></h3>
             <div class="run-event-list">
               <div v-for="event in selectedRunEvents" :key="event.id"><time>{{ new Date(event.timestamp).toLocaleTimeString('ja-JP', { hour: '2-digit', minute: '2-digit', second: '2-digit' }) }}</time><span>{{ event.type.replaceAll('_', ' ') }}</span></div>
             </div>
           </article>
         </div>
       </section>

      <section v-else class="empty-state hero">
        <span class="empty-symbol">⌘</span>
        <h1>Repositoryから始めましょう</h1>
        <p>左側にGit repositoryのパスを入力して、新しいSessionを作成します。</p>
      </section>

       <form v-if="selected && isActive && !selectedRunId" class="composer" @submit.prevent="sendPrompt">
        <textarea v-model="prompt" rows="1" :placeholder="`${providerLabel}に変更内容を伝える…`" :disabled="busy || !!activeRun" @keydown.ctrl.enter="sendPrompt" />
        <div class="composer-actions">
          <div class="run-options">
            <span>{{ providerLabel }}</span>
            <select v-model="selectedModel" :disabled="busy || !!activeRun" aria-label="Model" @change="persistSelectedModel">
              <option value="">Default model</option>
              <option v-for="model in availableModels" :key="model" :value="model">{{ model }}</option>
            </select>
          </div>
          <button v-if="activeRun" type="button" class="stop-button" :disabled="busy" aria-label="処理を停止" @click="cancelRun">停止</button>
          <button v-else type="submit" class="send-button" :disabled="busy || !prompt.trim()">Run {{ providerLabel }} <span>↗</span></button>
        </div>
      </form>
    </main>

    <aside class="changes-panel">
      <div class="side-panel-tabs" role="tablist" aria-label="詳細パネル">
        <button id="usage-tab" type="button" role="tab" :aria-selected="activeSidePanel === 'usage'" :class="{ selected: activeSidePanel === 'usage' }" @click="selectSidePanel('usage')">
          Usage <span class="tab-count">{{ usage.runs.length }}</span>
        </button>
        <button id="changes-tab" type="button" role="tab" :aria-selected="activeSidePanel === 'changes'" :class="{ selected: activeSidePanel === 'changes' }" @click="selectSidePanel('changes')">
          Changes <span class="tab-count">{{ changes.files.length }}</span>
        </button>
        <button id="source-stats-tab" type="button" role="tab" :aria-selected="activeSidePanel === 'sourceStats'" :class="{ selected: activeSidePanel === 'sourceStats' }" @click="selectSidePanel('sourceStats')">
          Source <span class="tab-count">{{ sourceStats.total.files }}</span>
        </button>
      </div>
      <div v-if="activeSidePanel === 'usage'" id="usage-panel" class="usage-section" role="tabpanel" aria-labelledby="usage-tab">
        <div class="section-heading">
          <span>Usage</span><span class="count accent">{{ usage.runs.length }}</span>
        </div>
        <div class="usage-summary">
          <template v-if="activeProvider === 'copilot'">
            <div class="usage-total usage-total-with-cost">
              <div><span>AI credits</span><strong>{{ formatCredits(usage.summary.aiCredits) }}</strong></div>
              <div><span>Cost</span><strong>{{ formatCost(usage.summary.costUsd) }}</strong></div>
            </div>
          </template>
          <template v-else>
            <div><span>Input</span><strong>{{ formatTokens(usage.summary.inputTokens) }}</strong></div>
            <div><span>Cached</span><strong>{{ formatTokens(usage.summary.cachedInputTokens) }}</strong></div>
            <div><span>Output</span><strong>{{ formatTokens(usage.summary.outputTokens) }}</strong></div>
            <div><span>Reasoning</span><strong>{{ formatTokens(usage.summary.reasoningOutputTokens) }}</strong></div>
            <div class="usage-total usage-total-with-cost">
              <div><span>Total</span><strong>{{ formatTokens(usage.summary.totalTokens) }}</strong></div>
              <div><span>Cost</span><strong>{{ formatCost(usage.summary.costUsd) }}</strong></div>
            </div>
          </template>
        </div>
        <div v-if="usage.runs.length" class="usage-run-list">
             <button v-for="entry in usage.runs" :key="entry.run.id" type="button" class="usage-run" :class="{ selected: selectedRunId === entry.run.id }" @click="selectRun(entry.run.id)">
            <div class="usage-run-header">
              <span :class="['usage-status', entry.run.status]">{{ entry.run.status }}</span>
              <time>{{ new Date(entry.run.finishedAt ?? entry.run.startedAt ?? '').toLocaleTimeString('ja-JP', { hour: '2-digit', minute: '2-digit' }) }}</time>
            </div>
            <p>{{ entry.run.prompt }}</p>
            <div v-if="activeProvider === 'copilot'" class="usage-run-values">
              <span>Actual model {{ entry.usage?.actualModel ?? '—' }}</span>
              <div class="usage-run-value-with-cost">
                <span>AI credits {{ formatCredits(entry.usage?.aiCredits) }}</span>
                <span>Cost {{ formatCost(entry.usage?.costUsd) }}</span>
              </div>
            </div>
            <div v-else class="usage-run-values">
              <span>Actual model {{ entry.usage?.actualModel ?? '—' }}</span>
              <div class="usage-run-value-with-cost">
                <span>Total {{ usageValue(entry.usage, 'totalTokens') }}</span>
                <span>Cost {{ formatCost(entry.usage?.costUsd) }}</span>
              </div>
            </div>
             </button>
        </div>
        <div v-else class="usage-empty">RunごとのUsageはまだありません。</div>
      </div>
      <div v-else-if="activeSidePanel === 'changes'" id="changes-panel" class="changes-section" role="tabpanel" aria-labelledby="changes-tab">
      <div class="section-heading">
        <span>Changes</span><span class="count accent">{{ changes.files.length }}</span>
      </div>
      <div v-if="changes.files.length" class="bulk-restore">
        <span>{{ restorableChanges }} restorable</span>
        <div>
          <button :disabled="busy || !isActive || restorableChanges === 0" class="restore-all-button" @click="restoreAll">Restore all</button>
        </div>
      </div>
      <div v-if="changes.files.length" class="change-list">
        <button v-for="file in changes.files" :key="file.id" type="button" class="change-card" @click="selectedChangeId = file.id">
          <div class="change-title"><span :class="['kind', file.kind]">{{ file.kind.slice(0, 1).toUpperCase() }}</span><span>{{ changePath(file) }}</span></div>
          <div class="change-meta">{{ file.restoreMode }} restore · {{ file.hunks.length }} hunk{{ file.hunks.length === 1 ? '' : 's' }}</div>
          <div v-for="hunk in file.hunks" :key="hunk.id" class="hunk-preview">
            <span>-{{ hunk.oldStart }},{{ hunk.oldLines }}</span><span>+{{ hunk.newStart }},{{ hunk.newLines }}</span>
          </div>
        </button>
      </div>
      <div v-else class="no-changes"><span>◇</span><p>変更はまだありません</p><small>Run完了後にGit差分が表示されます。</small></div>
      </div>
      <div v-else id="source-stats-panel" class="source-stats-section" role="tabpanel" aria-labelledby="source-stats-tab">
        <div class="section-heading">
         <span>Source</span><span class="count accent">{{ sourceStats.total.files }}</span>
        </div>
        <div v-if="sourceStats.languages.length" class="usage-summary">
          <div><span>Files</span><strong>{{ formatTokens(sourceStats.total.files) }}</strong></div>
          <div><span>Code</span><strong>{{ formatTokens(sourceStats.total.code) }}</strong></div>
        </div>
        <div v-if="sourceStats.languages.length" class="source-stats-list">
          <div v-for="language in sourceStats.languages" :key="language.language" class="source-stats-row">
            <span class="source-stats-language">{{ language.language }}</span>
            <span class="source-stats-files">{{ formatTokens(language.files) }} files</span>
            <span class="source-stats-code">{{ formatTokens(language.code) }} code</span>
          </div>
        </div>
        <div v-else class="no-changes"><span>◇</span><p>Source stats have not been measured yet</p><small>Files tracked by Git are not counted, or cloc is not installed, or measurement is still in progress.</small></div>
      </div>
    </aside>

    <div v-if="pendingApproval" class="approval-backdrop">
      <section class="approval-dialog" role="dialog" aria-modal="true" aria-labelledby="approval-title">
        <header class="approval-header">
          <div>
            <p class="eyebrow">COMMAND APPROVAL</p>
            <h2 id="approval-title">コマンドの実行を許可しますか？</h2>
          </div>
          <span :class="['risk-badge', pendingApproval.risk ?? 'unknown']">{{ pendingApproval.risk ?? '未判定' }}</span>
        </header>
        <pre class="approval-command"><code>{{ pendingApproval.command }}</code></pre>
        <p v-if="pendingApproval.summary" class="approval-summary">{{ pendingApproval.summary }}</p>
        <ul v-if="pendingApproval.factors.length" class="approval-factors">
          <li v-for="factor in pendingApproval.factors" :key="factor">{{ factor }}</li>
        </ul>
        <div class="approval-segments">
          <span v-for="segment in pendingApproval.segments" :key="segment.index"><strong>{{ segment.index + 1 }}</strong><code>{{ segment.command }}</code></span>
        </div>
        <label class="approval-rule">
          許可ルール（引数ごとに空白で区切り、<code>*</code>を使用可能）
          <input v-model="approvalRule" type="text" autocomplete="off" spellcheck="false">
        </label>
        <div class="approval-actions">
          <button type="button" class="deny-button" :disabled="busy" @click="decideApproval('deny')">不許可</button>
          <button type="button" :disabled="busy" @click="decideApproval('allow_once')">今回のみ許可</button>
          <button type="button" :disabled="busy || !approvalRule.trim()" @click="decideApproval('allow_session')">セッション中許可</button>
          <button type="button" class="primary-approval" :disabled="busy || !approvalRule.trim()" @click="decideApproval('allow_permanent')">永続的に許可</button>
        </div>
      </section>
    </div>

    <div v-if="selectedChange" class="diff-backdrop" @click.self="selectedChangeId = ''">
      <section class="diff-dialog" role="dialog" aria-modal="true" :aria-label="`${changePath(selectedChange)} の差分`">
        <header class="diff-header">
          <div>
            <p class="eyebrow">CHECKPOINT DIFF</p>
            <h2>{{ changePath(selectedChange) }}</h2>
            <p>{{ selectedChange.kind }} · {{ restoreTargetStatus(selectedChange) }}</p>
          </div>
          <button class="close-dialog" aria-label="差分を閉じる" @click="selectedChangeId = ''">×</button>
        </header>

        <div class="diff-body">
          <article v-for="hunk in selectedChange.hunks" :key="hunk.id" class="diff-hunk">
            <div class="hunk-header">
              <code>@@ -{{ hunk.oldStart }},{{ hunk.oldLines }} +{{ hunk.newStart }},{{ hunk.newLines }} @@</code>
              <span :class="['restore-badge', hunk.status]">{{ restoreTargetStatus(hunk) }}</span>
            </div>
            <div class="diff-columns">
              <div class="diff-side removed"><span>Original</span><pre>{{ hunk.originalText || '∅' }}</pre></div>
              <div class="diff-side added"><span>Modified</span><pre>{{ hunk.modifiedText || '∅' }}</pre></div>
            </div>
            <div v-if="hunk.status !== 'restored' && isActive" class="restore-actions">
              <button class="restore-button" :disabled="busy" @click="restoreHunk(hunk.id)">Restore hunk</button>
            </div>
          </article>

          <article v-if="selectedChange.hunks.length === 0" class="file-restore">
            <div v-if="selectedChange.original !== undefined || selectedChange.modified !== undefined" class="diff-columns">
              <div class="diff-side removed"><span>Original</span><pre>{{ selectedChange.original || '∅' }}</pre></div>
              <div class="diff-side added"><span>Modified</span><pre>{{ selectedChange.modified || '∅' }}</pre></div>
            </div>
            <div v-else class="binary-notice">Binary、rename、またはfile mode変更です。File単位でcheckpointへ戻せます。</div>
            <div v-if="selectedChange.status !== 'restored' && isActive" class="restore-actions">
              <button class="restore-button" :disabled="busy" @click="restoreFile(selectedChange.id)">Restore file</button>
            </div>
          </article>
        </div>
      </section>
    </div>
  </div>
</template>
