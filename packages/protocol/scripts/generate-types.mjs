import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { compileFromFile } from 'json-schema-to-typescript';

const packageRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
const schemaRoot = join(packageRoot, 'schema');
const outputRoot = join(packageRoot, 'src', 'generated');
const schemas = [
  'agent-run',
  'agent-session',
  'api-error',
  'change-set',
  'command-approval',
  'create-session-request',
  'event-list',
  'provider-list',
  'send-message-request',
  'session-event',
  'session-list',
  'token-usage',
  'usage-model-list',
  'usage-provider-list',
  'usage-summary',
  'ws-ticket',
];
const checkOnly = process.argv.includes('--check');
const bannerComment = `/* eslint-disable */
/**
 * Generated from packages/protocol/schema. Do not edit by hand.
 * Run \`corepack pnpm --filter @maatgen/protocol generate\` after changing a schema.
 */`;

await mkdir(outputRoot, { recursive: true });
const stale = [];

for (const name of schemas) {
  const input = join(schemaRoot, `${name}.schema.json`);
  const output = join(outputRoot, `${name}.ts`);
  const generated = await compileFromFile(input, {
    cwd: schemaRoot,
    bannerComment,
    enableConstEnums: false,
    style: { singleQuote: true, semi: true, trailingComma: 'all' },
    unknownAny: true,
    unreachableDefinitions: true,
  });
  if (checkOnly) {
    const current = await readFile(output, 'utf8').catch(() => '');
    if (current !== generated) stale.push(name);
  } else {
    await writeFile(output, generated, 'utf8');
  }
}

if (stale.length > 0) {
  throw new Error(`Generated protocol types are stale: ${stale.join(', ')}`);
}
