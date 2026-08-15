import { readFileSync } from 'node:fs';

import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import { describe, expect, it } from 'vitest';

const contracts = [
  'agent-session',
  'agent-run',
  'token-usage',
  'session-event',
  'change-set',
  'api-error',
  'session-list',
  'event-list',
  'ws-ticket',
  'create-session-request',
  'send-message-request',
] as const;

function readJSON(path: string): Record<string, unknown> {
  return JSON.parse(
    readFileSync(new URL(path, import.meta.url), 'utf8'),
  ) as Record<string, unknown>;
}

describe('protocol contracts', () => {
  it.each(contracts)('accepts the %s fixture', (contract) => {
    const ajv = new Ajv2020({ strict: true });
    addFormats(ajv);
    const schema = readJSON(`../schema/${contract}.schema.json`);
    const fixture = readJSON(`../fixtures/${contract}.json`);
    if (contract === 'session-list') {
      ajv.addSchema(readJSON('../schema/agent-session.schema.json'));
    }
    if (contract === 'event-list') {
      ajv.addSchema(readJSON('../schema/session-event.schema.json'));
    }
    const validate = ajv.compile(schema);

    expect(validate(fixture), JSON.stringify(validate.errors)).toBe(true);
  });

  it('rejects an unsupported schema version', () => {
    const ajv = new Ajv2020({ strict: true });
    addFormats(ajv);
    const schema = readJSON('../schema/session-event.schema.json');
    const fixture = readJSON('../fixtures/session-event.json');
    const validate = ajv.compile(schema);

    expect(validate({ ...fixture, schemaVersion: 3 })).toBe(false);
  });
});
