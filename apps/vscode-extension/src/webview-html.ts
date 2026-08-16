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
      <div class="top-panels">
        <details class="session-history" open>
          <summary><span>SESSION HISTORY</span><span class="panel-chevron" aria-hidden="true">⌄</span></summary>
          <div id="session-history-list" class="session-history-list"></div>
        </details>
        <details class="usage-result">
          <summary><span>USAGE</span><span id="usage-count" class="panel-count">0</span><span class="panel-chevron" aria-hidden="true">⌄</span></summary>
          <div class="usage-content">
            <div class="usage-grid">
              <span data-token-usage>Input <strong id="usage-input">—</strong></span>
              <span data-token-usage>Cached <strong id="usage-cached">—</strong></span>
              <span data-token-usage>Output <strong id="usage-output">—</strong></span>
              <div data-token-usage class="usage-total-cost">
                <span>Total <strong id="usage-total">—</strong></span>
                <span>Cost <strong id="usage-cost">—</strong></span>
              </div>
              <span data-copilot-usage hidden>Actual model <strong id="usage-model">—</strong></span>
              <div data-copilot-usage hidden class="usage-credits-cost">
                <span>AI credits <strong id="usage-credits">—</strong></span>
                <span>Cost <strong id="usage-cost-copilot">—</strong></span>
              </div>
            </div>
          </div>
        </details>
        <details class="changes-result">
          <summary><span>CHANGES</span><span id="changes-count" class="panel-count">0 files</span><span class="panel-chevron" aria-hidden="true">⌄</span></summary>
          <div class="changes-content"><div id="changes-list" class="changes-list"></div></div>
        </details>
      </div>
      <section class="chat-area">
        <section class="empty-state" id="empty-state">
          <p class="eyebrow">Phase 7</p>
          <h2>Extension host connected</h2>
          <p>The Agent Manager connection will appear here after startup.</p>
          <button id="refresh-button" type="button">Refresh workspace</button>
        </section>
        <section class="session-card" aria-live="polite" hidden>
          <div class="session-card-header"><span class="eyebrow">SESSION</span><span id="session-status">Offline</span></div>
          <div class="run-options">
            <label>Provider <select id="provider-select" aria-label="Provider"></select></label>
            <label>Model <select id="model-select" aria-label="Model"></select></label>
          </div>
          <section id="approval-card" class="approval-card" role="dialog" aria-labelledby="approval-heading" hidden>
            <div class="approval-heading"><span id="approval-heading" class="eyebrow">COMMAND APPROVAL</span><span id="approval-risk" class="approval-risk"></span></div>
            <strong>コマンドの実行を許可しますか？</strong>
            <pre id="approval-command"></pre>
            <p id="approval-summary" class="approval-summary"></p>
            <label class="approval-rule">許可ルール（<code>*</code>を使用可能）<input id="approval-rule" type="text" spellcheck="false"></label>
            <div class="approval-actions">
              <button type="button" data-approval-decision="deny" class="deny-action">不許可</button>
              <button type="button" data-approval-decision="allow_once">今回のみ</button>
              <button type="button" data-approval-decision="allow_session">セッション中</button>
              <button type="button" data-approval-decision="allow_permanent">永続的</button>
            </div>
          </section>
          <div id="event-list" class="event-list"></div>
          <form id="prompt-form" class="prompt-form">
            <textarea id="prompt-input" rows="2" placeholder="Agentに指示する…"></textarea>
            <div class="prompt-actions"><button id="close-session" type="button" class="quiet-action">Close</button><button id="run-button" type="submit">Run</button><button id="cancel-button" type="button" hidden>Stop</button></div>
          </form>
        </section>
        <p id="manager-error" class="manager-error" role="alert" hidden></p>
        <section class="assistant-result" aria-live="polite" hidden>
          <p class="eyebrow">LATEST AGENT RESULT</p>
          <div id="assistant-output" class="markdown-body"></div>
        </section>
      </section>
      <section class="status-card" aria-live="polite">
        <span class="status-dot" id="workspace-status"></span>
        <div class="workspace-inline">
          <span class="eyebrow">WORKSPACE</span>
          <strong class="workspace-name" id="workspace-name">Loading...</strong>
          <span class="workspace-path" id="workspace-path"></span>
        </div>
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
      const providerSelect = document.getElementById('provider-select');
      const modelSelect = document.getElementById('model-select');
      const approvalCard = document.getElementById('approval-card');
      const approvalRisk = document.getElementById('approval-risk');
      const approvalCommand = document.getElementById('approval-command');
      const approvalSummary = document.getElementById('approval-summary');
      const approvalRule = document.getElementById('approval-rule');
      const managerError = document.getElementById('manager-error');
      const resultSection = document.querySelector('.assistant-result');
      const outputElement = document.getElementById('assistant-output');
      const usageCount = document.getElementById('usage-count');
      const usageElements = {
        input: document.getElementById('usage-input'), cached: document.getElementById('usage-cached'), output: document.getElementById('usage-output'), total: document.getElementById('usage-total'), model: document.getElementById('usage-model'), credits: document.getElementById('usage-credits'), cost: document.getElementById('usage-cost'),
        costCopilot: document.getElementById('usage-cost-copilot')
      };
      const changesCount = document.getElementById('changes-count');
      const changesList = document.getElementById('changes-list');
      let followLatestEvent = true;
      let pendingApprovalId = '';
      const isNearEventListBottom = () => eventList.scrollHeight - eventList.clientHeight - eventList.scrollTop < 24;
      const scrollEventListToLatest = () => {
        if (followLatestEvent) eventList.scrollTop = eventList.scrollHeight;
      };
      eventList.addEventListener('scroll', () => {
        if (!isNearEventListBottom()) followLatestEvent = false;
      });
      const formatTokens = (value) => typeof value === 'number' ? value.toLocaleString('en-US') : '—';
      const formatCredits = (value) => typeof value === 'number' ? value.toLocaleString('en-US', { maximumFractionDigits: 6 }) : '—';
      const formatCost = (value) => typeof value === 'number' ? '$' + value.toFixed(6) : '—';
      const renderUsage = (summary) => {
        const isCopilotUsage = typeof summary.model === 'string' || typeof summary.aiCredits === 'number';
        document.querySelectorAll('[data-token-usage]').forEach((element) => { element.hidden = isCopilotUsage; });
        document.querySelectorAll('[data-copilot-usage]').forEach((element) => { element.hidden = !isCopilotUsage; });
        usageElements.input.textContent = formatTokens(summary.inputTokens);
        usageElements.cached.textContent = formatTokens(summary.cachedInputTokens);
        usageElements.output.textContent = formatTokens(summary.outputTokens);
        usageElements.total.textContent = formatTokens(summary.totalTokens);
        usageElements.model.textContent = typeof summary.actualModel === 'string' ? summary.actualModel : '—';
        usageElements.credits.textContent = formatCredits(summary.aiCredits);
        usageElements.cost.textContent = formatCost(summary.costUsd);
        usageElements.costCopilot.textContent = formatCost(summary.costUsd);
        usageCount.textContent = 'available';
      };
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
        scrollEventListToLatest();
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
          meta.textContent = session.agent + ' · ' + session.status + ' · ' + new Date(session.createdAt).toLocaleString();
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
        changesCount.textContent = files.length + ' file' + (files.length === 1 ? '' : 's');
      };
      const renderProviderOptions = (providers, selectedProvider, selectedModel, hasSession, activeRun) => {
        providerSelect.replaceChildren();
        (providers || []).forEach((provider) => {
          const option = document.createElement('option');
          option.value = provider.id; option.textContent = provider.label;
          option.selected = provider.id === selectedProvider;
          providerSelect.append(option);
        });
        providerSelect.disabled = hasSession || !!activeRun || !(providers || []).length;
        modelSelect.replaceChildren();
        const provider = (providers || []).find((item) => item.id === selectedProvider);
        promptInput.placeholder = (provider?.label || 'Agent') + 'に指示する…';
        const defaultOption = document.createElement('option');
        defaultOption.value = ''; defaultOption.textContent = 'Default model';
        defaultOption.selected = !selectedModel; modelSelect.append(defaultOption);
        (provider?.models || []).forEach((model) => {
          const option = document.createElement('option');
          option.value = model; option.textContent = model;
          option.selected = model === selectedModel;
          modelSelect.append(option);
        });
        modelSelect.disabled = !!activeRun || !(provider?.models || []).length;
      };

      const formatApprovalRule = (argv) => (argv || []).map((value) => /\\s|["']/.test(value) ? JSON.stringify(value) : value).join(' ');
      const parseApprovalRule = (value) => {
        const trimmed = value.trim();
        if (!trimmed) return [];
        if (trimmed.startsWith('[')) {
          const parsed = JSON.parse(trimmed);
          if (!Array.isArray(parsed) || !parsed.every((item) => typeof item === 'string')) throw new Error('許可ルールは文字列のJSON配列で指定してください。');
          return parsed;
        }
        return (trimmed.match(/"(?:\\\\.|[^"\\\\])*"|'[^']*'|\\S+/g) || []).map((token) => {
          if (token.startsWith('"')) return JSON.parse(token);
          if (token.startsWith("'") && token.endsWith("'")) return token.slice(1, -1);
          return token;
        });
      };

      const renderApproval = (approvals) => {
        const approval = (approvals || [])[0];
        approvalCard.hidden = !approval;
        pendingApprovalId = approval?.id || '';
        if (!approval) return;
        approvalRisk.textContent = approval.risk || '未判定';
        approvalRisk.className = 'approval-risk ' + (approval.risk || 'unknown');
        approvalCommand.textContent = approval.command;
        approvalSummary.textContent = approval.summary || '';
        approvalSummary.hidden = !approval.summary;
        approvalRule.value = formatApprovalRule(approval.segments?.[0]?.argv);
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
          sessionStatus.textContent = event.data.approvals?.length ? 'Approval required' : (event.data.activeRunId ? 'Running' : (event.data.session?.status ?? 'Offline'));
          renderProviderOptions(event.data.providers, event.data.selectedProvider, event.data.selectedModel, Boolean(event.data.session), Boolean(event.data.activeRunId));
          runButton.hidden = Boolean(event.data.activeRunId);
          cancelButton.hidden = !event.data.activeRunId;
          closeSessionButton.disabled = Boolean(event.data.activeRunId);
          renderEvents(event.data.events || []);
          renderApproval(event.data.approvals || []);
          renderChanges(event.data.changes);
          if (event.data.usage?.summary) {
            renderUsage(event.data.usage.summary);
          } else {
            usageCount.textContent = '0';
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
          renderUsage(event.data.summary || {});
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
        followLatestEvent = true;
        scrollEventListToLatest();
        promptInput.value = '';
        vscode.postMessage({ type: 'run.prompt', message });
      });
      providerSelect.addEventListener('change', () => vscode.postMessage({ type: 'provider.select', provider: providerSelect.value }));
      modelSelect.addEventListener('change', () => vscode.postMessage({ type: 'model.select', model: modelSelect.value }));
      cancelButton.addEventListener('click', () => vscode.postMessage({ type: 'run.cancel' }));
      closeSessionButton.addEventListener('click', () => vscode.postMessage({ type: 'session.close' }));
      document.querySelectorAll('[data-approval-decision]').forEach((button) => {
        button.addEventListener('click', () => {
          if (!pendingApprovalId) return;
          const decision = button.dataset.approvalDecision;
          let ruleArgv;
          try {
            ruleArgv = parseApprovalRule(approvalRule.value);
          } catch (error) {
            managerError.textContent = error instanceof Error ? error.message : String(error);
            managerError.hidden = false;
            return;
          }
          vscode.postMessage({
            type: 'approval.decide', approvalId: pendingApprovalId, decision,
            ...(['allow_session', 'allow_permanent'].includes(decision) ? { ruleArgv } : {}),
          });
        });
      });

      vscode.postMessage({ type: 'webview.ready' });
    </script>
  </body>
</html>`;
}
