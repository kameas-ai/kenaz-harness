/**
 * harnessClient.toolExposure.test.ts — tool-context-budget-01TCBUD01 WP02.
 *
 * The typed client methods for the tool-exposure bindings route to the
 * right Settings_/Projects_/Sessions_ binding with their arguments
 * unchanged. Drives the REAL client path (createHarnessClient ->
 * window.go.rpc.Bindings), not createFakeHarnessClient.
 */
import { describe, it, expect, afterEach, vi } from 'vitest';
import { createHarnessClient } from '@/lib/harnessClient';
import type { ToolExposure, ToolExposureSettings } from '@/lib/types';

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
    const settings: ToolExposureSettings = {
      exposure: layer,
      schemaBudgetTokens: 12000,
      activationTtlTurns: 4,
      effectiveSchemaBudgetTokens: 12000,
      effectiveActivationTtlTurns: 4,
    };
    const bindings = {
      Settings_GetToolExposure: vi.fn(async () => settings),
      Settings_SetToolExposure: vi.fn(async () => undefined),
      Projects_GetToolExposure: vi.fn(async () => layer),
      Projects_SetToolExposure: vi.fn(async () => undefined),
      Sessions_GetToolExposure: vi.fn(async () => ({
        exposure: layer,
        activations: [{ name: 'fetch__fetch', server: 'fetch', lastUsedTurn: 2, sticky: true }],
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
    await client.sessions.setToolExposure('s1', layer);
    expect(bindings.Sessions_SetToolExposure).toHaveBeenCalledWith('s1', layer);
  });
});
