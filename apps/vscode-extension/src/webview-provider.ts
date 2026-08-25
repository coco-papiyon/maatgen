import * as vscode from 'vscode';
import * as path from 'node:path';
import { createNonce } from './nonce.js';
import { renderWebviewHtml } from './webview-html.js';
import { AgentManagerClient, AgentManagerError, type AgentProvider, type ReasoningEffort, type SessionUsage } from './agent-manager-client.js';
import type { AgentSession, ChangeFile, ChangeSet, CommandApproval, SessionEvent } from './agent-manager-client.js';
import { CheckpointDocumentProvider } from './checkpoint-document-provider.js';
import { AgentResponseDocumentProvider } from './agent-response-document-provider.js';
import { selectAssistantResponse } from './assistant-response.js';
import { renderMarkdown } from '@maatgen/markdown';

interface WorkspaceState {
  name: string;
  path: string;
}

// Default providers configuration matching Agent Manager configuration
const DEFAULT_PROVIDERS: AgentProvider[] = [
  { id: 'codex', label: 'Codex', models: ['gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna', 'gpt-5.5', 'gpt-5.4', 'gpt-5.4-mini'], defaultModel: 'gpt-5.6-luna' },
  { id: 'claude', label: 'Claude Code', models: ['claude-opus-5', 'claude-sonnet-5', 'claude-sonnet-4-6', 'claude-haiku-4-5'] },
  { id: 'copilot', label: 'GitHub Copilot', models: ['auto', 'claude-sonnet-4.6', 'gpt-5.4', 'claude-haiku-4.5', 'gpt-5.3-codex', 'gemini-3.1-pro-preview', 'gemini-3.5-flash', 'gemini-3.6-flash', 'mai-code-1-flash'], defaultModel: 'auto' },
];

type WebviewMessage =
  | { type: 'webview.ready' }
  | { type: 'workspace.refresh' }
  | { type: 'run.prompt'; message: string }
  | { type: 'provider.select'; provider: string }
  | { type: 'model.select'; model: string }
  | { type: 'reasoning-effort.select'; reasoningEffort: string }
  | { type: 'run.cancel' }
  | { type: 'approval.decide'; approvalId: string; decision: 'allow_once' | 'allow_session' | 'allow_permanent' | 'deny'; ruleArgv?: string[] }
  | { type: 'session.select'; sessionId: string }
  | { type: 'session.new' }
  | { type: 'session.close' }
  | { type: 'change.openDiff'; fileId: string }
  | { type: 'change.restoreFile'; fileId: string }
  | { type: 'change.restoreAll' }
  | { type: 'response.open'; eventId: string }
  | { type: 'response.save'; eventId: string }
  | { type: 'workspace.searchFiles'; query: string; requestId: string }
  | { type: 'provider-usage.requestAll' };

const FILE_SEARCH_EXCLUDE = '**/{node_modules,.git,dist,out,build,.next,coverage,.venv,__pycache__}/**';
const FILE_SEARCH_MAX_CANDIDATES = 2000;
const FILE_SEARCH_MAX_RESULTS = 20;
const isReasoningEffort = (value: string): value is ReasoningEffort => ['low', 'medium', 'high', 'xhigh', 'max'].includes(value);

export class MaatgenWebviewViewProvider implements vscode.WebviewViewProvider {
  static readonly viewType = 'maatgen.sessions';

  private view: vscode.WebviewView | undefined;
  private readonly manager: AgentManagerClient;
  private session: AgentSession | undefined;
  private sessions: AgentSession[] = [];
  private providers: AgentProvider[] = [];
  private selectedProvider = 'codex';
  private selectedModel = '';
  private selectedReasoningEffort: ReasoningEffort | '' = '';
  private selectedSessionId: string | undefined;
  private events: SessionEvent[] = [];
  private pollTimer: ReturnType<typeof setInterval> | undefined;
  private syncBusy = false;
  private activeRunId: string | undefined;
  private changes: ChangeSet | undefined;
  private lastChangeRefreshSequence = 0;
  private approvals: CommandApproval[] = [];
  private providerUsage: import('./agent-manager-client.js').ProviderUsage | undefined;

  constructor(
    private readonly extensionUri: vscode.Uri,
    private readonly checkpointDocuments: CheckpointDocumentProvider,
    private readonly responseDocuments: AgentResponseDocumentProvider,
  ) {
    const config = vscode.workspace.getConfiguration('maatgen');
    this.manager = new AgentManagerClient(
      config.get('managerUrl', 'http://127.0.0.1:3100').replace(/\/$/, ''),
    );
  }

  resolveWebviewView(webviewView: vscode.WebviewView): void {
    this.view = webviewView;
    webviewView.webview.options = {
      enableScripts: true,
      localResourceRoots: [vscode.Uri.joinPath(this.extensionUri, 'media')],
    };

    const styleUri = webviewView.webview.asWebviewUri(
      vscode.Uri.joinPath(this.extensionUri, 'media', 'webview.css'),
    );
    webviewView.webview.html = renderWebviewHtml({
      cspSource: webviewView.webview.cspSource,
      nonce: createNonce(),
      styleUri: styleUri.toString(),
    });

    webviewView.webview.onDidReceiveMessage(
      (message: WebviewMessage) => {
        if (message.type === 'webview.ready' || message.type === 'workspace.refresh') {
          void this.syncSession();
        } else if (message.type === 'run.prompt') {
          void this.startRun(message.message);
        } else if (message.type === 'provider.select') {
          void this.selectProvider(message.provider);
        } else if (message.type === 'model.select') {
          this.selectedModel = message.model;
        } else if (message.type === 'reasoning-effort.select') {
          this.selectedReasoningEffort = isReasoningEffort(message.reasoningEffort) ? message.reasoningEffort : '';
        } else if (message.type === 'run.cancel' && this.activeRunId) {
          void this.manager.cancelRun(this.activeRunId).then(() => this.syncSession());
        } else if (message.type === 'approval.decide' && this.session) {
          void this.decideApproval(message);
        } else if (message.type === 'session.select') {
          this.selectedSessionId = message.sessionId;
          void this.syncSession();
        } else if (message.type === 'session.new') {
          void this.createSessionForSelectedProvider();
        } else if (message.type === 'change.openDiff') {
          void this.openChangeDiff(message.fileId);
        } else if (message.type === 'change.restoreFile') {
          void this.restoreFile(message.fileId, true);
        } else if (message.type === 'change.restoreAll') {
          void this.restoreAllChanges();
        } else if (message.type === 'response.open') {
          void this.openResponse(message.eventId);
        } else if (message.type === 'response.save') {
          void this.saveResponse(message.eventId);
        } else if (message.type === 'workspace.searchFiles') {
          void this.searchWorkspaceFiles(message.query).then((files) => {
            void this.view?.webview.postMessage({ type: 'workspace.files', requestId: message.requestId, files });
          });
        } else if (message.type === 'provider-usage.requestAll') {
          void this.requestAllProviderUsage();
        } else if (message.type === 'session.close' && this.session) {
          void this.manager.closeSession(this.session.id).then(async () => {
            this.stopPolling();
            this.session = undefined;
            this.selectedSessionId = undefined;
            this.events = [];
            this.activeRunId = undefined;
            this.changes = undefined;
            this.lastChangeRefreshSequence = 0;
            this.approvals = [];
            await this.postState(undefined, [], undefined, undefined);
          });
        }
      },
      undefined,
      [],
    );
  }

  refresh(): void {
    void this.syncSession();
  }

  dispose(): void {
    this.stopPolling();
  }

  private async syncSession(): Promise<void> {
    if (!this.view || this.syncBusy) return;
    this.syncBusy = true;
    try {
      const workspace = this.getWorkspaceState();
      if (!workspace) {
        this.session = undefined;
        this.events = [];
        this.activeRunId = undefined;
        this.changes = undefined;
        this.lastChangeRefreshSequence = 0;
        this.approvals = [];
        this.providerUsage = undefined;
        await this.postState(undefined, [], undefined, undefined);
        return;
      }
      try {
        this.providers = await this.manager.listProviders();
      } catch (error) {
        // If fetching providers fails, use default providers
        this.providers = DEFAULT_PROVIDERS;
      }
      if (!this.providers || this.providers.length === 0) {
        this.providers = DEFAULT_PROVIDERS;
      }
      if (!this.providers.some((provider) => provider.id === this.selectedProvider)) {
        this.selectedProvider = this.providers[0]?.id ?? 'codex';
      }
      this.sessions = await this.manager.listSessions();
      const previousSessionId = this.session?.id;
      const selected = this.selectedSessionId
        ? this.sessions.find((candidate) => candidate.id === this.selectedSessionId)
        : undefined;
      this.session = selected
        ?? this.session
        ?? this.sessions.find((candidate) => candidate.workspace === workspace.path && candidate.status === 'active')
        ?? await this.manager.createSession({ agent: this.selectedProvider, workspace: workspace.path });
      if (!this.session) {
        this.events = [];
        this.activeRunId = undefined;
        this.changes = undefined;
        this.lastChangeRefreshSequence = 0;
        this.approvals = [];
        this.providerUsage = undefined;
        await this.postState(undefined, [], undefined, undefined);
        return;
      }
      this.selectedProvider = this.session.agent;
      const provider = this.providers.find((candidate) => candidate.id === this.selectedProvider);
      if (previousSessionId !== this.session.id) {
        this.selectedModel = provider?.defaultModel && provider.models.includes(provider.defaultModel)
          ? provider.defaultModel
          : '';
        this.selectedReasoningEffort = '';
      } else if (this.selectedModel && !provider?.models.includes(this.selectedModel)) {
        this.selectedModel = '';
      }
      if (!this.sessions.some((candidate) => candidate.id === this.session?.id)) this.sessions = [this.session, ...this.sessions];
      const sessionChanged = previousSessionId !== this.session.id;
      const previousActiveRunId = this.activeRunId;
      const eventsPromise = this.manager.getEvents(this.session.id);
      const [session, events, usage, approvals] = await Promise.all([
        this.manager.getSession(this.session.id),
        eventsPromise,
        this.manager.getUsage(this.session.id),
        this.manager.listApprovals(this.session.id),
      ]);
      this.session = session;
      this.events = events;
      this.activeRunId = this.findActiveRunId(events);
      this.approvals = approvals;
      const runFinished = Boolean(previousActiveRunId && !this.activeRunId);
      const latestChangeRefreshSequence = this.latestChangeRefreshSequence(events);
      const shouldRefreshChanges = sessionChanged
        || !this.changes
        || runFinished
        || latestChangeRefreshSequence > this.lastChangeRefreshSequence;
      if (shouldRefreshChanges) {
        this.changes = await this.manager.getChanges(this.session.id);
        this.checkpointDocuments.updateChangeSet(this.changes);
        this.lastChangeRefreshSequence = latestChangeRefreshSequence;
      }
      await this.postState(session, events, usage, this.changes);
      this.startPolling();
      if (sessionChanged || (previousActiveRunId && !this.activeRunId)) void this.refreshProviderUsage(session.id);
    } catch (error) {
      await this.view.webview.postMessage({ type: 'manager.error', message: error instanceof Error ? error.message : String(error) });
    } finally {
      this.syncBusy = false;
    }
  }

  private startPolling(): void {
    if (this.pollTimer) return;
    this.pollTimer = setInterval(() => { void this.syncSession(); }, 1_000);
  }

  private stopPolling(): void {
    if (this.pollTimer) clearInterval(this.pollTimer);
    this.pollTimer = undefined;
  }

  private async refreshProviderUsage(sessionId: string): Promise<void> {
    try {
      const usage = await this.manager.getProviderUsage(sessionId);
      if (this.session?.id !== sessionId) return;
      this.providerUsage = usage;
      await this.view?.webview.postMessage({ type: 'provider-usage.state', usage });
    } catch {
      // Provider usage is optional and must not block the session UI.
    }
  }

  private async requestAllProviderUsage(): Promise<void> {
    const sessionId = this.session?.id;
    if (!sessionId) {
      await this.view?.webview.postMessage({ type: 'provider-usage.allState', usages: [] });
      return;
    }
    try {
      const usages = await this.manager.getAllProviderUsage(sessionId);
      if (this.session?.id !== sessionId) return;
      await this.view?.webview.postMessage({ type: 'provider-usage.allState', usages });
    } catch {
      // Provider usage is optional and must not block the session UI.
      await this.view?.webview.postMessage({ type: 'provider-usage.allState', usages: [] });
    }
  }

  private async startRun(message: string): Promise<void> {
    if (!this.session || this.session.status !== 'active' || !message.trim() || this.activeRunId) return;
    try {
      const run = await this.manager.sendMessage(this.session.id, {
        message: message.trim(),
        ...(this.selectedModel ? { model: this.selectedModel } : {}),
        ...(this.selectedReasoningEffort ? { reasoningEffort: this.selectedReasoningEffort } : {}),
      });
      this.activeRunId = run.id;
      await this.syncSession();
    } catch (error) {
      await this.view?.webview.postMessage({ type: 'manager.error', message: error instanceof Error ? error.message : String(error) });
    }
  }

  private async createSessionForSelectedProvider(): Promise<void> {
    const workspace = this.getWorkspaceState();
    if (!workspace || !this.selectedProvider || this.syncBusy) return;
    try {
      const session = await this.manager.createSession({ agent: this.selectedProvider, workspace: workspace.path });
      this.session = undefined;
      this.selectedSessionId = session.id;
      this.events = [];
      this.activeRunId = undefined;
      this.changes = undefined;
      this.lastChangeRefreshSequence = 0;
      this.approvals = [];
      await this.syncSession();
    } catch (error) {
      await this.view?.webview.postMessage({ type: 'manager.error', message: error instanceof Error ? error.message : String(error) });
    }
  }

  private async selectProvider(provider: string): Promise<void> {
    if (!provider || this.activeRunId || this.syncBusy) return;
    this.selectedProvider = provider;
    this.selectedModel = '';
    this.selectedReasoningEffort = '';
    this.selectedSessionId = undefined;
    this.session = undefined;
    this.events = [];
    this.activeRunId = undefined;
    this.changes = undefined;
    this.lastChangeRefreshSequence = 0;
    this.approvals = [];
    await this.createSessionForSelectedProvider();
  }

  private async decideApproval(message: Extract<WebviewMessage, { type: 'approval.decide' }>): Promise<void> {
    if (!this.session) return;
    try {
      await this.manager.decideApproval(this.session.id, message.approvalId, {
        decision: message.decision,
        ...(message.ruleArgv?.length ? { ruleArgv: message.ruleArgv } : {}),
      });
      await this.syncSession();
    } catch (error) {
      await this.view?.webview.postMessage({ type: 'manager.error', message: error instanceof Error ? error.message : String(error) });
    }
  }

  async openChangeDiff(fileId: string): Promise<void> {
    const changeSet = this.changes;
    const file = changeSet?.files.find((candidate) => candidate.id === fileId);
    if (!changeSet || !file) return;
    if (file.kind === 'binary' || (file.original === undefined && file.modified === undefined)) {
      await vscode.window.showInformationMessage(`${this.changePath(file)} is a ${file.kind} change and cannot be displayed as text.`);
      return;
    }
    const uris = this.checkpointDocuments.createDiffUris(changeSet, file);
    await vscode.commands.executeCommand('vscode.diff', uris.before, uris.after, `${this.changePath(file)} — Run changes`, { preview: true });
  }

  async restoreHunk(hunkId: string): Promise<void> {
    const changeSet = this.changes;
    const file = changeSet?.files.find((candidate) => candidate.hunks.some((hunk) => hunk.id === hunkId));
    if (!changeSet || !file || !(await this.ensureRestorable([file]))) return;
    try {
      await this.applyRestoredChanges(await this.manager.restoreHunk(changeSet.sessionId, changeSet.checkpointId, hunkId));
    } catch (error) {
      await this.reportRestoreError(error);
    }
  }

  async restoreFile(fileId: string, confirm = false): Promise<void> {
    const changeSet = this.changes;
    const file = changeSet?.files.find((candidate) => candidate.id === fileId);
    if (!changeSet || !file || !(await this.ensureRestorable([file]))) return;
    if (confirm) {
      const choice = await vscode.window.showWarningMessage(`Restore ${this.changePath(file)} to the Run checkpoint?`, { modal: true }, 'Restore');
      if (choice !== 'Restore') return;
    }
    try {
      await this.applyRestoredChanges(await this.manager.restoreFile(changeSet.sessionId, changeSet.checkpointId, fileId));
    } catch (error) {
      await this.reportRestoreError(error);
    }
  }

  private async restoreAllChanges(): Promise<void> {
    const changeSet = this.changes;
    const files = changeSet?.files.filter((file) => file.status !== 'restored') ?? [];
    if (!changeSet || files.length === 0 || !(await this.ensureRestorable(files))) return;
    const choice = await vscode.window.showWarningMessage(`Restore all ${files.length} changed files to the Run checkpoint?`, { modal: true }, 'Restore all');
    if (choice !== 'Restore all') return;
    try {
      await this.applyRestoredChanges(await this.manager.restoreAllChanges(changeSet.sessionId, changeSet.checkpointId));
    } catch (error) {
      await this.reportRestoreError(error);
    }
  }

  private async applyRestoredChanges(changeSet: ChangeSet): Promise<void> {
    this.changes = changeSet;
    this.checkpointDocuments.updateChangeSet(changeSet);
    await this.view?.webview.postMessage({ type: 'changes.state', changes: changeSet });
  }

  private async openResponse(eventId: string): Promise<void> {
    const response = selectAssistantResponse(this.events, eventId);
    if (!response || !this.session) {
      await vscode.window.showWarningMessage('The selected Agent response is not available.');
      return;
    }
    try {
      await this.responseDocuments.open(response, this.session.agent, this.session.workspace);
    } catch (error) {
      await this.reportResponseError(error);
    }
  }

  private async saveResponse(eventId: string): Promise<void> {
    const response = selectAssistantResponse(this.events, eventId);
    if (!response || !this.session) {
      await vscode.window.showWarningMessage('The selected Agent response is not available.');
      return;
    }
    try {
      await this.responseDocuments.saveResponse(response, this.session.agent, this.session.workspace);
    } catch (error) {
      await this.reportResponseError(error);
    }
  }

  private async reportResponseError(error: unknown): Promise<void> {
    const message = error instanceof Error ? error.message : String(error);
    await vscode.window.showErrorMessage(`Unable to use the Agent response: ${message}`);
    await this.view?.webview.postMessage({ type: 'manager.error', message });
  }

  private async ensureRestorable(files: ChangeFile[]): Promise<boolean> {
    if (!this.session || this.session.status !== 'active') {
      await vscode.window.showWarningMessage('Only an active Session can restore changes.');
      return false;
    }
    if (this.activeRunId) {
      await vscode.window.showWarningMessage('Wait for the active Run to finish before restoring changes.');
      return false;
    }
    const dirtyPaths = this.dirtyPaths(files);
    if (dirtyPaths.length > 0) {
      await vscode.window.showWarningMessage(`Save or discard unsaved edits before restoring: ${dirtyPaths.join(', ')}`);
      return false;
    }
    return true;
  }

  private dirtyPaths(files: ChangeFile[]): string[] {
    if (!this.session) return [];
    const candidates = new Map<string, string>();
    for (const file of files) {
      for (const relativePath of [file.oldPath, file.newPath]) {
        if (!relativePath) continue;
        candidates.set(this.normalizePath(path.resolve(this.session.workspace, relativePath)), relativePath);
      }
    }
    const dirty = new Set<string>();
    for (const document of vscode.workspace.textDocuments) {
      if (!document.isDirty || document.uri.scheme !== 'file') continue;
      const relativePath = candidates.get(this.normalizePath(document.uri.fsPath));
      if (relativePath) dirty.add(relativePath);
    }
    return [...dirty];
  }

  private normalizePath(value: string): string {
    const resolved = path.resolve(value);
    return process.platform === 'win32' ? resolved.toLowerCase() : resolved;
  }

  private changePath(file: ChangeFile): string {
    if (file.oldPath && file.newPath && file.oldPath !== file.newPath) return `${file.oldPath} → ${file.newPath}`;
    return file.newPath ?? file.oldPath ?? file.id;
  }

  private async reportRestoreError(error: unknown): Promise<void> {
    const message = error instanceof AgentManagerError && error.code === 'checkpoint_conflict'
      ? 'Restore was not applied because the file changed after the Run.'
      : error instanceof Error ? error.message : String(error);
    await vscode.window.showErrorMessage(message);
    await this.view?.webview.postMessage({ type: 'manager.error', message });
  }

  private async postState(session: AgentSession | undefined, events: SessionEvent[], usage: SessionUsage | undefined, changes: ChangeSet | undefined): Promise<void> {
    const renderedEvents = events.map((event) => event.type === 'assistant_message'
      ? { ...event, renderedHtml: renderMarkdown(typeof event.data?.text === 'string' ? event.data.text : '') }
      : event);
    await this.view?.webview.postMessage({
      type: 'session.state', workspace: this.getWorkspaceState(), sessions: this.sessions, session, events: renderedEvents, usage, changes, activeRunId: this.activeRunId,
      providerUsage: this.providerUsage, approvals: this.approvals, providers: this.providers, selectedProvider: this.selectedProvider, selectedModel: this.selectedModel, selectedReasoningEffort: this.selectedReasoningEffort,
    });
  }

  private findActiveRunId(events: SessionEvent[]): string | undefined {
    const terminal = new Set(events.filter((event) => ['run_completed', 'run_failed', 'run_cancelled'].includes(event.type)).map((event) => event.runId).filter(Boolean));
    return [...events].reverse().find((event) => event.type === 'run_started' && event.runId && !terminal.has(event.runId))?.runId;
  }

  private latestChangeRefreshSequence(events: SessionEvent[]): number {
    return events.reduce((latest, event) => {
      if (['change_restored', 'run_completed', 'run_failed', 'run_cancelled'].includes(event.type)) {
        return Math.max(latest, event.sequence);
      }
      return latest;
    }, 0);
  }

  private async searchWorkspaceFiles(query: string): Promise<string[]> {
    if (!this.getWorkspaceState()) return [];
    const uris = await vscode.workspace.findFiles('**/*', FILE_SEARCH_EXCLUDE, FILE_SEARCH_MAX_CANDIDATES);
    const files = uris.map((uri) => vscode.workspace.asRelativePath(uri, false));
    const trimmed = query.trim().toLowerCase();
    if (!trimmed) return [...files].sort().slice(0, FILE_SEARCH_MAX_RESULTS);
    return files
      .map((file) => ({ file, score: this.scoreFileMatch(file, trimmed) }))
      .filter((entry): entry is { file: string; score: number } => entry.score !== undefined)
      .sort((a, b) => a.score - b.score || a.file.length - b.file.length)
      .slice(0, FILE_SEARCH_MAX_RESULTS)
      .map((entry) => entry.file);
  }

  private scoreFileMatch(file: string, query: string): number | undefined {
    const lower = file.toLowerCase();
    const basename = lower.slice(lower.lastIndexOf('/') + 1);
    if (basename === query) return 0;
    if (basename.startsWith(query)) return 1;
    if (basename.includes(query)) return 2;
    if (lower.includes(query)) return 3;
    return undefined;
  }

  private getWorkspaceState(): WorkspaceState | undefined {
    const folder = vscode.workspace.workspaceFolders?.[0];
    if (!folder) return undefined;

    return {
      name: folder.name,
      path: folder.uri.fsPath,
    };
  }
}
