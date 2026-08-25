import { describe, expect, it } from 'vitest';
import { renderMarkdown } from './index.js';

describe('renderMarkdown', () => {
  it('renders ordinary tables without requiring alignment markers', () => {
    const html = renderMarkdown('| Name | Count |\n| --- | --- |\n| API | 3 |');
    expect(html).toContain('<div class="markdown-table-wrap"><table>');
    expect(html).toContain('<th>Name</th>');
    expect(html).toContain('<td>3</td>');
  });

  it('renders table alignment and inline Markdown in cells', () => {
    const html = renderMarkdown('| Name | Count | Status |\n| :--- | ---: | :---: |\n| **API** | 3 | `ok` |');
    expect(html).toContain('<th style="text-align:left">Name</th>');
    expect(html).toContain('<th style="text-align:right">Count</th>');
    expect(html).toContain('<th style="text-align:center">Status</th>');
    expect(html).toContain('<strong>API</strong>');
    expect(html).toContain('<code>ok</code>');
  });

  it('renders commonly used block and inline syntax', () => {
    const html = renderMarkdown([
      'Heading',
      '=======',
      '',
      '> first',
      '> second',
      '',
      '1. parent',
      '   - child',
      '',
      '~~old~~ and [reference][docs]',
      '',
      '[docs]: https://example.com/docs',
    ].join('\n'));
    expect(html).toContain('<h1>Heading</h1>');
    expect(html).toContain('<blockquote>');
    expect(html).toContain('<ol>');
    expect(html).toContain('<ul>');
    expect(html).toContain('<del>old</del>');
    expect(html).toContain('href="https://example.com/docs"');
  });

  it('renders task lists, fenced code, images, and autolinks', () => {
    const html = renderMarkdown('- [x] done\n- [ ] pending\n\n~~~ts\nconst n = 1;\n~~~\n\n![diagram](https://example.com/a.png)\n\nhttps://example.com');
    expect(html).toContain('<input type="checkbox" disabled checked>');
    expect(html).toContain('<input type="checkbox" disabled>');
    expect(html).toContain('<code class="language-ts">');
    expect(html).toContain('loading="lazy"');
    expect(html).toContain('<a href="https://example.com"');
  });

  it('escapes raw HTML and rejects unsafe links', () => {
    const html = renderMarkdown('<script>alert(1)</script>\n\n[unsafe](javascript:alert(1))\n\n```md\n[example](javascript:kept-as-code)\n```');
    expect(html).toContain('&lt;script&gt;alert(1)&lt;/script&gt;');
    expect(html).not.toContain('<script>');
    expect(html).not.toContain('href="javascript:');
    expect(html).toContain('[example](javascript:kept-as-code)');
  });
});
