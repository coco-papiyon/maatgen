import { describe, expect, it } from 'vitest';
import { renderWebviewHtml } from './webview-html.js';

describe('renderWebviewHtml', () => {
  it('locks resources down and authorizes only the nonce-bearing script', () => {
    const html = renderWebviewHtml({
      cspSource: 'vscode-webview://unit-test',
      nonce: 'test-nonce',
      styleUri: 'vscode-webview://unit-test/media/webview.css',
    });

    expect(html).toContain("default-src 'none'");
    expect(html).toContain("style-src vscode-webview://unit-test");
    expect(html).toContain("script-src 'nonce-test-nonce'");
    expect(html).toContain('<script nonce="test-nonce">');
    expect(html).not.toContain("'unsafe-inline'");
    expect(html).not.toContain('connect-src');
  });

  it('loads styles through a webview resource URI', () => {
    const html = renderWebviewHtml({
      cspSource: 'vscode-webview://unit-test',
      nonce: 'test-nonce',
      styleUri: 'vscode-webview://unit-test/media/webview.css',
    });

    expect(html).toContain(
      '<link rel="stylesheet" href="vscode-webview://unit-test/media/webview.css">',
    );
  });

  it('includes session execution, history, usage, and change surfaces', () => {
    const html = renderWebviewHtml({ cspSource: 'vscode-webview://unit-test', nonce: 'n', styleUri: 'style' });
    expect(html).toContain('<div class="top-panels">');
    expect(html).toContain('<details class="session-history">');
    expect(html).toContain('<details class="usage-result">');
    expect(html).toContain('id="usage-model"');
    expect(html).toContain('id="usage-credits"');
    expect(html).toContain('id="usage-cost"');
    expect(html).toContain('<details class="changes-result">');
    expect(html).toContain('<section class="chat-area">');
    expect(html).toContain('class="workspace-inline"');
    expect(html).toContain('followLatestEvent');
    expect(html).toContain('eventList.addEventListener(\'scroll\'');
    expect(html).toContain('session-history-list');
    expect(html).toContain("type: 'session.select'");
    expect(html).toContain('changes-list');
    expect(html).toContain("type: 'change.openDiff'");
    expect(html).toContain("type: 'change.restoreFile'");
    expect(html).toContain("type: 'change.restoreAll'");
    expect(html).toContain("type: 'response.open'");
    expect(html).toContain("type: 'response.save'");
    expect(html).toContain("openButton.textContent = 'Open in Editor'");
    expect(html).toContain("saveButton.textContent = 'Save Markdown'");
    expect(html).toContain("event.data?.type === 'changes.state'");
    expect(html).toContain("type: 'run.prompt'");
    expect(html).toContain('id="provider-select"');
    expect(html).toContain('providerSelect.disabled = !!activeRun');
    expect(html).not.toContain('Start a session');
    expect(html).not.toContain('start-provider-select');
    expect(html).toContain('id="model-select"');
    expect(html).toContain('const effectiveModel = provider?.models?.includes(selectedModel)');
    expect(html).toContain("type: 'model.select'");
    expect(html).toContain("session.agent + ' · ' + session.status");
    expect(html).toContain("(provider?.label || 'Agent') + 'に指示する…'");
    expect(html).toContain('id="approval-card"');
    expect(html).toContain('data-approval-decision="allow_permanent"');
    expect(html).toContain("type: 'approval.decide'");
    expect(html).not.toContain('LATEST AGENT RESULT');
  });

  it('shows provider-specific usage fields with one shared cost', () => {
    const html = renderWebviewHtml({ cspSource: 'vscode-webview://unit-test', nonce: 'n', styleUri: 'style' });
    expect(html).toContain("const isCopilot = provider === 'copilot';");
    expect(html).toContain('data-copilot-usage hidden>AI credits');
    expect(html.match(/id="usage-cost"/g)).toHaveLength(1);
    expect(html).not.toContain('usage-cost-credits');
    expect(html).not.toContain('data-credit-usage');
    expect(html).toContain('renderUsage(event.data.usage.summary, event.data.selectedProvider)');
  });

  it('shows the provider usage percentage rather than the remaining percentage', () => {
    const html = renderWebviewHtml({ cspSource: 'vscode-webview://unit-test', nonce: 'n', styleUri: 'style' });
    expect(html).toContain("window.name + ' ' + window.usedPercent + '%'");
    expect(html).not.toContain("window.name + ' ' + window.remainingPercent + '%'");
  });

  it('keeps the generated webview script syntactically valid', () => {
    const html = renderWebviewHtml({ cspSource: 'vscode-webview://unit-test', nonce: 'n', styleUri: 'style' });
    const script = html.match(/<script nonce="n">([\s\S]*)<\/script>/)?.[1];
    expect(script).toBeDefined();
    expect(() => new Function(script!)).not.toThrow();
  });

  it('wires up an @-mention file picker for the prompt textarea', () => {
    const html = renderWebviewHtml({ cspSource: 'vscode-webview://unit-test', nonce: 'n', styleUri: 'style' });
    expect(html).toContain('id="mention-list"');
    expect(html).toContain('class="prompt-input-wrap"');
    expect(html).toContain("const at = uptoCaret.lastIndexOf('@');");
    expect(html).toContain("type: 'workspace.searchFiles'");
    expect(html).toContain("event.data?.type === 'workspace.files'");
    expect(html).toContain("value.slice(0, start) + insertion + value.slice(end)");
    expect(html).toContain("event.key === 'ArrowDown'");
    expect(html).toContain("event.key === 'Escape'");
  });
});
