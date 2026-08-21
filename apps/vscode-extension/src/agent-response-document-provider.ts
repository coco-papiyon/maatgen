import * as path from 'node:path';
import * as vscode from 'vscode';
import type { AssistantResponse } from './assistant-response.js';

const SCHEME = 'maatgen-response';

interface ResponseDocumentEntry {
  content: string;
  fileName: string;
  workspace: string;
}

export class AgentResponseDocumentProvider implements vscode.TextDocumentContentProvider, vscode.Disposable {
  static readonly scheme = SCHEME;
  private readonly documents = new Map<string, ResponseDocumentEntry>();

  provideTextDocumentContent(uri: vscode.Uri): string {
    return this.documents.get(uri.toString())?.content ?? '';
  }

  async open(response: AssistantResponse, agent: string, workspace: string): Promise<void> {
    const uri = this.register(response, agent, workspace);
    const document = await vscode.workspace.openTextDocument(uri);
    if (document.languageId !== 'markdown') await vscode.languages.setTextDocumentLanguage(document, 'markdown');
    await vscode.commands.executeCommand('markdown.showPreview', uri);
  }

  async saveResponse(response: AssistantResponse, agent: string, workspace: string): Promise<void> {
    await this.save(this.register(response, agent, workspace));
  }

  async save(uri: vscode.Uri | undefined): Promise<void> {
    if (!uri || uri.scheme !== SCHEME) return;
    const entry = this.documents.get(uri.toString());
    if (!entry) {
      await vscode.window.showErrorMessage('The Agent response is no longer available. Open it again from the Maatgen panel.');
      return;
    }

    const workspaceFolder = vscode.workspace.workspaceFolders?.find((folder) => (
      normalizePath(folder.uri.fsPath) === normalizePath(entry.workspace)
    ));
    const defaultUri = workspaceFolder
      ? vscode.Uri.joinPath(workspaceFolder.uri, entry.fileName)
      : vscode.Uri.file(path.join(entry.workspace, entry.fileName));
    const target = await vscode.window.showSaveDialog({
      defaultUri,
      filters: { Markdown: ['md'] },
      saveLabel: 'Save Agent Response',
      title: 'Save Agent Response as Markdown',
    });
    if (!target) return;

    await vscode.workspace.fs.writeFile(target, Buffer.from(entry.content, 'utf8'));
    await vscode.window.showTextDocument(await vscode.workspace.openTextDocument(target), { preview: false });
  }

  dispose(): void {
    this.documents.clear();
  }

  private register(response: AssistantResponse, agent: string, workspace: string): vscode.Uri {
    const fileName = responseFileName(agent, response.timestamp);
    const uri = vscode.Uri.from({
      scheme: SCHEME,
      authority: 'response',
      path: `/${encodeURIComponent(response.sessionId)}/${encodeURIComponent(response.runId ?? 'session')}/${encodeURIComponent(response.eventId)}/${fileName}`,
    });
    this.documents.set(uri.toString(), { content: response.markdown, fileName, workspace });
    return uri;
  }
}

function responseFileName(agent: string, timestamp: string): string {
  const safeAgent = agent.toLowerCase().replace(/[^a-z0-9_-]+/g, '-').replace(/^-+|-+$/g, '') || 'agent';
  const parsed = new Date(timestamp);
  const stamp = Number.isNaN(parsed.getTime())
    ? 'response'
    : parsed.toISOString().replace(/\.\d{3}Z$/, 'Z').replace(/[:T]/g, '-');
  return `${safeAgent}-response-${stamp}.md`;
}

function normalizePath(value: string): string {
  const resolved = path.resolve(value);
  return process.platform === 'win32' ? resolved.toLowerCase() : resolved;
}
