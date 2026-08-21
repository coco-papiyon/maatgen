import type { SessionEvent } from './agent-manager-client.js';

export interface AssistantResponse {
  eventId: string;
  sessionId: string;
  runId?: string;
  timestamp: string;
  markdown: string;
}

export function selectAssistantResponse(events: SessionEvent[], eventId: string): AssistantResponse | undefined {
  const target = events.find((event) => event.id === eventId && event.type === 'assistant_message');
  if (!target) return undefined;

  const targetText = eventText(target);
  if (!targetText) return undefined;

  const itemId = typeof target.data.itemId === 'string' ? target.data.itemId : '';
  const blocks = itemId
    ? events.filter((event) => (
      event.type === 'assistant_message'
      && event.sessionId === target.sessionId
      && event.runId === target.runId
      && event.data.itemId === itemId
    ))
    : [target];
  const markdown = blocks
    .sort((left, right) => left.sequence - right.sequence)
    .map(eventText)
    .filter(Boolean)
    .join('\n\n');
  if (!markdown) return undefined;

  return {
    eventId: target.id,
    sessionId: target.sessionId,
    ...(target.runId ? { runId: target.runId } : {}),
    timestamp: target.timestamp,
    markdown,
  };
}

function eventText(event: SessionEvent): string {
  if (typeof event.data.text !== 'string' || !event.data.text.trim()) return '';
  return event.data.text;
}
