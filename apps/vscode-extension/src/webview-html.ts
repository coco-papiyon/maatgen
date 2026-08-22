export interface WebviewHtmlOptions {
  cspSource: string;
  nonce: string;
  styleUri: string;
}

export function renderWebviewHtml(options: WebviewHtmlOptions): string {
  const { cspSource, nonce, styleUri } = options;
  const imageUri = styleUri.replace(/webview\.css$/, 'maat.png');
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
        <img class="brand-mark" src="${imageUri}" aria-hidden="true" alt="Maat">
        <div>
          <h1>Maatgen</h1>
          <p>Coding Agent Manager</p>
        </div>
        <span id="provider-usage" class="provider-usage" aria-live="polite"></span>
      </header>
      <div class="top-panels">
        <details class="session-history">
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
              <span data-copilot-usage hidden>Actual model <strong id="usage-model">—</strong></span>
              <span data-copilot-usage hidden>AI credits <strong id="usage-credits">—</strong></span>
              <div class="usage-total-cost">
                <span data-token-usage>Total <strong id="usage-total">—</strong></span>
                <span>Cost <strong id="usage-cost">—</strong></span>
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
            <label>Reasoning <select id="reasoning-effort-select" aria-label="Reasoning effort"></select></label>
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
            <div class="prompt-input-wrap">
              <textarea id="prompt-input" rows="2" placeholder="Agentに指示する… (@でファイルを指定)"></textarea>
              <ul id="mention-list" class="mention-list" hidden></ul>
            </div>
            <div class="prompt-actions"><button id="new-session" type="button" class="quiet-action">New</button><button id="close-session" type="button" class="quiet-action">Close</button><button id="run-button" type="submit">Run</button><button id="cancel-button" type="button" hidden>Stop</button></div>
          </form>
        </section>
        <p id="manager-error" class="manager-error" role="alert" hidden></p>
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
      const providerUsageElement = document.getElementById('provider-usage');
      const eventList = document.getElementById('event-list');
      const promptForm = document.getElementById('prompt-form');
      const promptInput = document.getElementById('prompt-input');
      const mentionList = document.getElementById('mention-list');
      const newSessionButton = document.getElementById('new-session');
      const runButton = document.getElementById('run-button');
      const cancelButton = document.getElementById('cancel-button');
      const closeSessionButton = document.getElementById('close-session');
      const providerSelect = document.getElementById('provider-select');
      const modelSelect = document.getElementById('model-select');
      const reasoningEffortSelect = document.getElementById('reasoning-effort-select');
      const approvalCard = document.getElementById('approval-card');
      const approvalRisk = document.getElementById('approval-risk');
      const approvalCommand = document.getElementById('approval-command');
      const approvalSummary = document.getElementById('approval-summary');
      const approvalRule = document.getElementById('approval-rule');
      const managerError = document.getElementById('manager-error');
      const usageCount = document.getElementById('usage-count');
      const usageElements = {
        input: document.getElementById('usage-input'), cached: document.getElementById('usage-cached'), output: document.getElementById('usage-output'), total: document.getElementById('usage-total'), model: document.getElementById('usage-model'), credits: document.getElementById('usage-credits'), cost: document.getElementById('usage-cost'),
      };
      const changesCount = document.getElementById('changes-count');
      const changesList = document.getElementById('changes-list');
      let followLatestEvent = true;
      let pendingApprovalId = '';
      let canRestoreChanges = false;
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
      const renderUsage = (summary, provider) => {
        // Copilot is billed in AI credits. Codex and Claude Code report token
        // counts; use the provider instead of the presence of optional values
        // so stale or partial summaries cannot expose irrelevant fields.
        const isCopilot = provider === 'copilot';
        document.querySelectorAll('[data-token-usage]').forEach((element) => { element.hidden = isCopilot; });
        document.querySelectorAll('[data-copilot-usage]').forEach((element) => { element.hidden = !isCopilot; });
        usageElements.input.textContent = formatTokens(summary.inputTokens);
        usageElements.cached.textContent = formatTokens(summary.cachedInputTokens);
        usageElements.output.textContent = formatTokens(summary.outputTokens);
        usageElements.total.textContent = formatTokens(summary.totalTokens);
        usageElements.model.textContent = typeof summary.actualModel === 'string' ? summary.actualModel : '—';
        usageElements.credits.textContent = formatCredits(summary.aiCredits);
        usageElements.cost.textContent = formatCost(summary.costUsd);
        usageCount.textContent = 'available';
      };
      const renderProviderUsage = (usage) => {
        providerUsageElement.textContent = (usage?.windows || []).map((window) => window.name + ' ' + window.usedPercent + '%').join(' · ');
        providerUsageElement.title = usage?.fetchedAt ? 'Fetched ' + usage.fetchedAt : '';
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
      const isTokenUsageOnly = (text) => {
        const lines = text.split(/\\r?\\n/).map((line) => line.trim()).filter(Boolean);
        if (!lines.length || !lines.some((line) => /token\\s+usage/i.test(line))) return false;
        if (lines.length === 1 && /^token\\s+usage\\s*:\\s*\\S.*$/i.test(lines[0])) return true;
        return lines.every((line) => /^\\|.*\\|$/.test(line) || /^\\|?[\\s:|-]+\\|?$/.test(line));
      };
      const hasVisibleEventText = (event) => {
        const text = eventText(event);
        return text.trim() !== '' && !isTokenUsageOnly(text);
      };
      let lastRenderedEventsSignature = null;
      const renderEvents = (events) => {
        const visibleEvents = events.filter((event) => ['user_prompt', 'assistant_message', 'run_started', 'run_completed', 'run_failed', 'run_cancelled', 'error'].includes(event.type))
          .filter((event) => hasVisibleEventText(event));
        const signature = JSON.stringify(visibleEvents.map((event) => [event.id, event.type, eventText(event)]));
        if (signature === lastRenderedEventsSignature) return;
        lastRenderedEventsSignature = signature;
        eventList.replaceChildren();
        visibleEvents.forEach((event) => {
          const item = document.createElement('article');
          item.className = 'event-item ' + event.type;
          const label = document.createElement('span'); label.className = 'event-label'; label.textContent = event.type === 'user_prompt' ? 'U' : event.type === 'assistant_message' ? 'A' : event.type.startsWith('run_') ? 'R' : 'E';
          const body = document.createElement('div'); body.className = 'event-content';
          if (event.type === 'assistant_message') {
            body.innerHTML = renderMarkdown(eventText(event));
            const actions = document.createElement('div'); actions.className = 'response-actions';
            const openButton = document.createElement('button'); openButton.type = 'button'; openButton.textContent = 'Open in Editor';
            openButton.addEventListener('click', () => vscode.postMessage({ type: 'response.open', eventId: event.id }));
            const saveButton = document.createElement('button'); saveButton.type = 'button'; saveButton.textContent = 'Save Markdown';
            saveButton.addEventListener('click', () => vscode.postMessage({ type: 'response.save', eventId: event.id }));
            actions.append(openButton, saveButton); body.append(actions);
          } else body.textContent = eventText(event);
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
        const restorable = files.filter((file) => file.status !== 'restored');
        if (files.length) {
          const toolbar = document.createElement('div');
          toolbar.className = 'changes-toolbar';
          const summary = document.createElement('span');
          summary.textContent = restorable.length + ' restorable';
          const restoreAll = document.createElement('button');
          restoreAll.type = 'button'; restoreAll.textContent = 'Restore all';
          restoreAll.disabled = !canRestoreChanges || restorable.length === 0;
          restoreAll.addEventListener('click', () => vscode.postMessage({ type: 'change.restoreAll' }));
          toolbar.append(summary, restoreAll); changesList.append(toolbar);
        }
        files.forEach((file) => {
          const item = document.createElement('article');
          item.className = 'change-item';
          const open = document.createElement('button');
          open.type = 'button'; open.className = 'change-open';
          const path = document.createElement('strong');
          path.textContent = file.newPath || file.oldPath || file.id;
          const meta = document.createElement('span');
          meta.textContent = [file.kind, file.status, file.hunks?.length ? file.hunks.length + ' hunks' : 'file-level change'].join(' · ');
          open.append(path, meta);
          open.addEventListener('click', () => vscode.postMessage({ type: 'change.openDiff', fileId: file.id }));
          item.append(open);
          if (file.status !== 'restored') {
            const restore = document.createElement('button');
            restore.type = 'button'; restore.className = 'change-restore'; restore.textContent = 'Restore file';
            restore.disabled = !canRestoreChanges;
            restore.addEventListener('click', () => vscode.postMessage({ type: 'change.restoreFile', fileId: file.id }));
            item.append(restore);
          }
          changesList.append(item);
        });
      };
      const renderProviderOptions = (providers, selectedProvider, selectedModel, selectedReasoningEffort, hasSession, activeRun) => {
        providerSelect.replaceChildren();
        (providers || []).forEach((provider) => {
          const option = document.createElement('option');
          option.value = provider.id; option.textContent = provider.label;
          option.selected = provider.id === selectedProvider;
          providerSelect.append(option);
        });
        providerSelect.disabled = !!activeRun || !(providers || []).length;
        modelSelect.replaceChildren();
        const provider = (providers || []).find((item) => item.id === selectedProvider);
        const effectiveModel = provider?.models?.includes(selectedModel)
          ? selectedModel
          : (provider?.defaultModel && provider.models.includes(provider.defaultModel) ? provider.defaultModel : '');
        promptInput.placeholder = (provider?.label || 'Agent') + 'に指示する…';
        const defaultOption = document.createElement('option');
        defaultOption.value = ''; defaultOption.textContent = 'Default model';
        defaultOption.selected = !effectiveModel; modelSelect.append(defaultOption);
        (provider?.models || []).forEach((model) => {
          const option = document.createElement('option');
          option.value = model; option.textContent = model;
          option.selected = model === effectiveModel;
          modelSelect.append(option);
        });
        reasoningEffortSelect.replaceChildren();
        [['', 'Default'], ['low', 'Low'], ['medium', 'Medium'], ['high', 'High'], ['xhigh', 'XHigh'], ['max', 'Max']].forEach(([value, label]) => {
          const option = document.createElement('option'); option.value = value; option.textContent = label;
          option.selected = value === (selectedReasoningEffort || ''); reasoningEffortSelect.append(option);
        });
        reasoningEffortSelect.disabled = !!activeRun;
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

      let mentionRange = null;
      let mentionFiles = [];
      let mentionActiveIndex = 0;
      let mentionRequestSeq = 0;
      let latestMentionRequestId = '';
      let mentionDebounceTimer;
      const closeMention = () => {
        mentionRange = null;
        mentionFiles = [];
        mentionActiveIndex = 0;
        mentionList.hidden = true;
        mentionList.replaceChildren();
      };
      const currentMentionRange = () => {
        const value = promptInput.value;
        const caret = promptInput.selectionStart;
        const uptoCaret = value.slice(0, caret);
        const at = uptoCaret.lastIndexOf('@');
        if (at === -1) return null;
        const query = uptoCaret.slice(at + 1);
        if (/\s/.test(query)) return null;
        return { start: at, end: caret, query };
      };
      const renderMentionList = () => {
        mentionList.replaceChildren();
        if (!mentionRange) return;
        if (!mentionFiles.length) {
          const empty = document.createElement('li');
          empty.className = 'mention-empty';
          empty.textContent = '一致するファイルがありません';
          mentionList.append(empty);
          mentionList.hidden = false;
          return;
        }
        mentionFiles.forEach((file, index) => {
          const item = document.createElement('li');
          item.className = 'mention-item' + (index === mentionActiveIndex ? ' active' : '');
          item.textContent = file;
          item.addEventListener('mousedown', (event) => { event.preventDefault(); applyMention(file); });
          mentionList.append(item);
        });
        mentionList.hidden = false;
      };
      const applyMention = (file) => {
        if (!mentionRange) return;
        const { start, end } = mentionRange;
        const value = promptInput.value;
        const insertion = '@' + file + ' ';
        promptInput.value = value.slice(0, start) + insertion + value.slice(end);
        const caret = start + insertion.length;
        closeMention();
        promptInput.focus();
        promptInput.setSelectionRange(caret, caret);
      };
      const updateMention = () => {
        const range = currentMentionRange();
        if (!range) { closeMention(); return; }
        if (!mentionRange || mentionRange.query !== range.query) mentionActiveIndex = 0;
        mentionRange = range;
        clearTimeout(mentionDebounceTimer);
        mentionDebounceTimer = setTimeout(() => {
          const requestId = String(++mentionRequestSeq);
          latestMentionRequestId = requestId;
          vscode.postMessage({ type: 'workspace.searchFiles', query: range.query, requestId });
        }, 120);
      };
      promptInput.addEventListener('input', updateMention);
      promptInput.addEventListener('click', updateMention);
      promptInput.addEventListener('keyup', (event) => {
        if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) updateMention();
      });
      promptInput.addEventListener('keydown', (event) => {
        if (event.ctrlKey && event.key === 'Enter') {
          event.preventDefault();
          const message = promptInput.value.trim();
          if (!message) return;
          followLatestEvent = true;
          scrollEventListToLatest();
          promptInput.value = '';
          closeMention();
          vscode.postMessage({ type: 'run.prompt', message });
          return;
        }
        if (!mentionRange || mentionList.hidden) return;
        if (event.key === 'ArrowDown') {
          if (!mentionFiles.length) return;
          event.preventDefault();
          mentionActiveIndex = (mentionActiveIndex + 1) % mentionFiles.length;
          renderMentionList();
        } else if (event.key === 'ArrowUp') {
          if (!mentionFiles.length) return;
          event.preventDefault();
          mentionActiveIndex = (mentionActiveIndex - 1 + mentionFiles.length) % mentionFiles.length;
          renderMentionList();
        } else if (event.key === 'Enter' || event.key === 'Tab') {
          if (!mentionFiles.length) return;
          event.preventDefault();
          applyMention(mentionFiles[mentionActiveIndex]);
        } else if (event.key === 'Escape') {
          event.preventDefault();
          closeMention();
        }
      });
      document.addEventListener('mousedown', (event) => {
        if (mentionRange && !event.target.closest('.prompt-input-wrap')) closeMention();
      });

      window.addEventListener('message', (event) => {
        if (event.data?.type === 'workspace.files') {
          if (!mentionRange || event.data.requestId !== latestMentionRequestId) return;
          mentionFiles = event.data.files || [];
          mentionActiveIndex = 0;
          renderMentionList();
          return;
        }
        if (event.data?.type === 'session.state') {
          const workspace = event.data.workspace;
          nameElement.textContent = workspace?.name ?? 'No workspace open';
          pathElement.textContent = workspace?.path ?? 'Open a folder to start a session.';
          statusElement.classList.toggle('connected', Boolean(workspace));
          renderSessionHistory(event.data.sessions || [], event.data.session?.id);
          emptyState.hidden = Boolean(event.data.session);
          sessionSection.hidden = !event.data.session;
          sessionStatus.textContent = event.data.approvals?.length ? 'Approval required' : (event.data.activeRunId ? 'Running' : (event.data.session?.status ?? 'Offline'));
          renderProviderOptions(event.data.providers, event.data.selectedProvider, event.data.selectedModel, event.data.selectedReasoningEffort, Boolean(event.data.session), Boolean(event.data.activeRunId));
          renderProviderUsage(event.data.providerUsage);
          newSessionButton.disabled = Boolean(event.data.activeRunId);
          runButton.hidden = Boolean(event.data.activeRunId);
          cancelButton.hidden = !event.data.activeRunId;
          closeSessionButton.disabled = Boolean(event.data.activeRunId);
          canRestoreChanges = event.data.session?.status === 'active' && !event.data.activeRunId;
          renderEvents(event.data.events || []);
          renderApproval(event.data.approvals || []);
          renderChanges(event.data.changes);
          if (event.data.usage?.summary) {
            renderUsage(event.data.usage.summary, event.data.selectedProvider);
          } else {
            usageCount.textContent = '0';
          }
          managerError.hidden = true;
          return;
        }
        if (event.data?.type === 'changes.state') {
          renderChanges(event.data.changes);
          return;
        }
        if (event.data?.type === 'manager.error') {
          managerError.textContent = event.data.message;
          managerError.hidden = false;
          return;
        }
        if (event.data?.type === 'provider-usage.state') {
          renderProviderUsage(event.data.usage);
          return;
        }
        if (event.data?.type === 'usage.summary') {
          renderUsage(event.data.summary || {}, event.data.provider);
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
        closeMention();
        vscode.postMessage({ type: 'run.prompt', message });
      });
      providerSelect.addEventListener('change', () => vscode.postMessage({ type: 'provider.select', provider: providerSelect.value }));
      modelSelect.addEventListener('change', () => vscode.postMessage({ type: 'model.select', model: modelSelect.value }));
      reasoningEffortSelect.addEventListener('change', () => vscode.postMessage({ type: 'reasoning-effort.select', reasoningEffort: reasoningEffortSelect.value }));
      newSessionButton.addEventListener('click', () => vscode.postMessage({ type: 'session.new' }));
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
