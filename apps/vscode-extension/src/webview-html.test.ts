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
    expect(html).toContain('session-history-list');
    expect(html).toContain("type: 'session.select'");
    expect(html).toContain('changes-list');
    expect(html).toContain("type: 'run.prompt'");
  });
});
