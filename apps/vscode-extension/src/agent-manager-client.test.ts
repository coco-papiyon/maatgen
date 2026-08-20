import { afterEach, describe, expect, it, vi } from 'vitest';
import { AgentManagerClient } from './agent-manager-client.js';

afterEach(() => vi.unstubAllGlobals());

describe('AgentManagerClient session integration', () => {
  it('loads the provider and model catalog', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      providers: [{ id: 'codex', label: 'Codex', models: ['gpt-5.6-sol'], defaultModel: 'gpt-5.6-sol' }],
    }), { status: 200 }));
    vi.stubGlobal('fetch', fetch);

    await expect(new AgentManagerClient('http://127.0.0.1:3100', 'shared-token').listProviders()).resolves.toEqual([
      { id: 'codex', label: 'Codex', models: ['gpt-5.6-sol'], defaultModel: 'gpt-5.6-sol' },
    ]);
    expect(fetch).toHaveBeenCalledWith('http://127.0.0.1:3100/api/v1/providers', expect.objectContaining({
      headers: expect.objectContaining({ Authorization: 'Bearer shared-token' }),
    }));
  });

  it('reads every shared Manager session page used by the Web app', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({
        sessions: [{ id: 'vscode-session', agent: 'codex', workspace: 'C:/repo', status: 'closed', createdAt: '2026-08-15T00:00:00Z' }],
        nextCursor: 'next',
      }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        sessions: [{ id: 'older-session', agent: 'codex', workspace: 'C:/repo', status: 'closed', createdAt: '2026-08-14T00:00:00Z' }],
      }), { status: 200 }));
    vi.stubGlobal('fetch', fetch);

    const sessions = await new AgentManagerClient('http://127.0.0.1:3100', 'shared-token').listSessions();

    expect(sessions.map((session) => session.id)).toEqual(['vscode-session', 'older-session']);
    expect(fetch).toHaveBeenNthCalledWith(1, 'http://127.0.0.1:3100/api/v1/sessions?limit=100&status=all', expect.objectContaining({
      headers: expect.objectContaining({ Authorization: 'Bearer shared-token' }),
    }));
    expect(String(fetch.mock.calls[1]?.[0])).toContain('cursor=next');
  });

  it('loads and decides pending command approvals', async () => {
    const approval = { id: 'approval-1', command: 'go test ./...', status: 'pending', factors: [], segments: [] };
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ approvals: [approval] }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ ...approval, status: 'approved' }), { status: 200 }));
    vi.stubGlobal('fetch', fetch);
    const client = new AgentManagerClient('http://127.0.0.1:3100', 'shared-token');

    await expect(client.listApprovals('session-1')).resolves.toEqual([approval]);
    await client.decideApproval('session-1', 'approval-1', { decision: 'allow_session', ruleArgv: ['go', 'test', '*'] });

    expect(fetch).toHaveBeenNthCalledWith(1, 'http://127.0.0.1:3100/api/v1/sessions/session-1/approvals?status=pending', expect.any(Object));
    expect(fetch).toHaveBeenNthCalledWith(2, 'http://127.0.0.1:3100/api/v1/sessions/session-1/approvals/approval-1/decision', expect.objectContaining({
      method: 'POST', body: JSON.stringify({ decision: 'allow_session', ruleArgv: ['go', 'test', '*'] }),
    }));
  });
});
