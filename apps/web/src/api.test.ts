import { afterEach, describe, expect, it, vi } from 'vitest';
import { AgentApiError, httpAgentApi } from './api';

afterEach(() => vi.unstubAllGlobals());

describe('httpAgentApi', () => {
  it('reads the default workspace from manager runtime configuration', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      defaultWorkspace: 'C:/projects/current',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', fetch);

    await expect(httpAgentApi.getDefaultWorkspace()).resolves.toBe('C:/projects/current');
    expect(fetch).toHaveBeenCalledWith('/api/v1/runtime-config', expect.any(Object));
  });

  it('requests an opaque session cursor and returns the next page cursor', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      sessions: [],
      nextCursor: 'next-page',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', fetch);

    const page = await httpAgentApi.listSessions('current-page', 25);
    const target = String(fetch.mock.calls[0]?.[0]);
    expect(target).toContain('limit=25');
    expect(target).toContain('cursor=current-page');
    expect(page.nextCursor).toBe('next-page');
  });

  it('lists every registered GitHub repository monitor', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ monitors: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', fetch);

    await httpAgentApi.listGitHubMonitors();
    expect(fetch).toHaveBeenCalledWith('/api/v1/github/monitors', expect.any(Object));
  });

  it('omits the workspace query parameter when listing trigger rules across every repository', async () => {
    const fetch = vi.fn().mockImplementation(async () =>
      new Response(JSON.stringify({ rules: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    );
    vi.stubGlobal('fetch', fetch);

    await httpAgentApi.listGitHubTriggerRules();
    expect(fetch).toHaveBeenCalledWith('/api/v1/github/rules', expect.any(Object));

    await httpAgentApi.listGitHubTriggerRules('/repo');
    expect(fetch).toHaveBeenCalledWith('/api/v1/github/rules?workspace=%2Frepo', expect.any(Object));
  });

  it('preserves HTTP status and API error code for diagnostics', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { code: 'unauthorized', message: 'a valid bearer token is required' },
    }), { status: 401, headers: { 'Content-Type': 'application/json' } })));

    const error = await httpAgentApi.listSessions().catch((cause: unknown) => cause);
    expect(error).toBeInstanceOf(AgentApiError);
    expect(error).toMatchObject({ status: 401, code: 'unauthorized' });
  });
});
