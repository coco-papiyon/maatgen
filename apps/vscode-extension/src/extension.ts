import * as vscode from 'vscode';
import { CheckpointDocumentProvider } from './checkpoint-document-provider.js';
import { MaatgenWebviewViewProvider } from './webview-provider.js';

export function activate(context: vscode.ExtensionContext): void {
  const checkpointDocuments = new CheckpointDocumentProvider();
  const provider = new MaatgenWebviewViewProvider(context.extensionUri, checkpointDocuments);

  context.subscriptions.push(
    vscode.workspace.registerTextDocumentContentProvider(CheckpointDocumentProvider.scheme, checkpointDocuments),
    vscode.languages.registerCodeLensProvider({ scheme: CheckpointDocumentProvider.scheme }, checkpointDocuments),
    vscode.window.registerWebviewViewProvider(MaatgenWebviewViewProvider.viewType, provider),
    vscode.commands.registerCommand('maatgen.refresh', () => provider.refresh()),
    vscode.commands.registerCommand('maatgen.restoreHunk', (hunkId: string) => provider.restoreHunk(hunkId)),
    vscode.commands.registerCommand('maatgen.restoreFile', (fileId: string) => provider.restoreFile(fileId, true)),
    vscode.commands.registerCommand('maatgen.focusPanel', () => {
      vscode.commands.executeCommand('maatgen.sessions.focus');
    }),
    checkpointDocuments,
    { dispose: () => provider.dispose() },
  );
}

export function deactivate(): void {}
