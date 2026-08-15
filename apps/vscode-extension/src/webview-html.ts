export interface WebviewHtmlOptions {
  cspSource: string;
  nonce: string;
  styleUri: string;
}

export function renderWebviewHtml(options: WebviewHtmlOptions): string {
  const { cspSource, nonce, styleUri } = options;
  const csp = [
    "default-src 'none'",
    `img-src ${cspSource} https:`,
    `style-src ${cspSource}`,
    `script-src 'nonce-${nonce}'`,
  ].join('; ');

  return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8">
    <meta http-equiv="Content-Security-Policy" content="${csp}">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <link rel="stylesheet" href="${styleUri}">
    <title>Maatgen</title>
  </head>
  <body>
    <main class="extension-shell">
      <header class="extension-header">
        <span class="brand-mark" aria-hidden="true">M</span>
        <div>
          <h1>Maatgen</h1>
          <p>Coding Agent Manager</p>
        </div>
      </header>
      <section class="status-card" aria-live="polite">
        <span class="status-dot" id="workspace-status"></span>
        <div>
          <p class="eyebrow">Workspace</p>
          <p class="workspace-name" id="workspace-name">Loading...</p>
          <p class="workspace-path" id="workspace-path"></p>
        </div>
      </section>
      <section class="empty-state">
        <p class="eyebrow">Phase 7</p>
        <h2>Extension host connected</h2>
        <p>The Agent Manager connection will appear here after startup.</p>
        <button id="refresh-button" type="button">Refresh workspace</button>
      </section>
    </main>
    <script nonce="${nonce}">
      const vscode = acquireVsCodeApi();
      const nameElement = document.getElementById('workspace-name');
      const pathElement = document.getElementById('workspace-path');
      const statusElement = document.getElementById('workspace-status');

      window.addEventListener('message', (event) => {
        if (event.data?.type !== 'workspace.state') return;
        const workspace = event.data.workspace;
        nameElement.textContent = workspace?.name ?? 'No workspace open';
        pathElement.textContent = workspace?.path ?? 'Open a folder to start a session.';
        statusElement.classList.toggle('connected', Boolean(workspace));
      });

      document.getElementById('refresh-button').addEventListener('click', () => {
        vscode.postMessage({ type: 'workspace.refresh' });
      });

      vscode.postMessage({ type: 'webview.ready' });
    </script>
  </body>
</html>`;
}
