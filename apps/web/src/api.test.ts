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

  it('preserves HTTP status and API error code for diagnostics', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { code: 'unauthorized', message: 'a valid bearer token is required' },
    }), { status: 401, headers: { 'Content-Type': 'application/json' } })));

    const error = await httpAgentApi.listSessions().catch((cause: unknown) => cause);
    expect(error).toBeInstanceOf(AgentApiError);
    expect(error).toMatchObject({ status: 401, code: 'unauthorized' });
  });
});
