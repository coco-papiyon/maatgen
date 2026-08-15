import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import App from './App.vue';
import { AgentApiError } from './api';
import type { EventStreamFactory } from './event-stream';
import { createMockEnvironment, MockAgentApi } from './testing/mock-agent-api';
import type { SessionEvent } from '@maatgen/protocol';

let wrapper: VueWrapper | undefined;

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  localStorage.removeItem('maatgen.showSystemMessages');
  localStorage.removeItem('maatgen.provider');
  localStorage.removeItem('maatgen.sidePanel');
  vi.restoreAllMocks();
});

async function mountApp() {
  const environment = createMockEnvironment();
  wrapper = mount(App, { props: environment });
  await flushPromises();
  return { wrapper, environment };
}

const passiveEventStream: EventStreamFactory = (options) => ({
  start: () => options.onState('connected'),
  stop: () => options.onState('disconnected'),
});

describe('App with MockAgentApi', () => {
  it('uses the manager launch directory as the initial repository path', async () => {
    const mounted = await mountApp();
    expect((mounted.wrapper.find('#workspace').element as HTMLInputElement).value)
      .toBe('C:/demo/current-repository');
  });

  it('renders session history, timeline events, changes, and live state', async () => {
    const mounted = await mountApp();
    expect(mounted.wrapper.find('.error-banner').exists() ? mounted.wrapper.find('.error-banner').text() : '').toBe('');
    expect(mounted.wrapper.findAll('.session-item')).toHaveLength(5);
    expect(mounted.wrapper.text()).toContain('認証処理を確認し、テストを追加しました。');
    expect(mounted.wrapper.find('.stream-state').text()).toBe('Live');
    expect(mounted.wrapper.find('.change-title').text()).toContain('src/auth.ts');
  });

  it('switches the side panel between Usage and Changes tabs', async () => {
    const mounted = await mountApp();
    expect(mounted.wrapper.find('#changes-panel').exists()).toBe(true);
    expect(mounted.wrapper.find('#usage-panel').exists()).toBe(false);
    await mounted.wrapper.find('#usage-tab').trigger('click');
    expect(mounted.wrapper.find('#usage-panel').exists()).toBe(true);
    expect(mounted.wrapper.find('#changes-panel').exists()).toBe(false);
    expect(mounted.wrapper.find('#usage-tab').attributes('aria-selected')).toBe('true');
  });

  it('shows Copilot AI credits instead of token metrics', async () => {
    const mounted = await mountApp();
    await mounted.wrapper.findAll('.session-item')[2]!.trigger('click');
    await flushPromises();
    await mounted.wrapper.find('#usage-tab').trigger('click');
    const panel = mounted.wrapper.find('#usage-panel').text();
    // Summary should show AI credits and cost, but not aggregated token metrics or a single "Actual model"
    expect(panel).toContain('AI credits');
    expect(panel).toContain('0.125');
    expect(panel).not.toContain('Input');
  });

  it('keeps the composer visible while only the conversation timeline scrolls', async () => {
    const mounted = await mountApp();
    const styles = readFileSync('src/styles.css', 'utf8');
    expect(styles).toMatch(/\.conversation\s*\{[^}]*display:\s*flex;[^}]*flex-direction:\s*column;/);
    expect(styles).toMatch(/\.timeline\s*\{[^}]*flex:\s*1 1 auto;[^}]*min-height:\s*0;[^}]*overflow:\s*auto;/);
    expect(mounted.wrapper.find('.composer').exists()).toBe(true);
  });

  it('scrolls the conversation to the newest event', async () => {
    const api = new MockAgentApi();
    let pushEvent: ((event: SessionEvent) => void) | undefined;
    wrapper = mount(App, {
      props: {
        agentApi: api,
        eventStreamFactory: (options) => {
          pushEvent = options.onEvent;
          return { start: () => options.onState('connected'), stop: () => options.onState('disconnected') };
        },
      },
    });
    await flushPromises();
    const timeline = wrapper.find('.timeline').element as HTMLElement;
    Object.defineProperty(timeline, 'scrollHeight', { configurable: true, value: 1234 });
    pushEvent!({
      id: 'latest-event', sessionId: 'mock-success', sequence: 5,
      timestamp: new Date().toISOString(), schemaVersion: 2, source: 'manager',
      type: 'assistant_message', data: { text: '追加の結果' },
    });
    await flushPromises();
    expect(timeline.scrollTop).toBe(1234);
  });

  it('hides command and file-change system messages by default and shows them when configured', async () => {
    const mounted = await mountApp();
    expect(mounted.wrapper.find('.timeline').text()).not.toContain('npm test');
    expect(mounted.wrapper.find('.timeline').text()).not.toContain('file_change_reported');

    await mounted.wrapper.find('.system-message-setting input').setValue(true);
    expect(mounted.wrapper.find('.timeline').text()).toContain('npm test');
    expect(mounted.wrapper.find('.timeline').text()).toContain('file_change_reported');
  });

  it('selects a configured model for the next run', async () => {
    const mounted = await mountApp();
    const send = vi.spyOn(mounted.environment.agentApi, 'sendMessage').mockResolvedValue({
      id: 'run-model', sessionId: 'mock-success', status: 'running', prompt: 'モデル指定のテスト',
    });
    await mounted.wrapper.find('select[aria-label="Model"]').setValue('gpt-5.6-sol');
    await mounted.wrapper.find('.composer textarea').setValue('モデル指定のテスト');
    await mounted.wrapper.find('.composer').trigger('submit');
    await flushPromises();
    expect(send).toHaveBeenCalledWith(expect.any(String), {
      message: 'モデル指定のテスト',
      model: 'gpt-5.6-sol',
    });
  });

  it('restores the previously selected provider for a new session', async () => {
    localStorage.setItem('maatgen.provider', 'other');
    class MultiProviderApi extends MockAgentApi {
      override async listProviders() {
        return {
          providers: [
            { id: 'codex' as const, label: 'Codex', models: ['gpt-5.6-sol'] },
            { id: 'other' as const, label: 'Other', models: ['other-model'] },
          ] as any,
        };
      }
    }

    wrapper = mount(App, { props: { agentApi: new MultiProviderApi(), eventStreamFactory: passiveEventStream } });
    await flushPromises();

    expect((wrapper.find('.provider-fields select').element as HTMLSelectElement).value).toBe('other');
  });

  it('keeps the composer available and continues the same session after a run completes', async () => {
    const api = new MockAgentApi();
    let pushEvent: ((event: SessionEvent) => void) | undefined;
    const eventStreamFactory: EventStreamFactory = (options) => {
      pushEvent = options.onEvent;
      return { start: () => options.onState('connected'), stop: () => options.onState('disconnected') };
    };
    wrapper = mount(App, { props: { agentApi: api, eventStreamFactory } });
    await flushPromises();
    const send = vi.spyOn(api, 'sendMessage').mockResolvedValue({
      id: 'run-first', sessionId: 'mock-success', status: 'running', prompt: '最初の指示',
    });

    await wrapper.find('.composer textarea').setValue('最初の指示');
    await wrapper.find('.composer').trigger('submit');
    await flushPromises();
    pushEvent!({
      id: 'completed-first', sessionId: 'mock-success', runId: 'run-first', sequence: 5,
      timestamp: new Date().toISOString(), schemaVersion: 2, source: 'manager', type: 'run_completed', data: {},
    });
    await flushPromises();

    expect(wrapper.find('.composer').exists()).toBe(true);
    await wrapper.find('.composer textarea').setValue('続きの指示');
    await wrapper.find('.composer').trigger('submit');
    await flushPromises();
    expect(send).toHaveBeenLastCalledWith('mock-success', { message: '続きの指示' });
    expect(send).toHaveBeenCalledTimes(2);
  });

  it('restores an in-progress run and lets the user stop it after reopening a session', async () => {
    class RunningApi extends MockAgentApi {
      override async getEvents(id: string, afterSequence = 0) {
        const existing = await super.getEvents(id, afterSequence);
        if (id !== 'mock-success' || afterSequence > 0) return existing;
        return [...existing, {
          id: 'running-event', sessionId: id, runId: 'run-restored', sequence: 5,
          timestamp: new Date().toISOString(), schemaVersion: 2 as const, source: 'manager' as const,
          type: 'run_started' as const, data: {},
        }];
      }
    }
    const api = new RunningApi();
    const cancel = vi.spyOn(api, 'cancelRun').mockResolvedValue();
    wrapper = mount(App, { props: { agentApi: api, eventStreamFactory: passiveEventStream } });
    await flushPromises();

    expect(wrapper.find('.stop-button').text()).toBe('停止');
    await wrapper.find('.stop-button').trigger('click');
    await flushPromises();
    expect(cancel).toHaveBeenCalledWith('run-restored');
  });

  it('opens a multi-hunk diff and restores one hunk', async () => {
    const mounted = await mountApp();
    const session = mounted.wrapper.findAll('.session-item').find((item) => item.text().includes('multi-hunk'));
    expect(session).toBeDefined();
    await session!.trigger('click');
    await flushPromises();

    await mounted.wrapper.find('.change-card').trigger('click');
    expect(mounted.wrapper.findAll('.diff-hunk')).toHaveLength(2);
    expect(mounted.wrapper.findAll('.restore-badge').map((badge) => badge.text())).toEqual(['changed', 'changed']);

    await mounted.wrapper.find('.restore-button').trigger('click');
    await flushPromises();
    expect(mounted.wrapper.findAll('.restore-badge').map((badge) => badge.text())).toEqual(['restored', 'changed']);
  });

  it('provides failure and cancellation scenarios', async () => {
    const api = new MockAgentApi();
    const { sessions } = await api.listSessions();
    const failure = sessions.find((session) => session.id === 'mock-failure');
    const cancelled = sessions.find((session) => session.id === 'mock-cancelled');
    expect(failure && (await api.getEvents(failure.id)).map((event) => event.type)).toContain('run_failed');
    expect(cancelled && (await api.getEvents(cancelled.id)).map((event) => event.type)).toContain('run_cancelled');
    expect((await api.getChanges('mock-multi-hunk')).files[0]?.hunks).toHaveLength(2);
  });

  it('loads additional session history pages', async () => {
    class PagedMockApi extends MockAgentApi {
      override listSessions(cursor?: string) {
        return super.listSessions(cursor, 2);
      }
    }
    const api = new PagedMockApi();
    wrapper = mount(App, { props: { agentApi: api, eventStreamFactory: passiveEventStream } });
    await flushPromises();
    expect(wrapper.findAll('.session-item')).toHaveLength(2);
    await wrapper.find('.load-more').trigger('click');
    await flushPromises();
    expect(wrapper.findAll('.session-item')).toHaveLength(4);
    expect(wrapper.find('.load-more').exists()).toBe(true);
    await wrapper.find('.load-more').trigger('click');
    await flushPromises();
    expect(wrapper.findAll('.session-item')).toHaveLength(5);
    expect(wrapper.find('.load-more').exists()).toBe(false);
  });

  it('shows actionable Manager and authentication diagnostics', async () => {
    class OfflineApi extends MockAgentApi {
      override async listSessions(): Promise<never> {
        throw new TypeError('Failed to fetch');
      }
    }
    wrapper = mount(App, { props: { agentApi: new OfflineApi(), eventStreamFactory: passiveEventStream } });
    await flushPromises();
    expect(wrapper.find('.diagnostic-card.manager').text()).toContain('Agent Managerに接続できません');
    wrapper.unmount();

    class UnauthorizedApi extends MockAgentApi {
      override async listSessions(): Promise<never> {
        throw new AgentApiError('unauthorized', 401, 'unauthorized');
      }
    }
    wrapper = mount(App, { props: { agentApi: new UnauthorizedApi(), eventStreamFactory: passiveEventStream } });
    await flushPromises();
    expect(wrapper.find('.diagnostic-card.auth').text()).toContain('認証に失敗しました');
  });

  it('shows the Codex installation diagnostic from a failed run event', async () => {
    const mounted = await mountApp();
    const failure = mounted.wrapper.findAll('.session-item').find((item) => item.text().includes('failure'));
    await failure!.trigger('click');
    await flushPromises();
    expect(mounted.wrapper.find('.diagnostic-card.codex').text()).toContain('Codex CLIを利用できません');
  });

  it('shows the GitHub Copilot installation diagnostic from a failed run event', async () => {
    const mounted = await mountApp();
    const failure = mounted.wrapper.findAll('.session-item').find((item) => item.text().includes('copilot-failure'));
    await failure!.trigger('click');
    await flushPromises();
    expect(mounted.wrapper.find('.diagnostic-card.copilot').text()).toContain('GitHub Copilot CLIを利用できません');
  });
});
