<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import type { AgentRun, AgentSession, ChangeSet, Provider, SessionEvent } from '@maatgen/protocol';
import { AgentApiError, httpAgentApi, type AgentApi } from './api';
import { SessionEventStream, type EventStreamFactory, type EventStreamLike, type EventStreamState } from './event-stream';

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
const workspace = ref('');
const prompt = ref('');
const activeRun = ref<AgentRun>();
const busy = ref(false);
const error = ref('');
const streamError = ref('');
const streamState = ref<EventStreamState>('disconnected');
const diagnostic = ref<{ kind: 'manager' | 'auth' | 'codex'; title: string; message: string }>();
const selectedChangeId = ref('');
const showSystemMessages = ref(localStorage.getItem('maatgen.showSystemMessages') === 'true');
let sessionPollTimer: number | undefined;
let eventStream: EventStreamLike | undefined;

const lastSequence = computed(() => events.value.at(-1)?.sequence ?? 0);
const visibleEvents = computed(() => showSystemMessages.value
  ? events.value
  : events.value.filter((event) => !['command_started', 'command_completed', 'file_change_reported'].includes(event.type)));
const isActive = computed(() => selected.value?.status === 'active');
const selectedChange = computed(() => changes.value.files.find((file) => file.id === selectedChangeId.value));
const activeProvider = computed(() => selected.value?.agent ?? newSessionProvider.value);
const availableModels = computed(() => providers.value.find((provider) => provider.id === activeProvider.value)?.models ?? []);
const providerLabel = computed(() => providers.value.find((provider) => provider.id === activeProvider.value)?.label ?? activeProvider.value);
const restorableChanges = computed(() => changes.value.files.reduce((total, file) => {
  if (file.restoreMode === 'file') return total + (file.status !== 'restored' ? 1 : 0);
  return total + file.hunks.filter((hunk) => hunk.status !== 'restored').length;
}, 0));
const statusLabel = computed(() => {
  if (!selected.value) return '待機中';
  if (activeRun.value) return `${providerLabel.value} 実行中`;
  return selected.value.status === 'active' ? '準備完了' : '終了済み';
});
const streamLabel = computed(() => ({
  connecting: '接続中',
  connected: 'Live',
  reconnecting: '再接続中',
  disconnected: 'Offline',
})[streamState.value]);

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
  const provider = providers.value.find((item) => item.id === session.agent);
  selectedModel.value = provider?.defaultModel && provider.models.includes(provider.defaultModel)
    ? provider.defaultModel
    : '';
  events.value = [];
  changes.value = emptyChangeSet(session.id);
  activeRun.value = undefined;
  error.value = '';
  diagnostic.value = undefined;
  await refreshSelected(true);
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
  const [session, newEvents, changeSet] = await Promise.all([
    api.getSession(id),
    api.getEvents(id, full ? 0 : lastSequence.value),
    api.getChanges(id),
  ]);
  selected.value = session;
  if (full) events.value = newEvents;
  else events.value.push(...newEvents.filter((event) => event.sequence > lastSequence.value));
  restoreActiveRun(events.value);
  changes.value = changeSet;
  updateDiagnosticFromEvents(newEvents);
}

async function refreshSelectedState(sessionId: string) {
  const [session, changeSet] = await Promise.all([
    api.getSession(sessionId),
    api.getChanges(sessionId),
  ]);
  if (selected.value?.id !== sessionId) return;
  selected.value = session;
  changes.value = changeSet;
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
      if (['change_detected', 'change_restored', 'run_completed', 'run_failed', 'run_cancelled'].includes(event.type)) {
        void refreshSelectedState(sessionId).catch((cause) => {
          handleFailure(cause);
        });
      }
    },
  });
  eventStream.start();
}

async function createSession() {
  if (!workspace.value.trim()) return;
  await act(async () => {
    const created = await api.createSession({ agent: newSessionProvider.value, workspace: workspace.value.trim() });
    workspace.value = '';
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

function updateDiagnosticFromEvents(items: SessionEvent[]) {
  const unavailable = items.some((event) => {
    const data = event.data as Record<string, unknown> | undefined;
    return event.type === 'run_failed' && data?.code === 'codex_unavailable';
  });
  if (unavailable) {
    diagnostic.value = {
      kind: 'codex',
      title: 'Codex CLIを利用できません',
      message: 'Codex CLIをインストールしてPATHを確認し、codex --versionが成功する状態にしてください。',
    };
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
      <div class="brand"><span class="brand-mark">M</span><span>maatgen</span></div>
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
        <button v-if="diagnostic.kind !== 'codex'" :disabled="busy" @click="retryConnection">再試行</button>
      </section>
      <div v-else-if="error" class="error-banner" role="alert">{{ error }}</div>

      <section v-if="selected" class="timeline" aria-live="polite">
        <div v-if="visibleEvents.length === 0" class="empty-state compact">
          <span class="empty-symbol">⌁</span>
          <h2>{{ providerLabel }}に最初の指示を送る</h2>
          <p>対象Repositoryを直接編集します。各Run開始前にcheckpointを作成します。</p>
        </div>
        <article v-for="event in visibleEvents" :key="event.id" class="event" :class="eventKind(event)">
          <div class="event-label">{{ eventKind(event) === 'assistant' ? providerLabel.toUpperCase() : eventKind(event).toUpperCase() }}</div>
          <div class="event-body">{{ eventText(event) }}</div>
          <time>{{ new Date(event.timestamp).toLocaleTimeString('ja-JP', { hour: '2-digit', minute: '2-digit' }) }}</time>
        </article>
        <div v-if="activeRun" class="thinking"><span /><span /><span /> {{ providerLabel }} is working</div>
      </section>

      <section v-else class="empty-state hero">
        <span class="empty-symbol">⌘</span>
        <h1>Repositoryから始めましょう</h1>
        <p>左側にGit repositoryのパスを入力して、新しいSessionを作成します。</p>
      </section>

      <form v-if="selected && isActive" class="composer" @submit.prevent="sendPrompt">
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
    </aside>

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
