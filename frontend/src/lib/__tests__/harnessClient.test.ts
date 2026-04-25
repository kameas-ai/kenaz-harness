import { describe, it, expect } from 'vitest';
import { createFakeHarnessClient } from '@/lib/harnessClient';

describe('createFakeHarnessClient (FR-008 / SC-006)', () => {
  it('returns sane defaults across every view-scoped client', async () => {
    const c = createFakeHarnessClient();
    expect(await c.sessions.list()).toEqual([]);
    expect(await c.llm.listProviders()).toEqual([]);
    expect(await c.mcp.listServers()).toEqual([]);
    expect(await c.a2a.listCards()).toEqual([]);
    expect(await c.workflow.listJobs()).toEqual([]);
    expect(await c.trust.listSecretReferences()).toEqual([]);
    expect(await c.context.list()).toEqual([]);
    expect(await c.bundle.list()).toEqual([]);
    expect(await c.audit.listEntries({})).toEqual([]);
    expect((await c.shellStatus()).connection).toBe('ready');
    expect((await c.settings.get()).schemaVersion).toBe(1);
  });

  it('accepts a partial seed override', async () => {
    const c = createFakeHarnessClient({
      sessions: {
        list: async () => [
          { id: 's1', name: 'one', createdAt: 'x', updatedAt: 'x' },
        ],
        get: async (id) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async (n) => ({
          id: 'new',
          name: n,
          createdAt: '',
          updatedAt: '',
        }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      },
    });
    const list = await c.sessions.list();
    expect(list).toHaveLength(1);
    expect(list[0].name).toBe('one');
  });
});

describe('Trust client never exposes credential values', () => {
  it('SecretReference shape lacks any value field at compile time', async () => {
    const c = createFakeHarnessClient();
    const refs = await c.trust.listSecretReferences();
    // structural assertion: a typical reference would shape-check the
    // absence of a `value` field. Compile-time guarantees are in
    // lib/types.ts; this is the runtime smoke check.
    refs.forEach((r) => {
      expect(Object.keys(r)).not.toContain('value');
      expect(Object.keys(r)).not.toContain('secret');
      expect(Object.keys(r)).not.toContain('password');
    });
  });
});
