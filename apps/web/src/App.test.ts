import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import App from './App.vue';
import { AgentApiError } from './api';
import type { EventStreamFactory } from './event-stream';
import { createMockEnvironment, MockAgentApi } from './testing/mock-agent-api';
import type { ApprovalDecisionRequest, CommandApproval, NodeScopedSession, SessionEvent } from '@maatgen/protocol';
import { colorForNode } from './nodeColors';
import { nodes, selectedNodeId } from './nodes';

let wrapper: VueWrapper | undefined;

afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
  localStorage.removeItem('maatgen.showSystemMessages');
  localStorage.removeItem('maatgen.provider');
  localStorage.removeItem('maatgen.sidePanel');
  localStorage.removeItem('maatgen.sessionStatusFilter');
  localStorage.removeItem('maatgen.workspaceHistory');
  localStorage.removeItem('maatgen.sessionHost');
  localStorage.removeItem('maatgen.showAllServers');
  nodes.value = [];
  selectedNodeId.value = 'local';
  vi.useRealTimers();
  vi.restoreAllMocks();
});

async function mountApp(api = new MockAgentApi()) {
  const environment = createMockEnvironment(api);
  wrapper = mount(App, { props: environment });
  await flushPromises();
  return { wrapper, environment, api };
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

  it('uses Hostname only as the new Session target and keeps the Session list filter unchanged', async () => {
    class MultiHostApi extends MockAgentApi {
      override async listNodes() {
        return [
          { id: 'local', name: 'Local host', status: 'connected' as const, createdAt: '2026-08-15T00:00:00Z' },
          { id: 'linux-dev', name: 'Linux dev box', status: 'connected' as const, createdAt: '2026-08-15T00:00:00Z' },
        ];
      }

      override async getDefaultWorkspace(nodeId?: string) {
        return nodeId === 'linux-dev' ? '/home/dev/maatgen' : 'C:/demo/current-repository';
      }
    }

    const api = new MultiHostApi();
    const createSession = vi.spyOn(api, 'createSession');
    const mounted = await mountApp(api);
    const labels = mounted.wrapper.findAll('form.new-session label');
    expect(labels[0]?.text()).toContain('Provider');
    expect(labels[1]?.text()).toContain('Hostname');
    const sessionCount = mounted.wrapper.findAll('.session-item').length;

    await mounted.wrapper.get('select[aria-label="Hostname"]').setValue('linux-dev');
    await flushPromises();

    expect(selectedNodeId.value).toBe('local');
    expect(mounted.wrapper.findAll('.session-item')).toHaveLength(sessionCount);
    expect((mounted.wrapper.get('#workspace').element as HTMLInputElement).value).toBe('/home/dev/maatgen');

    await mounted.wrapper.get('form.new-session').trigger('submit');
    await flushPromises();
    expect(createSession).toHaveBeenCalledWith(expect.objectContaining({ workspace: '/home/dev/maatgen' }), 'linux-dev');
    expect(selectedNodeId.value).toBe('local');
    expect(mounted.wrapper.findAll('.session-item')).toHaveLength(sessionCount + 1);
  });

  it('does not show a Sessions label beside the all-server filter', async () => {
    const mounted = await mountApp();
    expect(mounted.wrapper.get('.session-section-heading').text()).not.toContain('Sessions');
    expect(mounted.wrapper.get('.session-section-heading').text()).toContain('全サーバ');
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

  it('marks only running sessions and removes the mark after the session list refreshes', async () => {
    vi.useFakeTimers();
    class RunningSessionApi extends MockAgentApi {
      running = true;

      override async listSessions() {
        const page = await super.listSessions();
        return {
          ...page,
          sessions: page.sessions.map((session, index) => ({
            ...session,
            ...(index === 0 && this.running ? { activeRunStatus: 'running' as const } : {}),
            ...(index === 1 ? { activeRunStatus: 'starting' as const } : {}),
          })),
        };
      }
    }
    const api = new RunningSessionApi();
    localStorage.setItem('maatgen.showAllServers', '0');
    wrapper = mount(App, { props: { agentApi: api, eventStreamFactory: passiveEventStream } });
    await flushPromises();

    expect(wrapper.findAll('.running-mark')).toHaveLength(1);
    expect(wrapper.find('.running-mark').text()).toBe('実行中');
    expect(wrapper.find('.running-mark').element.parentElement?.textContent).toContain('success');

    api.running = false;
    await vi.advanceTimersByTimeAsync(10_000);
    await flushPromises();

    expect(wrapper.find('.running-mark').exists()).toBe(false);
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

  it('shows Git status with commit and push actions in a five-tab row', async () => {
    const api = new MockAgentApi();
    const commit = vi.spyOn(api, 'commitGitChanges');
    const push = vi.spyOn(api, 'pushGitChanges');
    const mounted = await mountApp(api);

    expect(mounted.wrapper.findAll('.side-panel-tabs button')).toHaveLength(5);
    expect(mounted.wrapper.find('#source-stats-tab .tab-count').exists()).toBe(false);
    expect(mounted.wrapper.find('#files-tab .tab-count').exists()).toBe(false);
    expect(mounted.wrapper.find('#git-tab .tab-count').exists()).toBe(false);

    await mounted.wrapper.find('#git-tab').trigger('click');
    await flushPromises();
    expect(mounted.wrapper.find('#git-panel').text()).toContain('origin/main');
    expect(mounted.wrapper.find('#git-panel').text()).toContain('README.md');

    await mounted.wrapper.findAll('.git-commit button')[1]!.trigger('click');
    await flushPromises();
    expect(push).toHaveBeenCalled();

    await mounted.wrapper.find<HTMLInputElement>('.git-commit input').setValue('Commit from Git tab');
    await mounted.wrapper.find('.git-commit').trigger('submit');
    await flushPromises();
    await flushPromises();
    expect(commit).toHaveBeenCalledWith(expect.any(String), 'Commit from Git tab');
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

  it('shows a file tree in the Files tab and renders markdown files in the center pane', async () => {
    const mounted = await mountApp();
    await mounted.wrapper.find('#files-tab').trigger('click');
    await flushPromises();
    expect(mounted.wrapper.find('#files-panel').exists()).toBe(true);
    expect(mounted.wrapper.find('#changes-panel').exists()).toBe(false);
    expect(mounted.wrapper.find('#files-tab').attributes('aria-selected')).toBe('true');
    expect(mounted.wrapper.find('.file-tree').text()).toContain('README.md');

    const readmeButton = mounted.wrapper.findAll('.file-tree-file').find((item) => item.text().includes('README.md'))!;
    expect(readmeButton.find('.file-tree-caret').exists()).toBe(true);
    await readmeButton.trigger('click');
    await flushPromises();

    expect(mounted.wrapper.find('.file-view').exists()).toBe(true);
    expect(mounted.wrapper.find('.timeline').exists()).toBe(false);
    expect(mounted.wrapper.find('.file-view-markdown').html()).toContain('<h1');

    await mounted.wrapper.find('.file-view .quiet-button').trigger('click');
    expect(mounted.wrapper.find('.file-view').exists()).toBe(false);
    expect(mounted.wrapper.find('.timeline').exists()).toBe(true);
  });

  it('renders non-markdown files as plain source text', async () => {
    const mounted = await mountApp();
    await mounted.wrapper.find('#files-tab').trigger('click');
    await flushPromises();
    await mounted.wrapper.find('.file-tree-dir').trigger('click');
    await flushPromises();
    const authButton = mounted.wrapper.findAll('.file-tree-file').find((item) => item.text().includes('auth.ts'))!;
    await authButton.trigger('click');
    await flushPromises();

    expect(mounted.wrapper.find('.file-view-markdown').exists()).toBe(false);
    expect(mounted.wrapper.find('.file-view-source').text()).toContain('export const enabled = true;');
  });

  it('toggles a markdown file between rendered and raw views', async () => {
    const mounted = await mountApp();
    await mounted.wrapper.find('#files-tab').trigger('click');
    await flushPromises();
    const readmeButton = mounted.wrapper.findAll('.file-tree-file').find((item) => item.text().includes('README.md'))!;
    await readmeButton.trigger('click');
    await flushPromises();

    const rawButton = mounted.wrapper.findAll('.file-view-action-button').find((button) => button.text() === 'Raw')!;
    expect(mounted.wrapper.find('.file-view-markdown').exists()).toBe(true);
    await rawButton.trigger('click');

    expect(mounted.wrapper.find('.file-view-markdown').exists()).toBe(false);
    expect(mounted.wrapper.get('.file-view-source').text()).toContain('# Mock Repository');
    expect(rawButton.text()).toBe('Markdown');

    await rawButton.trigger('click');
    expect(mounted.wrapper.find('.file-view-markdown').exists()).toBe(true);
  });

  it('copies the displayed file content to the clipboard', async () => {
    vi.useFakeTimers();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const mounted = await mountApp();
    await mounted.wrapper.find('#files-tab').trigger('click');
    await flushPromises();
    const readmeButton = mounted.wrapper.findAll('.file-tree-file').find((item) => item.text().includes('README.md'))!;
    await readmeButton.trigger('click');
    await flushPromises();

    const copyButton = mounted.wrapper.findAll('.file-view-action-button').find((button) => button.text() === 'Copy')!;
    await copyButton.trigger('click');

    expect(writeText).toHaveBeenCalledWith('# Mock Repository\n\nこれはWeb版のFileタブ用のモックデータです。\n\n- ツリー表示\n- Markdown変換表示\n');
    expect(copyButton.text()).toBe('Copied');
    vi.advanceTimersByTime(1500);
    await mounted.wrapper.vm.$nextTick();
    expect(copyButton.text()).toBe('Copy');
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

  it("shows the raw/copy toolbar only on a Run's final assistant reply, not on an intermediate one", async () => {
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
    const base = { sessionId: 'mock-success', timestamp: new Date().toISOString(), schemaVersion: 2 as const, source: 'manager' as const, runId: 'run-x' };
    pushEvent!({ ...base, id: 'reply-1', sequence: 10, type: 'assistant_message', data: { text: '途中経過です。' } });
    pushEvent!({ ...base, id: 'reply-2', sequence: 11, type: 'assistant_message', data: { text: '**最終回答**です。' } });
    await flushPromises();

    const articles = wrapper.findAll('.event.assistant');
    const intermediate = articles.find((article) => article.text().includes('途中経過'))!;
    const final = articles.find((article) => article.text().includes('最終回答'))!;
    expect(intermediate.find('.event-actions').exists()).toBe(false);
    expect(final.find('.event-actions').exists()).toBe(true);
    expect(final.findAll('.event-action-button').map((button) => button.text())).toEqual(['Raw', 'Copy']);
  });

  it('toggles a final assistant reply between rendered markdown and raw source', async () => {
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
    pushEvent!({
      id: 'reply-final', sessionId: 'mock-success', sequence: 10, runId: 'run-x',
      timestamp: new Date().toISOString(), schemaVersion: 2, source: 'manager',
      type: 'assistant_message', data: { text: '**強調**テキストです。' },
    });
    await flushPromises();

    const article = wrapper.findAll('.event.assistant').find((item) => item.text().includes('強調テキストです'))!;
    expect(article.find('.markdown-body').exists()).toBe(true);
    expect(article.find('.event-body-raw').exists()).toBe(false);

    await article.get('.event-action-button').trigger('click');
    expect(article.find('.markdown-body').exists()).toBe(false);
    expect(article.get('.event-body-raw').text()).toBe('**強調**テキストです。');
    expect(article.get('.event-action-button').text()).toBe('Markdown');

    await article.get('.event-action-button').trigger('click');
    expect(article.find('.markdown-body').exists()).toBe(true);
    expect(article.find('.event-body-raw').exists()).toBe(false);
  });

  it('copies the raw text of a final assistant reply to the clipboard', async () => {
    vi.useFakeTimers();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
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
    pushEvent!({
      id: 'reply-final', sessionId: 'mock-success', sequence: 10, runId: 'run-x',
      timestamp: new Date().toISOString(), schemaVersion: 2, source: 'manager',
      type: 'assistant_message', data: { text: 'コピー対象のテキスト' },
    });
    await flushPromises();

    const article = wrapper.findAll('.event.assistant').find((item) => item.text().includes('コピー対象'))!;
    const copyButton = article.findAll('.event-action-button')[1]!;
    await copyButton.trigger('click');
    await flushPromises();

    expect(writeText).toHaveBeenCalledWith('コピー対象のテキスト');
    expect(copyButton.text()).toBe('Copied');

    await vi.advanceTimersByTimeAsync(1500);
    await flushPromises();
    expect(copyButton.text()).toBe('Copy');
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

  it('does not send autoApprove by default', async () => {
    const mounted = await mountApp();
    const send = vi.spyOn(mounted.environment.agentApi, 'sendMessage').mockResolvedValue({
      id: 'run-default', sessionId: 'mock-success', status: 'running', prompt: '通常のテスト',
    });
    expect((mounted.wrapper.get('input[aria-label="自動承認"]').element as HTMLInputElement).checked).toBe(false);
    await mounted.wrapper.find('.composer textarea').setValue('通常のテスト');
    await mounted.wrapper.find('.composer').trigger('submit');
    await flushPromises();
    expect(send).toHaveBeenCalledWith(expect.any(String), { message: '通常のテスト' });
  });

  it('enables Codex auto-approval for the next run', async () => {
    const mounted = await mountApp();
    const send = vi.spyOn(mounted.environment.agentApi, 'sendMessage').mockResolvedValue({
      id: 'run-auto-approve', sessionId: 'mock-success', status: 'running', prompt: '自動承認のテスト',
    });
    await mounted.wrapper.get('input[aria-label="自動承認"]').setValue(true);
    await mounted.wrapper.find('.composer textarea').setValue('自動承認のテスト');
    await mounted.wrapper.find('.composer').trigger('submit');
    await flushPromises();
    expect(send).toHaveBeenCalledWith(expect.any(String), {
      message: '自動承認のテスト',
      autoApprove: true,
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

  it('remembers a newly created Session workspace as a Repository path history entry', async () => {
    const mounted = await mountApp();
    await mounted.wrapper.find('#workspace').setValue('C:/repo/new-project');
    await mounted.wrapper.find('form.new-session').trigger('submit');
    await flushPromises();
    await mounted.wrapper.find('.workspace-history-toggle').trigger('click');
    const options = mounted.wrapper.findAll('#workspace-history li').map((option) => option.text());
    expect(options).toContain('C:/repo/new-project');
    expect(JSON.parse(localStorage.getItem('maatgen.workspaceHistory') ?? '[]')).toContain('C:/repo/new-project');
  });

  it('offers previously used Repository paths from history while still allowing free text entry', async () => {
    localStorage.setItem('maatgen.workspaceHistory', JSON.stringify(['C:/repo/previous-project', 'D:/work/another-project']));
    const mounted = await mountApp();
    const input = mounted.wrapper.find('#workspace');
    await input.setValue('previous');
    await mounted.wrapper.find('.workspace-history-toggle').trigger('click');
    const options = mounted.wrapper.findAll('#workspace-history li').map((option) => option.text());
    expect(options).toEqual(['C:/repo/previous-project', 'D:/work/another-project']);
    const secondOption = mounted.wrapper.findAll('#workspace-history li')[1];
    expect(secondOption).toBeDefined();
    await secondOption!.trigger('mousedown');
    expect((input.element as HTMLInputElement).value).toBe('D:/work/another-project');
    expect(mounted.wrapper.find('#workspace-history').exists()).toBe(false);
    await input.setValue('C:/repo/typed-freely');
    expect((input.element as HTMLInputElement).value).toBe('C:/repo/typed-freely');
  });

  it('moves a repeated Repository path to the front of history instead of duplicating it', async () => {
    localStorage.setItem('maatgen.workspaceHistory', JSON.stringify(['C:/repo/older', 'C:/repo/newer']));
    const mounted = await mountApp();
    await mounted.wrapper.find('#workspace').setValue('C:/repo/older');
    await mounted.wrapper.find('form.new-session').trigger('submit');
    await flushPromises();
    expect(JSON.parse(localStorage.getItem('maatgen.workspaceHistory') ?? '[]')).toEqual(['C:/repo/older', 'C:/repo/newer']);
  });

  it("shows the session's first instruction in the sidebar, falling back to the workspace path until one is sent", async () => {
    const mounted = await mountApp();
    await mounted.wrapper.find('#workspace').setValue('C:/repo/new-project');
    await mounted.wrapper.find('form.new-session').trigger('submit');
    await flushPromises();

    const item = () => mounted.wrapper.findAll('.session-item').find((entry) => entry.text().includes('new-project'))!;
    expect(item().find('.session-title').text()).toBe('repo/new-project');

    await mounted.wrapper.find('.composer textarea').setValue('最初の指示');
    await mounted.wrapper.find('.composer').trigger('submit');
    await flushPromises();
    // Force a session-list refetch (mirrors the periodic poll that normally
    // picks up the change) rather than asserting on timer internals.
    await mounted.wrapper.find('.session-filter select').setValue('all');
    await flushPromises();
    await mounted.wrapper.find('.session-filter select').setValue('active');
    await flushPromises();

    expect(item().find('.session-title').text()).toBe('最初の指示');
    expect(item().find('.session-meta').text()).toContain('new-project');
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
    localStorage.setItem('maatgen.showAllServers', '0');
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

  describe('cross-server aggregate Session list (ADR-009 Decision 5.1)', () => {
    const remoteSession: NodeScopedSession = {
      id: 'agg-remote', agent: 'claude', workspace: '/tmp/remote-workspace', workspaceKind: 'git_repository',
      status: 'active', triggerSource: 'manual', createdAt: '2026-08-15T01:00:00Z', nodeId: 'linux-dev', nodeName: 'Linux dev box',
    };
    const localSession: NodeScopedSession = {
      id: 'agg-local', agent: 'codex', workspace: '/tmp/local-workspace', workspaceKind: 'git_repository',
      status: 'active', triggerSource: 'manual', createdAt: '2026-08-15T00:00:00Z', nodeId: 'local', nodeName: 'Local',
    };

    it('is on by default and shows sessions from every server', async () => {
      const mounted = await mountApp();
      expect((mounted.wrapper.get('.all-servers-toggle input').element as HTMLInputElement).checked).toBe(true);
      expect(mounted.wrapper.findAll('.session-item')).toHaveLength(6);
    });

    it('keeps an explicitly disabled all-server filter off', async () => {
      localStorage.setItem('maatgen.showAllServers', '0');
      const mounted = await mountApp();
      expect((mounted.wrapper.get('.all-servers-toggle input').element as HTMLInputElement).checked).toBe(false);
    });

    it('lists sessions from every connected server with a node label when enabled', async () => {
      const api = new MockAgentApi();
      api.setAggregateSessions([remoteSession, localSession]);
      const { wrapper } = await mountApp(api);

      const items = wrapper.findAll('.session-item');
      expect(items).toHaveLength(2);
      expect(wrapper.text()).toContain('Linux dev box');
      expect(wrapper.text()).toContain('Local');
    });

    it('colors the active-session dot by its server, and turns off the color for a closed session', async () => {
      const api = new MockAgentApi();
      api.setAggregateSessions([
        remoteSession,
        { ...localSession, id: 'agg-local-closed', status: 'closed' },
      ]);
      const { wrapper } = await mountApp(api);

      const items = wrapper.findAll('.session-item');
      const activeDot = items[0]!.get('.mini-dot');
      expect((activeDot.attributes('style') ?? '')).toContain(colorForNode('linux-dev'));
      const closedDot = items[1]!.get('.mini-dot');
      expect(closedDot.attributes('style')).toBeUndefined();
    });

    it('switches to a session’s own server when it is opened from the aggregate list', async () => {
      const api = new MockAgentApi();
      api.setAggregateSessions([remoteSession]);
      const { wrapper } = await mountApp(api);
      nodes.value = [...nodes.value, { id: 'linux-dev', name: 'Linux dev box', status: 'connected', createdAt: '2026-08-15T00:00:00Z' }];
      await wrapper.get('.session-item').trigger('click');
      await flushPromises();

      expect(selectedNodeId.value).toBe('linux-dev');
    });

    it('opens the session directly, without switching nodes, when it already belongs to the current node', async () => {
      const api = new MockAgentApi();
      api.setAggregateSessions([localSession]);
      const { wrapper } = await mountApp(api);
      const item = wrapper.get('.session-item');
      await item.trigger('click');
      await flushPromises();

      expect(selectedNodeId.value).toBe('local');
      expect(item.classes()).toContain('selected');
    });
  });
});
