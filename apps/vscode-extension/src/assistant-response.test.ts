import { describe, expect, it } from 'vitest';
import type { SessionEvent } from './agent-manager-client.js';
import { selectAssistantResponse } from './assistant-response.js';

const event = (overrides: Partial<SessionEvent> & Pick<SessionEvent, 'id' | 'sequence'>): SessionEvent => ({
  sessionId: 'session-1',
  runId: 'run-1',
  timestamp: '2026-08-22T12:34:56Z',
  source: 'codex',
  type: 'assistant_message',
  data: { text: 'Answer' },
  ...overrides,
});

describe('selectAssistantResponse', () => {
  it('returns the selected assistant message', () => {
    const response = selectAssistantResponse([event({ id: 'event-1', sequence: 1 })], 'event-1');
    expect(response?.markdown).toBe('Answer');
    expect(response?.runId).toBe('run-1');
  });

  it('joins text blocks from the same provider item in sequence order', () => {
    const events = [
      event({ id: 'event-2', sequence: 2, data: { itemId: 'item-1', text: 'Second block' } }),
      event({ id: 'event-1', sequence: 1, data: { itemId: 'item-1', text: 'First block' } }),
      event({ id: 'event-3', sequence: 3, data: { itemId: 'item-2', text: 'Another answer' } }),
    ];
    expect(selectAssistantResponse(events, 'event-2')?.markdown).toBe('First block\n\nSecond block');
  });

  it('does not combine matching item IDs from another run', () => {
    const events = [
      event({ id: 'event-1', sequence: 1, data: { itemId: 'item-1', text: 'Selected' } }),
      event({ id: 'event-2', sequence: 2, runId: 'run-2', data: { itemId: 'item-1', text: 'Other run' } }),
    ];
    expect(selectAssistantResponse(events, 'event-1')?.markdown).toBe('Selected');
  });

  it('rejects non-assistant and empty events', () => {
    expect(selectAssistantResponse([event({ id: 'event-1', sequence: 1, type: 'error' })], 'event-1')).toBeUndefined();
    expect(selectAssistantResponse([event({ id: 'event-2', sequence: 2, data: { text: '  ' } })], 'event-2')).toBeUndefined();
  });
});
