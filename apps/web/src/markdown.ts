function escapeHtml(value: string): string {
  return value.replace(/[&<>"']/g, (character) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  })[character] ?? character);
}

function safeUrl(value: string): string | undefined {
  const url = value.trim();
  if (/^(https?:|mailto:)/i.test(url)) return url;
  return undefined;
}

function inlineMarkdown(value: string): string {
  let output = escapeHtml(value);
  output = output.replace(/^\[([ xX])\]\s+/, (_match, checked: string) => (
    `<input type="checkbox" disabled${checked.toLowerCase() === 'x' ? ' checked' : ''}> `
  ));
  output = output.replace(/`([^`\n]+)`/g, '<code>$1</code>');
  output = output.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>');
  output = output.replace(/__([^_\n]+)__/g, '<strong>$1</strong>');
  output = output.replace(/\*([^*\n]+)\*/g, '<em>$1</em>');
  output = output.replace(/_([^_\n]+)_/g, '<em>$1</em>');
  output = output.replace(/~~([^~\n]+)~~/g, '<del>$1</del>');
  output = output.replace(/!\[([^\]\n]*)\]\(([^)\n]+)\)/g, (_match, alt: string, url: string) => {
    const safe = safeUrl(url);
    return safe ? `<img src="${escapeHtml(safe)}" alt="${alt}" loading="lazy">` : alt;
  });
  output = output.replace(/\[([^\]\n]+)\]\(([^)\n]+)\)/g, (_match, label: string, url: string) => {
    const safe = safeUrl(url);
    return safe ? `<a href="${escapeHtml(safe)}" target="_blank" rel="noopener noreferrer">${label}</a>` : label;
  });
  return output;
}

function splitTableCells(line: string): string[] {
  const trimmed = line.trim().replace(/^\|/, '').replace(/\|$/, '');
  return trimmed.split(/(?<!\\)\|/).map((cell) => cell.replace(/\\\|/g, '|').trim());
}

function tableAlignment(cell: string): '' | 'left' | 'center' | 'right' {
  const value = cell.trim();
  if (!/^:?-{3,}:?$/.test(value)) return '';
  if (value.startsWith(':') && value.endsWith(':')) return 'center';
  if (value.startsWith(':')) return 'left';
  if (value.endsWith(':')) return 'right';
  return '';
}

function isTableSeparator(line: string): boolean {
  const cells = splitTableCells(line);
  return cells.length > 0 && cells.every((cell) => tableAlignment(cell) !== '');
}

function renderTable(lines: string[]): string {
  const header = splitTableCells(lines[0] ?? '');
  const alignments = splitTableCells(lines[1] ?? '').map(tableAlignment);
  const body = lines.slice(2).map(splitTableCells);
  const cell = (tag: 'th' | 'td', value: string, index: number) => {
    const align = alignments[index] ?? '';
    return `<${tag}${align ? ` style="text-align:${align}"` : ''}>${inlineMarkdown(value)}</${tag}>`;
  };
  return `<div class="markdown-table-wrap"><table><thead><tr>${header.map((value, index) => cell('th', value, index)).join('')}</tr></thead><tbody>${body
    .map((row) => `<tr>${header.map((_value, index) => cell('td', row[index] ?? '', index)).join('')}</tr>`).join('')}</tbody></table></div>`;
}

export function renderMarkdown(markdown: string): string {
  const lines = markdown.replaceAll('\r\n', '\n').split('\n');
  const output: string[] = [];
  let inCode = false;
  let codeLanguage = '';
  let codeLines: string[] = [];
  let paragraph: string[] = [];
  let listType: 'ul' | 'ol' | undefined;

  const closeList = () => {
    if (listType) output.push(`</${listType}>`);
    listType = undefined;
  };
  const closeParagraph = () => {
    if (paragraph.length) output.push(`<p>${paragraph.map(inlineMarkdown).join('<br>')}</p>`);
    paragraph = [];
  };
  const closeCode = () => {
    output.push(`<pre><code${codeLanguage ? ` class="language-${escapeHtml(codeLanguage)}"` : ''}>${escapeHtml(codeLines.join('\n'))}</code></pre>`);
    codeLines = [];
    codeLanguage = '';
  };

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index] ?? '';
    const fence = line.match(/^\s*```\s*([\w-]*)\s*$/);
    if (fence) {
      if (inCode) closeCode();
      else codeLanguage = fence[1] ?? '';
      inCode = !inCode;
      continue;
    }
    if (inCode) { codeLines.push(line); continue; }
    if (!line.trim()) { closeParagraph(); closeList(); continue; }

    if (index + 1 < lines.length && line.includes('|') && isTableSeparator(lines[index + 1] ?? '')) {
      closeParagraph(); closeList();
      const tableLines = [line, lines[index + 1] ?? ''];
      index += 2;
      while (index < lines.length && (lines[index] ?? '').includes('|') && (lines[index] ?? '').trim()) {
        tableLines.push(lines[index] ?? '');
        index += 1;
      }
      index -= 1;
      output.push(renderTable(tableLines));
      continue;
    }

    if (/^\s{0,3}([-*_])(?:\s*\1){2,}\s*$/.test(line)) {
      closeParagraph(); closeList(); output.push('<hr>'); continue;
    }

    const heading = line.match(/^(#{1,6})\s+(.+)$/);
    if (heading) {
      const hashes = heading[1] ?? '';
      const text = heading[2] ?? '';
      closeParagraph(); closeList(); output.push(`<h${hashes.length}>${inlineMarkdown(text)}</h${hashes.length}>`); continue;
    }
    const quote = line.match(/^>\s?(.*)$/);
    if (quote) { closeParagraph(); closeList(); output.push(`<blockquote>${inlineMarkdown(quote[1] ?? '')}</blockquote>`); continue; }
    const list = line.match(/^\s*([-*+]\s+|\d+[.)]\s+)(.+)$/);
    if (list) {
      closeParagraph();
      const marker = list[1] ?? '';
      const nextType = /^\d/.test(marker) ? 'ol' : 'ul';
      if (listType !== nextType) { closeList(); listType = nextType; output.push(`<${listType}>`); }
      output.push(`<li>${inlineMarkdown(list[2] ?? '')}</li>`);
      continue;
    }
    closeList();
    paragraph.push(line);
  }
  if (inCode) closeCode();
  closeParagraph();
  closeList();
  return output.join('');
}
