/**
 * harnessClient.toolExposure.test.ts — tool-context-budget-01TCBUD01 WP02/WP03.
 *
 * The typed client methods for the tool-exposure bindings route to the
 * right Settings_/Projects_/Sessions_ binding with their arguments
 * unchanged. Drives the REAL client path (createHarnessClient ->
 * window.go.rpc.Bindings), not createFakeHarnessClient.
 */
import { describe, it, expect, afterEach, vi } from 'vitest';
import { createHarnessClient } from '@/lib/harnessClient';
import type { ToolExposure, ToolExposureOrg, ToolExposureSettings } from '@/lib/types';

type WindowWithGo = { go: { rpc: { Bindings: unknown } } };

function rawGo(): WindowWithGo {
  return window as unknown as WindowWithGo;
}

describe('createHarnessClient() — tool exposure', () => {
  const originalBindings: unknown = rawGo().go?.rpc?.Bindings;

  afterEach(() => {
    rawGo().go.rpc.Bindings = originalBindings;
  });

  it('routes every typed method to its binding', async () => {
    const layer: ToolExposure = {
      servers: { outlook: { tier: 'summary', tools: { 'send-mail': 'full' } } },
    };
    // WP07: the organisation's entries ride along read-only.
    const org: ToolExposureOrg = {
      settings: [
        { server: 'fetch', tier: 'full', pinned: false },
        { server: 'outlook', tier: 'off', pinned: true, pinnedBy: 'org' },
      ],
      schemaBudgetTokens: 8000,
      bundleId: 42,
    };
    const settings: ToolExposureSettings = {
      exposure: layer,
      schemaBudgetTokens: 12000,
      activationTtlTurns: 4,
      effectiveSchemaBudgetTokens: 8000,
      effectiveActivationTtlTurns: 4,
      org,
    };
    const bindings = {
      Settings_GetToolExposure: vi.fn(async () => settings),
      Settings_SetToolExposure: vi.fn(async () => undefined),
      Projects_GetToolExposure: vi.fn(async () => layer),
      Projects_SetToolExposure: vi.fn(async () => undefined),
      Sessions_GetToolExposure: vi.fn(async () => ({
        exposure: layer,
        activations: [{ name: 'fetch__fetch', server: 'fetch', lastUsedTurn: 2, sticky: true }],
        org,
      })),
      Sessions_SetToolExposure: vi.fn(async () => undefined),
    };
    rawGo().go.rpc.Bindings = bindings;
    const client = createHarnessClient();

    expect(await client.settings.getToolExposure()).toEqual(settings);
    await client.settings.setToolExposure(settings);
    expect(bindings.Settings_SetToolExposure).toHaveBeenCalledWith(settings);

    expect(await client.projects.getToolExposure('p1')).toEqual(layer);
    expect(bindings.Projects_GetToolExposure).toHaveBeenCalledWith('p1');
    await client.projects.setToolExposure('p1', layer);
    expect(bindings.Projects_SetToolExposure).toHaveBeenCalledWith('p1', layer);

    const s = await client.sessions.getToolExposure('s1');
    expect(bindings.Sessions_GetToolExposure).toHaveBeenCalledWith('s1');
    expect(s.activations[0].name).toBe('fetch__fetch');
    expect(s.org.settings[1]).toEqual({ server: 'outlook', tier: 'off', pinned: true, pinnedBy: 'org' });
    await client.sessions.setToolExposure('s1', layer);
    expect(bindings.Sessions_SetToolExposure).toHaveBeenCalledWith('s1', layer);
  });

  it('routes sessions.loadTools to Sessions_LoadTools and returns its result', async () => {
    const result = {
      loaded: ['outlook__list-messages', 'outlook__send-mail'],
      loaded_by_server: { outlook: 2 },
      not_loaded: [{ name: 'github', reason: 'server github is not running (state: failed)' }],
      summary: 'loaded 2 tool(s); their definitions are sent on your next call; 1 could not be loaded (see not_loaded)',
    };
    const bindings = {
      Sessions_LoadTools: vi.fn(async () => result),
    };
    rawGo().go.rpc.Bindings = bindings;
    const client = createHarnessClient();

    const got = await client.sessions.loadTools('s1', ['outlook', 'github'], ['fetch__*'], true);
    expect(bindings.Sessions_LoadTools).toHaveBeenCalledWith(
      's1',
      ['outlook', 'github'],
      ['fetch__*'],
      true,
    );
    expect(got).toEqual(result);
    expect(got.not_loaded[0].reason).toContain('not running');
  });
});
