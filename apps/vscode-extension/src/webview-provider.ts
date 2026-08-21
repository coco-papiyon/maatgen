import * as vscode from 'vscode';
import { createNonce } from './nonce.js';
import { renderWebviewHtml } from './webview-html.js';
import { AgentManagerClient, type AgentProvider, type SessionUsage } from './agent-manager-client.js';
import type { AgentSession, ChangeSet, CommandApproval, SessionEvent } from './agent-manager-client.js';

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
  | { type: 'run.cancel' }
  | { type: 'approval.decide'; approvalId: string; decision: 'allow_once' | 'allow_session' | 'allow_permanent' | 'deny'; ruleArgv?: string[] }
  | { type: 'session.select'; sessionId: string }
  | { type: 'session.new' }
  | { type: 'session.close' }
  | { type: 'workspace.searchFiles'; query: string; requestId: string };

const FILE_SEARCH_EXCLUDE = '**/{node_modules,.git,dist,out,build,.next,coverage,.venv,__pycache__}/**';
const FILE_SEARCH_MAX_CANDIDATES = 2000;
const FILE_SEARCH_MAX_RESULTS = 20;

export class MaatgenWebviewViewProvider implements vscode.WebviewViewProvider {
  static readonly viewType = 'maatgen.sessions';

  private view: vscode.WebviewView | undefined;
  private readonly manager: AgentManagerClient;
  private session: AgentSession | undefined;
  private sessions: AgentSession[] = [];
  private providers: AgentProvider[] = [];
  private selectedProvider = 'codex';
  private selectedModel = '';
  private selectedSessionId: string | undefined;
  private events: SessionEvent[] = [];
  private pollTimer: ReturnType<typeof setInterval> | undefined;
  private syncBusy = false;
  private activeRunId: string | undefined;
  private approvals: CommandApproval[] = [];

  constructor(private readonly extensionUri: vscode.Uri) {
    const config = vscode.workspace.getConfiguration('maatgen');
    this.manager = new AgentManagerClient(
      config.get('managerUrl', 'http://127.0.0.1:3100').replace(/\/$/, ''),
      config.get('managerAuthToken', 'maatgen-local-development-token'),
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
        } else if (message.type === 'run.cancel' && this.activeRunId) {
          void this.manager.cancelRun(this.activeRunId).then(() => this.syncSession());
        } else if (message.type === 'approval.decide' && this.session) {
          void this.decideApproval(message);
        } else if (message.type === 'session.select') {
          this.selectedSessionId = message.sessionId;
          void this.syncSession();
        } else if (message.type === 'session.new') {
          void this.createSessionForSelectedProvider();
        } else if (message.type === 'workspace.searchFiles') {
          void this.searchWorkspaceFiles(message.query).then((files) => {
            void this.view?.webview.postMessage({ type: 'workspace.files', requestId: message.requestId, files });
          });
        } else if (message.type === 'session.close' && this.session) {
          void this.manager.closeSession(this.session.id).then(async () => {
            this.stopPolling();
            this.session = undefined;
            this.selectedSessionId = undefined;
            this.events = [];
            this.activeRunId = undefined;
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
        this.approvals = [];
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
        this.approvals = [];
        await this.postState(undefined, [], undefined, undefined);
        return;
      }
      this.selectedProvider = this.session.agent;
      const provider = this.providers.find((candidate) => candidate.id === this.selectedProvider);
      if (previousSessionId !== this.session.id) {
        this.selectedModel = provider?.defaultModel && provider.models.includes(provider.defaultModel)
          ? provider.defaultModel
          : '';
      } else if (this.selectedModel && !provider?.models.includes(this.selectedModel)) {
        this.selectedModel = '';
      }
      if (!this.sessions.some((candidate) => candidate.id === this.session?.id)) this.sessions = [this.session, ...this.sessions];
      const [session, events, usage, changes, approvals] = await Promise.all([
        this.manager.getSession(this.session.id),
        this.manager.getEvents(this.session.id),
        this.manager.getUsage(this.session.id),
        this.manager.getChanges(this.session.id),
        this.manager.listApprovals(this.session.id),
      ]);
      this.session = session;
      this.events = events;
      this.activeRunId = this.findActiveRunId(events);
      this.approvals = approvals;
      await this.postState(session, events, usage, changes);
      this.startPolling();
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

  private async startRun(message: string): Promise<void> {
    if (!this.session || this.session.status !== 'active' || !message.trim() || this.activeRunId) return;
    try {
      const run = await this.manager.sendMessage(this.session.id, {
        message: message.trim(),
        ...(this.selectedModel ? { model: this.selectedModel } : {}),
      });
      this.activeRunId = run.id;
      await this.syncSession();
    } catch (error) {
      await this.view?.webview.postMessage({ type: 'manager.error', message: error instanceof Error ? error.message : String(error) });
    }
  }

  private async createSessionForSelectedProvider(): Promise<void> {
    const workspace = this.getWorkspaceState();
    if (!workspace || this.session || !this.selectedProvider || this.syncBusy) return;
    try {
      await this.manager.createSession({ agent: this.selectedProvider, workspace: workspace.path });
      await this.syncSession();
    } catch (error) {
      await this.view?.webview.postMessage({ type: 'manager.error', message: error instanceof Error ? error.message : String(error) });
    }
  }

  private async selectProvider(provider: string): Promise<void> {
    if (!provider || this.activeRunId || this.syncBusy) return;
    this.selectedProvider = provider;
    this.selectedModel = '';
    this.selectedSessionId = undefined;
    this.session = undefined;
    this.events = [];
    this.activeRunId = undefined;
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

  private async postState(session: AgentSession | undefined, events: SessionEvent[], usage: SessionUsage | undefined, changes: ChangeSet | undefined): Promise<void> {
    await this.view?.webview.postMessage({
      type: 'session.state', workspace: this.getWorkspaceState(), sessions: this.sessions, session, events, usage, changes, activeRunId: this.activeRunId,
      approvals: this.approvals, providers: this.providers, selectedProvider: this.selectedProvider, selectedModel: this.selectedModel,
    });
  }

  private findActiveRunId(events: SessionEvent[]): string | undefined {
    const terminal = new Set(events.filter((event) => ['run_completed', 'run_failed', 'run_cancelled'].includes(event.type)).map((event) => event.runId).filter(Boolean));
    return [...events].reverse().find((event) => event.type === 'run_started' && event.runId && !terminal.has(event.runId))?.runId;
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
