import { describe, expect, it, vi } from 'vitest';
import type { SessionEvent } from '@maatgen/protocol';
import type { AgentApi } from './api';
import { SessionEventStream, webSocketURL } from './event-stream';

class FakeSocket {
  onopen: WebSocket['onopen'] = null;
  onmessage: WebSocket['onmessage'] = null;
  onerror: WebSocket['onerror'] = null;
  onclose: WebSocket['onclose'] = null;
  close = vi.fn();

  open() {
    this.onopen?.call(this as unknown as WebSocket, {} as Event);
  }

  message(event: SessionEvent) {
    this.onmessage?.call(this as unknown as WebSocket, { data: JSON.stringify(event) } as MessageEvent);
  }

  disconnect() {
    this.onclose?.call(this as unknown as WebSocket, {} as CloseEvent);
  }
}

function sessionEvent(sequence: number, sessionId = 'session 1'): SessionEvent {
  return {
    id: `event-${sequence}`,
    sessionId,
    sequence,
    timestamp: '2026-01-01T00:00:00Z',
    schemaVersion: 1,
    source: 'manager',
    type: 'run_started',
    data: {},
  };
}

describe('SessionEventStream', () => {
  it('reconnects from the last sequence and ignores duplicate events', async () => {
    const tickets = vi.fn()
      .mockResolvedValueOnce({ ticket: 'first', expiresAt: '' })
      .mockResolvedValueOnce({ ticket: 'second', expiresAt: '' });
    const sockets: FakeSocket[] = [];
    const protocols: string[][] = [];
    const urls: string[] = [];
    const scheduled: Array<{ callback: () => void; delay: number }> = [];
    const received: SessionEvent[] = [];
    const states: string[] = [];
    const stream = new SessionEventStream({
      api: { issueWebSocketTicket: tickets } as unknown as AgentApi,
      sessionId: 'session 1',
      afterSequence: 2,
      onEvent: (event) => received.push(event),
      onState: (state) => states.push(state),
      location: { href: 'http://localhost:5173/' },
      socketFactory: (url, selectedProtocols) => {
        const socket = new FakeSocket();
        sockets.push(socket);
        urls.push(url);
        protocols.push(selectedProtocols);
        return socket as unknown as WebSocket;
      },
      schedule: (callback, delay) => {
        scheduled.push({ callback, delay });
        return scheduled.length;
      },
      cancelSchedule: vi.fn(),
    });

    stream.start();
    await vi.waitFor(() => expect(sockets).toHaveLength(1));
    expect(protocols[0]).toEqual(['maatgen.v1', 'ticket.first']);
    expect(new URL(urls[0]!).searchParams.get('afterSequence')).toBe('2');
    sockets[0]!.open();
    sockets[0]!.message(sessionEvent(3));
    sockets[0]!.message(sessionEvent(3));
    sockets[0]!.message(sessionEvent(4, 'another-session'));
    expect(received.map((event) => event.sequence)).toEqual([3]);

    sockets[0]!.disconnect();
    expect(scheduled[0]!.delay).toBe(500);
    scheduled[0]!.callback();
    await vi.waitFor(() => expect(sockets).toHaveLength(2));
    expect(protocols[1]).toEqual(['maatgen.v1', 'ticket.second']);
    expect(new URL(urls[1]!).searchParams.get('afterSequence')).toBe('3');
    expect(states).toEqual(['connecting', 'connected', 'reconnecting']);

    stream.stop();
    expect(sockets[1]!.close).toHaveBeenCalledWith(1000, 'session changed');
    expect(states.at(-1)).toBe('disconnected');
  });

  it('backs off repeated ticket failures up to ten seconds', async () => {
    const scheduled: Array<{ callback: () => void; delay: number }> = [];
    const stream = new SessionEventStream({
      api: { issueWebSocketTicket: vi.fn().mockRejectedValue(new Error('manager unavailable')) } as unknown as AgentApi,
      sessionId: 'session-1',
      afterSequence: 0,
      onEvent: vi.fn(),
      onState: vi.fn(),
      onError: vi.fn(),
      location: { href: 'http://localhost:5173/' },
      schedule: (callback, delay) => {
        scheduled.push({ callback, delay });
        return scheduled.length;
      },
      cancelSchedule: vi.fn(),
    });

    stream.start();
    await vi.waitFor(() => expect(scheduled).toHaveLength(1));
    for (let index = 0; index < 6; index += 1) {
      scheduled[index]!.callback();
      await vi.waitFor(() => expect(scheduled).toHaveLength(index + 2));
    }
    expect(scheduled.map(({ delay }) => delay)).toEqual([500, 1000, 2000, 4000, 8000, 10_000, 10_000]);
    stream.stop();
  });
});

describe('webSocketURL', () => {
  it('uses wss for HTTPS and encodes the session id', () => {
    const url = new URL(webSocketURL('session / 1', 42, { href: 'https://example.test/app' }));
    expect(url.protocol).toBe('wss:');
    expect(url.pathname).toBe('/ws');
    expect(url.searchParams.get('sessionId')).toBe('session / 1');
    expect(url.searchParams.get('afterSequence')).toBe('42');
  });
});
