/**
 * harnessClient.arrayNullSafety.test.ts
 *
 * Regression coverage for the SURFACE ERROR crash observed in a running
 * `wails dev` session:
 *
 *   null is not an object (evaluating 'tasks.value.filter')
 *
 * Root cause: Wails marshals a Go `nil` slice to JSON `null`, not `[]`.
 * WailsBindingsLike declares every list binding as `Promise<T[]>` — never
 * `T[] | null` — so a Go implementation that takes an early-return-nil
 * path (task registry not wired, no task matches the session, fleet
 * syncer offline, ...) delivers `null` to a `ref<T[]>` with zero
 * compile-time signal. The first array method the view calls on it
 * throws.
 *
 * These tests drive the REAL client path — createHarnessClient() ->
 * wailsBindings() -> window.go.rpc.Bindings — not
 * createFakeHarnessClient(), which builds its stub directly from
 * hand-written arrays and therefore never exercises the Wails
 * null-marshaling behaviour at all. A fixture built on the fake would
 * pass whether or not the adapter normalises anything (see
 * BackgroundTaskChip.spec.ts / TasksPanel.spec.ts, which both mock
 * Tasks_List(By Session) via createFakeHarnessClient and consequently
 * never caught this).
 */
import { describe, it, expect, afterEach } from 'vitest';
import { createHarnessClient } from '@/lib/harnessClient';

type WindowWithGo = { go: { rpc: { Bindings: unknown } } };

// The real (private, unexported) WailsBindingsLike interface has 400+
// required members; these tests only ever stub the one or two methods
// each case exercises, so the raw fixture is intentionally partial and
// the window global is accessed through a loose local shape (cast via
// `unknown`, bypassing the ambient WailsBindingsLike-typed global)
// rather than through `typeof window`, which would still force the
// full interface via intersection.
function rawGo(): WindowWithGo {
  return window as unknown as WindowWithGo;
}

function setRawBindings(bindings: unknown): void {
  rawGo().go.rpc.Bindings = bindings;
}

describe('createHarnessClient() — array-returning bindings survive a Go nil slice', () => {
  const originalBindings: unknown = rawGo().go?.rpc?.Bindings;

  afterEach(() => {
    setRawBindings(originalBindings);
  });

  it('Tasks_ListBySession: null resolves to [] instead of crashing BackgroundTaskChip-style .filter', async () => {
    setRawBindings({
      Tasks_ListBySession: async (_sessionId: string) => null,
    });
    const client = createHarnessClient();
    const tasks = await client.Tasks_ListBySession('sess-1');
    expect(tasks).toEqual([]);
    // BackgroundTaskChip.vue:48 — tasks.value.filter(t => t.status === 'running')
    expect(() => (tasks as Array<{ status: string }>).filter((t) => t.status === 'running')).not.toThrow();
  });

  it('Tasks_List: null resolves to [] instead of crashing TasksPanel-style .filter', async () => {
    setRawBindings({
      Tasks_List: async () => null,
    });
    const client = createHarnessClient();
    const tasks = await client.Tasks_List();
    expect(tasks).toEqual([]);
    // TasksPanel.vue:53/57 — tasks.value.filter(...)
    expect(() => (tasks as Array<{ status: string }>).filter((t) => t.status === 'running')).not.toThrow();
  });

  it('Unit_ListConflicts: null resolves to [] (fleet syncer offline path)', async () => {
    setRawBindings({
      Unit_ListConflicts: async () => null,
    });
    const client = createHarnessClient();
    const conflicts = await client.Unit_ListConflicts();
    expect(conflicts).toEqual([]);
  });

  it('Search_Unified: null resolves to [] (search-disabled short circuit)', async () => {
    setRawBindings({
      Search_Unified: async (_q: string, _f: unknown) => null,
    });
    const client = createHarnessClient();
    const hits = await client.search.unified('query', {});
    expect(hits).toEqual([]);
  });

  it('Shell_PathComplete: null resolves to [] (empty "@" token path)', async () => {
    setRawBindings({
      Shell_PathComplete: async (_partial: string) => null,
    });
    const client = createHarnessClient();
    const opts = await client.shell.pathComplete('');
    expect(opts).toEqual([]);
  });

  it('a non-array-returning binding is left untouched by the proxy', async () => {
    setRawBindings({
      AppInfo: async () => ({ version: '1.2.3' }),
    });
    const client = createHarnessClient();
    const info = await client.appInfo();
    expect(info).toEqual({ version: '1.2.3' });
  });

  it('a real (non-null) array result passes through unchanged', async () => {
    setRawBindings({
      Tasks_List: async () => [{ id: 't1' }],
    });
    const client = createHarnessClient();
    const tasks = await client.Tasks_List();
    expect(tasks).toEqual([{ id: 't1' }]);
  });
});
