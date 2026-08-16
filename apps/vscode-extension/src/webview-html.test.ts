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
    expect(html).toContain('<details class="session-history" open>');
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
    expect(html).toContain("type: 'run.prompt'");
    expect(html).toContain('id="provider-select"');
    expect(html).toContain('id="model-select"');
    expect(html).toContain("type: 'model.select'");
    expect(html).toContain("session.agent + ' · ' + session.status");
    expect(html).toContain("(provider?.label || 'Agent') + 'に指示する…'");
    expect(html).toContain('id="approval-card"');
    expect(html).toContain('data-approval-decision="allow_permanent"');
    expect(html).toContain("type: 'approval.decide'");
  });
});
