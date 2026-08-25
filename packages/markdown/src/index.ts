import MarkdownIt from 'markdown-it';

const markdown = new MarkdownIt({
  breaks: true,
  html: false,
  linkify: true,
  typographer: false,
});

markdown.renderer.rules.link_open = (tokens, index, options, _environment, renderer) => {
  const token = tokens[index];
  if (!token) return '';
  token.attrSet('target', '_blank');
  token.attrSet('rel', 'noopener noreferrer');
  return renderer.renderToken(tokens, index, options);
};

const defaultImageRenderer = markdown.renderer.rules.image
  ?? ((tokens, index, options, environment, renderer) => renderer.renderToken(tokens, index, options));
markdown.renderer.rules.image = (tokens, index, options, environment, renderer) => {
  tokens[index]?.attrSet('loading', 'lazy');
  return defaultImageRenderer(tokens, index, options, environment, renderer);
};

markdown.renderer.rules.table_open = () => '<div class="markdown-table-wrap"><table>\n';
markdown.renderer.rules.table_close = () => '</table></div>\n';
markdown.renderer.rules.s_open = () => '<del>';
markdown.renderer.rules.s_close = () => '</del>';

function renderTaskListItems(html: string): string {
  return html.replace(
    /(<li>\s*(?:<p>)?)\[([ xX])\]\s+/g,
    (_match, prefix: string, checked: string) => `${prefix}<input type="checkbox" disabled${checked.toLowerCase() === 'x' ? ' checked' : ''}> `,
  );
}

/** Render untrusted CommonMark/GFM-style Markdown as safe HTML. */
export function renderMarkdown(source: string): string {
  return renderTaskListItems(markdown.render(source));
}
