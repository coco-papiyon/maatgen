import * as vscode from 'vscode';
import { createNonce } from './nonce.js';
import { renderWebviewHtml } from './webview-html.js';

interface WorkspaceState {
  name: string;
  path: string;
}

type WebviewMessage =
  | { type: 'webview.ready' }
  | { type: 'workspace.refresh' };

export class MaatgenWebviewViewProvider implements vscode.WebviewViewProvider {
  static readonly viewType = 'maatgen.sessions';

  private view: vscode.WebviewView | undefined;

  constructor(private readonly extensionUri: vscode.Uri) {}

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
          void this.postWorkspaceState();
        }
      },
      undefined,
      [],
    );
  }

  refresh(): void {
    void this.postWorkspaceState();
  }

  private async postWorkspaceState(): Promise<void> {
    if (!this.view) return;

    await this.view.webview.postMessage({
      type: 'workspace.state',
      workspace: this.getWorkspaceState(),
    });
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
