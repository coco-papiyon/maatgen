import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import App from './App.vue';
import { AgentApiError } from './api';
import type { EventStreamFactory } from './event-stream';
import { createMockEnvironment, MockAgentApi } from './testing/mock-agent-api';
import type { ApprovalDecisionRequest, CommandApproval, SessionEvent } from '@maatgen/protocol';

let wrapper: VueWrapper | undefined;

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  localStorage.removeItem('maatgen.showSystemMessages');
  localStorage.removeItem('maatgen.provider');
  localStorage.removeItem('maatgen.sidePanel');
  localStorage.removeItem('maatgen.sessionStatusFilter');
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
    expect(mounted.wrapper.findAll('.session-item')).toHaveLength(6);
    expect(mounted.wrapper.text()).toContain('認証処理を確認し、テストを追加しました。');
    expect(mounted.wrapper.find('.stream-state').text()).toBe('Live');
    expect(mounted.wrapper.find('.change-title').text()).toContain('src/auth.ts');
  });

  it('marks sessions with unread assistant activity and clears the mark when opened', async () => {
    const mounted = await mountApp();
    const unread = mounted.wrapper.findAll('.unread-mark');
    expect(unread.length).toBeGreaterThan(0);

    const unreadSession = mounted.wrapper.findAll('.session-item').find((item) => item.find('.unread-mark').exists())!;
    expect(unreadSession.exists()).toBe(true);
    await unreadSession.trigger('click');
    await flushPromises();

    expect(unreadSession.find('.unread-mark').exists()).toBe(false);
  });

  it('shows the remaining provider usage percentage', async () => {
    class ProviderUsageApi extends MockAgentApi {
      override async getProviderUsage() {
        return {
          provider: 'codex' as const,
          windows: [{ name: 'primary', usedPercent: 3, remainingPercent: 97 }],
          fetchedAt: '2026-08-21T17:32:54Z',
        };
      }
    }
    wrapper = mount(App, { props: { agentApi: new ProviderUsageApi(), eventStreamFactory: passiveEventStream } });
    await flushPromises();
    expect(wrapper.find('.provider-usage-summary').text()).toBe('primary 97%');
  });

  it('hides a closed session from the list but keeps it available via the status filter', async () => {
    const mounted = await mountApp();
    const target = mounted.wrapper.findAll('.session-item').find((item) => item.text().includes('success'));
    expect(target).toBeDefined();
    await target!.trigger('click');
    await flushPromises();

    const closeButton = mounted.wrapper.findAll('button').find((button) => button.text() === 'Close session');
    expect(closeButton).toBeDefined();
    await closeButton!.trigger('click');
    await flushPromises();

    expect(mounted.wrapper.findAll('.session-item')).toHaveLength(5);
    expect(mounted.wrapper.findAll('.session-item').some((item) => item.text().includes('success'))).toBe(false);

    await mounted.wrapper.find('.session-filter select').setValue('all');
    await flushPromises();
    expect(mounted.wrapper.findAll('.session-item')).toHaveLength(6);
    expect(mounted.wrapper.findAll('.session-item').some((item) => item.text().includes('success'))).toBe(true);
  });

  it('reopens a closed session', async () => {
    const mounted = await mountApp();
    const target = mounted.wrapper.findAll('.session-item').find((item) => item.text().includes('success'));
    await target!.trigger('click');
    await flushPromises();

    const closeButton = mounted.wrapper.findAll('button').find((button) => button.text() === 'Close session');
    await closeButton!.trigger('click');
    await flushPromises();
    expect(mounted.wrapper.find('.run-state').text()).toContain('終了済み');

    await mounted.wrapper.find('.session-filter select').setValue('all');
    await flushPromises();
    const successInAll = mounted.wrapper.findAll('.session-item').find((item) => item.text().includes('success'));
    await successInAll!.trigger('click');
    await flushPromises();

    const reopenButton = mounted.wrapper.findAll('button').find((button) => button.text() === 'Reopen session');
    expect(reopenButton).toBeDefined();
    await reopenButton!.trigger('click');
    await flushPromises();
    expect(mounted.wrapper.find('.run-state').text()).toContain('準備完了');
  });

  it('restores a pending command approval and submits a session rule', async () => {
    class ApprovalApi extends MockAgentApi {
      decision?: ApprovalDecisionRequest;
      approval: CommandApproval = {
        id: 'approval-1', sessionId: 'mock-success', runId: 'run-1', providerRequestId: 'provider-1',
        command: 'go test ./internal/approval', shell: 'powershell', workingDirectory: 'C:/demo/success',
        segments: [{ index: 0, command: 'go test ./internal/approval', argv: ['go', 'test', './internal/approval'], allowed: false }],
        status: 'pending', risk: 'high', summary: 'テストコマンドの確認が必要です', factors: ['workspace-write'],
        createdAt: '2026-08-16T00:00:00Z',
      };

      override async listApprovals(id: string) {
        return id === this.approval.sessionId && this.approval.status === 'pending' ? [this.approval] : [];
      }

      override async decideApproval(_sessionId: string, _approvalId: string, request: ApprovalDecisionRequest) {
        this.decision = request;
        this.approval = { ...this.approval, status: 'approved', decision: request.decision };
        return this.approval;
      }
    }
    const api = new ApprovalApi();
    wrapper = mount(App, { props: { agentApi: api, eventStreamFactory: passiveEventStream } });
    await flushPromises();

    expect(wrapper.find('.approval-dialog').text()).toContain('go test ./internal/approval');
    await wrapper.find('.approval-rule input').setValue('go test *');
    await wrapper.findAll('.approval-actions button')[2]!.trigger('click');
    await flushPromises();

    expect(api.decision).toEqual({ decision: 'allow_session', ruleArgv: ['go', 'test', '*'] });
    expect(wrapper.find('.approval-dialog').exists()).toBe(false);
  });

  it('shows the error inside the approval dialog when a rule is rejected, instead of hiding it behind the modal', async () => {
    class RejectingApprovalApi extends MockAgentApi {
      approval: CommandApproval = {
        id: 'approval-3', sessionId: 'mock-success', runId: 'run-1', providerRequestId: 'provider-3',
        command: 'go test ./internal/approval', shell: 'powershell', workingDirectory: 'C:/demo/success',
        segments: [{ index: 0, command: 'go test ./internal/approval', argv: ['go', 'test', './internal/approval'], allowed: false }],
        status: 'pending', risk: 'high', summary: 'テストコマンドの確認が必要です', factors: ['workspace-write'],
        createdAt: '2026-08-16T00:00:00Z',
      };

      override async listApprovals(id: string) {
        return id === this.approval.sessionId && this.approval.status === 'pending' ? [this.approval] : [];
      }

      override async decideApproval(): Promise<CommandApproval> {
        throw new AgentApiError('ruleArgv must match an approval segment', 409, 'conflict');
      }
    }
    const api = new RejectingApprovalApi();
    wrapper = mount(App, { props: { agentApi: api, eventStreamFactory: passiveEventStream } });
    await flushPromises();

    await wrapper.find('.approval-rule input').setValue('$env:GOCACHE=*');
    await wrapper.findAll('.approval-actions button')[3]!.trigger('click');
    await flushPromises();

    expect(wrapper.find('.approval-dialog').exists()).toBe(true);
    expect(wrapper.find('.approval-dialog .error-banner').text()).toContain('ruleArgv must match an approval segment');
  });

  it('shows per-segment approval status and defaults the rule to the first unapproved segment', async () => {
    class SplitApprovalApi extends MockAgentApi {
      approval: CommandApproval = {
        id: 'approval-2', sessionId: 'mock-success', runId: 'run-1', providerRequestId: 'provider-2',
        command: `"C:\\Program Files\\PowerShell\\7\\pwsh.exe" -Command "gofmt -w store_test.go; $env:GOCACHE='C:\\tmp\\dedupe'; go test ./..."`,
        shell: 'powershell', workingDirectory: 'C:/demo/success',
        segments: [
          { index: 0, command: 'gofmt -w store_test.go', argv: ['gofmt', '-w', 'store_test.go'], allowed: true },
          { index: 1, command: "$env:GOCACHE='C:\\tmp\\dedupe'", argv: [], allowed: false },
          { index: 2, command: 'go test ./...', argv: ['go', 'test', './...'], allowed: false },
        ],
        status: 'pending', factors: [], createdAt: '2026-08-16T00:00:00Z',
      };

      override async listApprovals(id: string) {
        return id === this.approval.sessionId && this.approval.status === 'pending' ? [this.approval] : [];
      }
    }
    const api = new SplitApprovalApi();
    wrapper = mount(App, { props: { agentApi: api, eventStreamFactory: passiveEventStream } });
    await flushPromises();

    const statuses = wrapper.findAll('.segment-status');
    expect(statuses.map((node) => node.classes()).map((classes) => classes.includes('allowed'))).toEqual([true, false, false]);
    expect(wrapper.find<HTMLInputElement>('.approval-rule input').element.value).toBe("$env:GOCACHE='C:\\tmp\\dedupe'");
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

  it('shows source line counts by language in the コード数 tab', async () => {
    const mounted = await mountApp();
    await mounted.wrapper.find('#source-stats-tab').trigger('click');
    expect(mounted.wrapper.find('#source-stats-panel').exists()).toBe(true);
    expect(mounted.wrapper.find('#changes-panel').exists()).toBe(false);
    expect(mounted.wrapper.find('#source-stats-tab').attributes('aria-selected')).toBe('true');
    const text = mounted.wrapper.find('#source-stats-panel').text();
    expect(text).toContain('Go');
    expect(text).toContain('9,894 code');
    expect(text).toContain('TypeScript');
  });

  it('opens Run details in the central pane and returns to the chat', async () => {
    const mounted = await mountApp();
    await mounted.wrapper.find('#usage-tab').trigger('click');
    await mounted.wrapper.find('.usage-run').trigger('click');

    expect(mounted.wrapper.find('.run-detail').exists()).toBe(true);
    expect(mounted.wrapper.find('.run-detail').text()).toContain('Mock usage run');
    expect(mounted.wrapper.find('.timeline').exists()).toBe(false);
    expect(mounted.wrapper.find('.composer').exists()).toBe(false);

    await mounted.wrapper.find('.run-detail .quiet-button').trigger('click');
    expect(mounted.wrapper.find('.run-detail').exists()).toBe(false);
    expect(mounted.wrapper.find('.timeline').exists()).toBe(true);
    expect(mounted.wrapper.find('.composer').exists()).toBe(true);
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
    expect(wrapper.findAll('.session-item')).toHaveLength(6);
    expect(wrapper.find('.load-more').exists()).toBe(false);
  });

  it('shows an actionable Manager diagnostic when the Manager is unreachable', async () => {
    class OfflineApi extends MockAgentApi {
      override async listSessions(): Promise<never> {
        throw new TypeError('Failed to fetch');
      }
    }
    wrapper = mount(App, { props: { agentApi: new OfflineApi(), eventStreamFactory: passiveEventStream } });
    await flushPromises();
    expect(wrapper.find('.diagnostic-card.manager').text()).toContain('Agent Managerに接続できません');
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

  it('shows the Claude Code installation diagnostic from a failed run event', async () => {
    const mounted = await mountApp();
    const failure = mounted.wrapper.findAll('.session-item').find((item) => item.text().includes('claude-failure'));
    await failure!.trigger('click');
    await flushPromises();
    expect(mounted.wrapper.find('.diagnostic-card.claude').text()).toContain('Claude Code CLIを利用できません');
  });

  it('shows Claude Code token metrics and its CLI reported cost', async () => {
    const mounted = await mountApp();
    const claudeSession = mounted.wrapper.findAll('.session-item').find((item) => item.text().includes('claude-failure'));
    await claudeSession!.trigger('click');
    await flushPromises();
    await mounted.wrapper.find('#usage-tab').trigger('click');
    const panel = mounted.wrapper.find('#usage-panel').text();
    expect(panel).toContain('Input');
    expect(panel).toContain('1,200');
    expect(panel).toContain('$0.250000');
    expect(panel).not.toContain('AI credits');
  });
});
