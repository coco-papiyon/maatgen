import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import App from './App.vue';
import { AgentApiError } from './api';
import type { EventStreamFactory } from './event-stream';
import { createMockEnvironment, MockAgentApi } from './testing/mock-agent-api';

let wrapper: VueWrapper | undefined;

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
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
  it('renders session history, timeline events, changes, and live state', async () => {
    const mounted = await mountApp();
    expect(mounted.wrapper.find('.error-banner').exists() ? mounted.wrapper.find('.error-banner').text() : '').toBe('');
    expect(mounted.wrapper.findAll('.session-item')).toHaveLength(4);
    expect(mounted.wrapper.text()).toContain('認証処理を確認し、テストを追加しました。');
    expect(mounted.wrapper.find('.stream-state').text()).toBe('Live');
    expect(mounted.wrapper.find('.change-title').text()).toContain('src/auth.ts');
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

  it('opens a multi-hunk diff and applies a hunk review', async () => {
    const mounted = await mountApp();
    const session = mounted.wrapper.findAll('.session-item').find((item) => item.text().includes('multi-hunk'));
    expect(session).toBeDefined();
    await session!.trigger('click');
    await flushPromises();

    await mounted.wrapper.find('.change-card').trigger('click');
    expect(mounted.wrapper.findAll('.diff-hunk')).toHaveLength(2);
    expect(mounted.wrapper.findAll('.review-badge').map((badge) => badge.text())).toEqual(['pending', 'pending']);

    await mounted.wrapper.find('.accept-button').trigger('click');
    await flushPromises();
    expect(mounted.wrapper.findAll('.review-badge').map((badge) => badge.text())).toEqual(['accepted', 'pending']);
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
});
