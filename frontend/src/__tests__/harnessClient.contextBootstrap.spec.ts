/**
 * harnessClient.contextBootstrap.spec.ts
 *
 * Tests the context-bootstrap sub-client + the onboarding runBootstrap method
 * are present on the fake client and seedable.
 * (context-bootstrap-harness-integration WP07)
 */
import { describe, it, expect, vi } from 'vitest';
import { createFakeHarnessClient } from '@/lib/harnessClient';

describe('createFakeHarnessClient — contextBootstrap sub-client', () => {
  it('exposes a contextBootstrap property with the four methods', () => {
    const client = createFakeHarnessClient();
    expect(client.contextBootstrap).toBeDefined();
    expect(typeof client.contextBootstrap.start).toBe('function');
    expect(typeof client.contextBootstrap.status).toBe('function');
    expect(typeof client.contextBootstrap.resume).toBe('function');
    expect(typeof client.contextBootstrap.health).toBe('function');
  });

  it('health resolves to a zero rollup by default', async () => {
    const client = createFakeHarnessClient();
    const h = await client.contextBootstrap.health();
    expect(h.total_nodes).toBe(0);
    expect(h.nodes_by_source_kind).toEqual({});
    expect(h.connected_sources).toEqual([]);
  });

  it('start resolves to an idle result by default', async () => {
    const client = createFakeHarnessClient();
    const r = await client.contextBootstrap.start({ consented_sources: ['gmail'] });
    expect(r.fleet_backed).toBe(false);
    expect(r.run_id).toBe('');
  });

  it('methods can be overridden via seed', async () => {
    const startFn = vi.fn().mockResolvedValue({
      run_id: 'run-1',
      recipe_version: '2.0.0',
      status: 'completed',
      fleet_backed: true,
    });
    const client = createFakeHarnessClient({
      contextBootstrap: {
        start: startFn,
        status: async () => ({ run_id: 'run-1', phase: 'done', connectors: [], total_nodes_written: 5 }),
        resume: async () => ({ run_id: 'run-1', recipe_version: '2.0.0', status: 'running', fleet_backed: true }),
        health: async () => ({ total_nodes: 5, nodes_by_source_kind: { email: 5 }, connected_sources: ['gmail'] }),
      },
    });
    const r = await client.contextBootstrap.start({ consented_sources: ['gmail'] });
    expect(startFn).toHaveBeenCalledWith({ consented_sources: ['gmail'] });
    expect(r.run_id).toBe('run-1');
    const st = await client.contextBootstrap.status();
    expect(st.total_nodes_written).toBe(5);
  });

  it('onboarding exposes runBootstrap', async () => {
    const runFn = vi.fn().mockResolvedValue('run-42');
    const base = createFakeHarnessClient();
    const client = createFakeHarnessClient({
      onboarding: { ...base.onboarding, runBootstrap: runFn },
    });
    const id = await client.onboarding.runBootstrap(['gmail', 'slack']);
    expect(runFn).toHaveBeenCalledWith(['gmail', 'slack']);
    expect(id).toBe('run-42');
  });
});
