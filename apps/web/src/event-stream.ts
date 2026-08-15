import type { SessionEvent } from '@maatgen/protocol';
import type { AgentApi } from './api';

export type EventStreamState = 'connecting' | 'connected' | 'reconnecting' | 'disconnected';

export interface EventStreamOptions {
  api: AgentApi;
  sessionId: string;
  afterSequence: number;
  onEvent: (event: SessionEvent) => void;
  onState: (state: EventStreamState) => void;
  onError?: (error: Error) => void;
  location?: Pick<Location, 'href'>;
  socketFactory?: (url: string, protocols: string[]) => WebSocket;
  schedule?: (callback: () => void, delay: number) => number;
  cancelSchedule?: (timer: number) => void;
}

export interface EventStreamLike {
  start(): void;
  stop(): void;
}

export type EventStreamFactory = (options: EventStreamOptions) => EventStreamLike;

const initialReconnectDelay = 500;
const maximumReconnectDelay = 10_000;

export class SessionEventStream {
  private readonly socketFactory: NonNullable<EventStreamOptions['socketFactory']>;
  private readonly schedule: NonNullable<EventStreamOptions['schedule']>;
  private readonly cancelSchedule: NonNullable<EventStreamOptions['cancelSchedule']>;
  private socket: WebSocket | undefined;
  private reconnectTimer: number | undefined;
  private reconnectAttempt = 0;
  private generation = 0;
  private stopped = true;
  private cursor: number;

  constructor(private readonly options: EventStreamOptions) {
    this.cursor = options.afterSequence;
    this.socketFactory = options.socketFactory ?? ((url, protocols) => new WebSocket(url, protocols));
    this.schedule = options.schedule ?? ((callback, delay) => window.setTimeout(callback, delay));
    this.cancelSchedule = options.cancelSchedule ?? ((timer) => window.clearTimeout(timer));
  }

  start() {
    if (!this.stopped) return;
    this.stopped = false;
    this.reconnectAttempt = 0;
    this.options.onState('connecting');
    void this.connect(this.generation);
  }

  stop() {
    if (this.stopped) return;
    this.stopped = true;
    this.generation += 1;
    if (this.reconnectTimer !== undefined) this.cancelSchedule(this.reconnectTimer);
    this.reconnectTimer = undefined;
    this.socket?.close(1000, 'session changed');
    this.socket = undefined;
    this.options.onState('disconnected');
  }

  private async connect(generation: number) {
    try {
      const { ticket } = await this.options.api.issueWebSocketTicket();
      if (this.stopped || generation !== this.generation) return;

      const socket = this.socketFactory(webSocketURL(this.options.sessionId, this.cursor, this.options.location), [
        'maatgen.v1',
        `ticket.${ticket}`,
      ]);
      this.socket = socket;
      socket.onopen = () => {
        if (socket !== this.socket || this.stopped) return;
        this.options.onState('connected');
      };
      socket.onmessage = (message) => {
        if (socket !== this.socket || this.stopped) return;
        try {
          const event = JSON.parse(String(message.data)) as SessionEvent;
          if (!isEventAfter(event, this.options.sessionId, this.cursor)) return;
          this.cursor = event.sequence;
          this.reconnectAttempt = 0;
          this.options.onEvent(event);
        } catch (cause) {
          this.options.onError?.(asError(cause));
        }
      };
      socket.onerror = () => socket.close();
      socket.onclose = () => {
        if (socket !== this.socket || this.stopped) return;
        this.socket = undefined;
        this.queueReconnect(generation);
      };
    } catch (cause) {
      if (this.stopped || generation !== this.generation) return;
      this.options.onError?.(asError(cause));
      this.queueReconnect(generation);
    }
  }

  private queueReconnect(generation: number) {
    const delay = Math.min(initialReconnectDelay * 2 ** this.reconnectAttempt, maximumReconnectDelay);
    this.reconnectAttempt += 1;
    this.options.onState('reconnecting');
    this.reconnectTimer = this.schedule(() => {
      this.reconnectTimer = undefined;
      if (!this.stopped && generation === this.generation) void this.connect(generation);
    }, delay);
  }
}

export function webSocketURL(sessionId: string, afterSequence: number, location?: Pick<Location, 'href'>): string {
  const source = location ?? window.location;
  const url = new URL('/ws', source.href);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  url.searchParams.set('sessionId', sessionId);
  url.searchParams.set('afterSequence', String(afterSequence));
  return url.toString();
}

function isEventAfter(event: SessionEvent, sessionId: string, cursor: number): boolean {
  return event.sessionId === sessionId
    && Number.isSafeInteger(event.sequence)
    && event.sequence > cursor;
}

function asError(cause: unknown): Error {
  return cause instanceof Error ? cause : new Error(String(cause));
}
