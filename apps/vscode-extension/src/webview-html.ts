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
      <nav class="session-history" aria-label="Session history">
        <p class="eyebrow">SESSION HISTORY</p>
        <div id="session-history-list" class="session-history-list"></div>
      </nav>
      <section class="empty-state" id="empty-state">
        <p class="eyebrow">Phase 7</p>
        <h2>Extension host connected</h2>
        <p>The Agent Manager connection will appear here after startup.</p>
        <button id="refresh-button" type="button">Refresh workspace</button>
      </section>
      <section class="session-card" aria-live="polite" hidden>
        <div class="session-card-header"><span class="eyebrow">CODEX SESSION</span><span id="session-status">Offline</span></div>
        <div id="event-list" class="event-list"></div>
        <form id="prompt-form" class="prompt-form">
          <textarea id="prompt-input" rows="2" placeholder="Codexに指示する…"></textarea>
          <div class="prompt-actions"><button id="close-session" type="button" class="quiet-action">Close</button><button id="run-button" type="submit">Run</button><button id="cancel-button" type="button" hidden>Stop</button></div>
        </form>
      </section>
      <p id="manager-error" class="manager-error" role="alert" hidden></p>
      <section class="assistant-result" aria-live="polite" hidden>
        <p class="eyebrow">LATEST AGENT RESULT</p>
        <div id="assistant-output" class="markdown-body"></div>
      </section>
      <section class="usage-result" aria-live="polite" hidden>
        <p class="eyebrow">TOKEN USAGE</p>
        <div class="usage-grid">
          <span>Input <strong id="usage-input">—</strong></span>
          <span>Cached <strong id="usage-cached">—</strong></span>
          <span>Output <strong id="usage-output">—</strong></span>
          <span>Total <strong id="usage-total">—</strong></span>
        </div>
      </section>
      <section class="changes-result" aria-live="polite" hidden>
        <div class="result-heading"><p class="eyebrow">CHANGES</p><span id="changes-count">0 files</span></div>
        <div id="changes-list" class="changes-list"></div>
      </section>
    </main>
    <script nonce="${nonce}">
      const vscode = acquireVsCodeApi();
      const nameElement = document.getElementById('workspace-name');
      const pathElement = document.getElementById('workspace-path');
      const statusElement = document.getElementById('workspace-status');
      const sessionSection = document.querySelector('.session-card');
      const emptyState = document.getElementById('empty-state');
      const historyList = document.getElementById('session-history-list');
      const sessionStatus = document.getElementById('session-status');
      const eventList = document.getElementById('event-list');
      const promptForm = document.getElementById('prompt-form');
      const promptInput = document.getElementById('prompt-input');
      const runButton = document.getElementById('run-button');
      const cancelButton = document.getElementById('cancel-button');
      const closeSessionButton = document.getElementById('close-session');
      const managerError = document.getElementById('manager-error');
      const resultSection = document.querySelector('.assistant-result');
      const outputElement = document.getElementById('assistant-output');
      const usageSection = document.querySelector('.usage-result');
      const usageElements = {
        input: document.getElementById('usage-input'), cached: document.getElementById('usage-cached'),
        output: document.getElementById('usage-output'), total: document.getElementById('usage-total')
      };
      const changesSection = document.querySelector('.changes-result');
      const changesCount = document.getElementById('changes-count');
      const changesList = document.getElementById('changes-list');
      const formatTokens = (value) => typeof value === 'number' ? value.toLocaleString('en-US') : '—';
      const eventText = (event) => {
        const data = event.data || {};
        if (typeof data.text === 'string') return data.text;
        if (typeof data.message === 'string') return data.message;
        if (typeof data.command === 'string') return data.command;
        if (event.type === 'run_started') return 'Run started';
        if (event.type === 'run_completed') return 'Run completed';
        if (event.type === 'run_failed') return 'Run failed';
        if (event.type === 'run_cancelled') return 'Run cancelled';
        return event.type.replaceAll('_', ' ');
      };
      const renderEvents = (events) => {
        eventList.replaceChildren();
        events.filter((event) => ['user_prompt', 'assistant_message', 'run_started', 'run_completed', 'run_failed', 'run_cancelled', 'error'].includes(event.type)).forEach((event) => {
          const item = document.createElement('article');
          item.className = 'event-item ' + event.type;
          const label = document.createElement('span'); label.className = 'event-label'; label.textContent = event.type.replaceAll('_', ' ');
          const body = document.createElement('div'); body.className = 'event-content';
          if (event.type === 'assistant_message') body.innerHTML = renderMarkdown(eventText(event));
          else body.textContent = eventText(event);
          item.append(label, body); eventList.append(item);
        });
        eventList.scrollTop = eventList.scrollHeight;
      };
      const renderSessionHistory = (sessions, selectedId) => {
        historyList.replaceChildren();
        sessions.forEach((session) => {
          const button = document.createElement('button');
          button.type = 'button';
          button.className = 'history-item' + (session.id === selectedId ? ' selected' : '');
          button.disabled = session.id === selectedId;
          const title = document.createElement('strong');
          title.textContent = session.workspace.split(/[\\/]/).pop() || session.workspace;
          const meta = document.createElement('span');
          meta.textContent = session.status + ' · ' + new Date(session.createdAt).toLocaleString();
          button.append(title, meta);
          button.addEventListener('click', () => vscode.postMessage({ type: 'session.select', sessionId: session.id }));
          historyList.append(button);
        });
      };
      const renderChanges = (changeSet) => {
        changesList.replaceChildren();
        const files = changeSet?.files || [];
        changesCount.textContent = files.length + ' file' + (files.length === 1 ? '' : 's');
        files.forEach((file) => {
          const item = document.createElement('article');
          item.className = 'change-item';
          const path = document.createElement('strong');
          path.textContent = file.newPath || file.oldPath || file.id;
          const meta = document.createElement('span');
          meta.textContent = [file.status || file.kind, file.hunks?.length ? file.hunks.length + ' hunks' : 'file-level change'].join(' · ');
          item.append(path, meta);
          changesList.append(item);
        });
        changesSection.hidden = files.length === 0;
      };

      const escapeHtml = (value) => value.replace(/[&<>\"']/g, (character) => ({
        '&': '&amp;', '<': '&lt;', '>': '&gt;', '\"': '&quot;', "'": '&#39;'
      }[character] || character));
      const safeUrl = (value) => /^(https?:|mailto:)/i.test(value.trim()) ? value.trim() : undefined;
      const inlineMarkdown = (value) => {
        let html = escapeHtml(value);
        html = html.replace(/^\\[([ xX])\\]\\s+/, (match, checked) => '<input type="checkbox" disabled' + (checked.toLowerCase() === 'x' ? ' checked' : '') + '> ');
        html = html.replace(/\\*\\*([^*\\n]+)\\*\\*/g, '<strong>$1</strong>');
        html = html.replace(/\\*([^*\\n]+)\\*/g, '<em>$1</em>');
        html = html.replace(/~~([^~\\n]+)~~/g, '<del>$1</del>');
        html = html.replace(/!\\[([^\\]\\n]*)\\]\\(([^)\\n]+)\\)/g, (match, alt, url) => { const safe = safeUrl(url); return safe ? '<img src="' + escapeHtml(safe) + '" alt="' + alt + '" loading="lazy">' : alt; });
        return html.replace(/\\[([^\\]\\n]+)\\]\\(([^)\\n]+)\\)/g, (match, label, url) => {
          const safe = safeUrl(url);
          return safe ? '<a href="' + escapeHtml(safe) + '" target="_blank" rel="noopener noreferrer">' + label + '</a>' : label;
        });
      };
      const splitTableCells = (line) => line.trim().replace(/^\\|/, '').replace(/\\|$/, '').split(/(?<!\\\\)\\|/).map((cell) => cell.replace(/\\\\\\|/g, '|').trim());
      const tableAlignment = (cell) => {
        const value = cell.trim();
        if (!/^:?-{3,}:?$/.test(value)) return '';
        if (value[0] === ':' && value[value.length - 1] === ':') return 'center';
        if (value[0] === ':') return 'left';
        if (value[value.length - 1] === ':') return 'right';
        return '';
      };
      const renderMarkdown = (markdown) => {
        const lines = markdown.replace(/\\r\\n/g, '\\n').split('\\n');
        let html = '';
        for (let index = 0; index < lines.length; index += 1) {
          const line = lines[index];
          const next = lines[index + 1];
          if (line && line.indexOf('|') >= 0 && next && splitTableCells(next).every((cell) => tableAlignment(cell))) {
            const headers = splitTableCells(line);
            const alignments = splitTableCells(next).map(tableAlignment);
            html += '<div class="markdown-table-wrap"><table><thead><tr>';
            headers.forEach((header, column) => { const align = alignments[column]; html += '<th' + (align ? ' style="text-align:' + align + '"' : '') + '>' + inlineMarkdown(header) + '</th>'; });
            html += '</tr></thead><tbody>';
            index += 2;
            while (index < lines.length && lines[index] && lines[index].indexOf('|') >= 0) {
              const cells = splitTableCells(lines[index]);
              html += '<tr>';
              headers.forEach((_header, column) => { const align = alignments[column]; html += '<td' + (align ? ' style="text-align:' + align + '"' : '') + '>' + inlineMarkdown(cells[column] || '') + '</td>'; });
              html += '</tr>';
              index += 1;
            }
            html += '</tbody></table></div>';
            index -= 1;
            continue;
          }
          const heading = line.match(/^(#{1,6})\\s+(.+)$/);
          if (heading) { html += '<h' + heading[1].length + '>' + inlineMarkdown(heading[2]) + '</h' + heading[1].length + '>'; continue; }
          if (line.slice(0, 3) === String.fromCharCode(96, 96, 96)) { html += '<pre><code>'; continue; }
          if (/^\\s*[-*+]\\s+/.test(line)) { html += '<li>' + inlineMarkdown(line.replace(/^\\s*[-*+]\\s+/, '')) + '</li>'; continue; }
          if (/^\\s*([-*_])(?:\\s*\\1){2,}\\s*$/.test(line)) { html += '<hr>'; continue; }
          if (line) html += '<p>' + inlineMarkdown(line) + '</p>';
        }
        return html;
      };

      window.addEventListener('message', (event) => {
        if (event.data?.type === 'session.state') {
          const workspace = event.data.workspace;
          nameElement.textContent = workspace?.name ?? 'No workspace open';
          pathElement.textContent = workspace?.path ?? 'Open a folder to start a session.';
          statusElement.classList.toggle('connected', Boolean(workspace));
          renderSessionHistory(event.data.sessions || [], event.data.session?.id);
          emptyState.hidden = Boolean(event.data.session);
          sessionSection.hidden = !event.data.session;
          sessionStatus.textContent = event.data.activeRunId ? 'Running' : (event.data.session?.status ?? 'Offline');
          runButton.hidden = Boolean(event.data.activeRunId);
          cancelButton.hidden = !event.data.activeRunId;
          closeSessionButton.disabled = Boolean(event.data.activeRunId);
          renderEvents(event.data.events || []);
          renderChanges(event.data.changes);
          if (event.data.usage?.summary) {
            const summary = event.data.usage.summary;
            usageElements.input.textContent = formatTokens(summary.inputTokens);
            usageElements.cached.textContent = formatTokens(summary.cachedInputTokens);
            usageElements.output.textContent = formatTokens(summary.outputTokens);
            usageElements.total.textContent = formatTokens(summary.totalTokens);
            usageSection.hidden = false;
          } else {
            usageSection.hidden = true;
          }
          managerError.hidden = true;
          return;
        }
        if (event.data?.type === 'manager.error') {
          managerError.textContent = event.data.message;
          managerError.hidden = false;
          return;
        }
        if (event.data?.type === 'assistant.message') {
          outputElement.innerHTML = renderMarkdown(String(event.data.markdown || ''));
          resultSection.hidden = false;
          return;
        }
        if (event.data?.type === 'usage.summary') {
          const summary = event.data.summary || {};
          usageElements.input.textContent = formatTokens(summary.inputTokens);
          usageElements.cached.textContent = formatTokens(summary.cachedInputTokens);
          usageElements.output.textContent = formatTokens(summary.outputTokens);
          usageElements.total.textContent = formatTokens(summary.totalTokens);
          usageSection.hidden = false;
          return;
        }
        if (event.data?.type !== 'workspace.state') return;
        const workspace = event.data.workspace;
        nameElement.textContent = workspace?.name ?? 'No workspace open';
        pathElement.textContent = workspace?.path ?? 'Open a folder to start a session.';
        statusElement.classList.toggle('connected', Boolean(workspace));
      });

      document.getElementById('refresh-button').addEventListener('click', () => {
        vscode.postMessage({ type: 'workspace.refresh' });
      });
      promptForm.addEventListener('submit', (event) => {
        event.preventDefault();
        const message = promptInput.value.trim();
        if (!message) return;
        promptInput.value = '';
        vscode.postMessage({ type: 'run.prompt', message });
      });
      cancelButton.addEventListener('click', () => vscode.postMessage({ type: 'run.cancel' }));
      closeSessionButton.addEventListener('click', () => vscode.postMessage({ type: 'session.close' }));

      vscode.postMessage({ type: 'webview.ready' });
    </script>
  </body>
</html>`;
}
