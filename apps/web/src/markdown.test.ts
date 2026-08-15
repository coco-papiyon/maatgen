import { describe, expect, it } from 'vitest';
import { renderMarkdown } from './markdown';

describe('renderMarkdown', () => {
  it('renders common Markdown blocks and escapes HTML', () => {
    const html = renderMarkdown('# Result\n\n- **done**\n- `go test`\n\n```ts\nconst value = 1;\n```\n\n<script>alert(1)</script>');
    expect(html).toContain('<h1>Result</h1>');
    expect(html).toContain('<strong>done</strong>');
    expect(html).toContain('<pre><code class="language-ts">const value = 1;</code></pre>');
    expect(html).toContain('&lt;script&gt;alert(1)&lt;/script&gt;');
    expect(html).not.toContain('<script>');
  });

  it('only emits safe external links', () => {
    const html = renderMarkdown('[safe](https://example.com) [unsafe](javascript:alert(1))');
    expect(html).toContain('href="https://example.com"');
    expect(html).not.toContain('javascript:');
  });

  it('renders tables with alignment and escaped cell content', () => {
    const html = renderMarkdown('| Name | Count | Status |\n| :--- | ---: | :---: |\n| **API** | 3 | <ok> |\n| UI | 1 | done |');
    expect(html).toContain('<table>');
    expect(html).toContain('<th style="text-align:left">Name</th>');
    expect(html).toContain('<th style="text-align:right">Count</th>');
    expect(html).toContain('<th style="text-align:center">Status</th>');
    expect(html).toContain('&lt;ok&gt;');
  });

  it('renders task lists, strikethrough, and horizontal rules', () => {
    const html = renderMarkdown('- [x] finished\n- [ ] pending\n\n~~old~~\n\n---');
    expect(html).toContain('<input type="checkbox" disabled checked>');
    expect(html).toContain('<input type="checkbox" disabled>');
    expect(html).toContain('<del>old</del>');
    expect(html).toContain('<hr>');
  });

  it('renders safe images and rejects unsafe image URLs', () => {
    const html = renderMarkdown('![diagram](https://example.com/diagram.png) ![bad](javascript:alert(1))');
    expect(html).toContain('<img src="https://example.com/diagram.png" alt="diagram" loading="lazy">');
    expect(html).not.toContain('javascript:');
  });
});
