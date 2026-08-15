import * as vscode from 'vscode';
import { MaatgenWebviewViewProvider } from './webview-provider.js';

export function activate(context: vscode.ExtensionContext): void {
  const provider = new MaatgenWebviewViewProvider(context.extensionUri);

  context.subscriptions.push(
    vscode.window.registerWebviewViewProvider(MaatgenWebviewViewProvider.viewType, provider),
    vscode.commands.registerCommand('maatgen.refresh', () => provider.refresh()),
  );
}

export function deactivate(): void {}
