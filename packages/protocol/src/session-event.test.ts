import { readFileSync } from 'node:fs';

import Ajv2020 from 'ajv/dist/2020.js';
import addFormats from 'ajv-formats';
import { describe, expect, it } from 'vitest';

import type { SessionEvent } from './index.js';

const schema = JSON.parse(
  readFileSync(new URL('../schema/session-event.schema.json', import.meta.url), 'utf8'),
);
const fixture = JSON.parse(
  readFileSync(new URL('../fixtures/session-event.json', import.meta.url), 'utf8'),
) as SessionEvent;

describe('SessionEvent contract', () => {
  it('accepts the shared fixture', () => {
    const ajv = new Ajv2020({ strict: true });
    addFormats(ajv);
    const validate = ajv.compile(schema);

    expect(validate(fixture), JSON.stringify(validate.errors)).toBe(true);
  });

  it('rejects an unsupported schema version', () => {
    const ajv = new Ajv2020({ strict: true });
    addFormats(ajv);
    const validate = ajv.compile(schema);

    expect(validate({ ...fixture, schemaVersion: 2 })).toBe(false);
  });
});
