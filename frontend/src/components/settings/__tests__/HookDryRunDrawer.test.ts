/**
 * HookDryRunDrawer.test.ts
 *
 * AC-052 (controls-and-readouts-that-tell-the-truth-01PMZ808 WP20 /
 * UNIT-15): the dry-run drawer must render `permissionDecision` and
 * `watchPaths` from the per-hook HookOutput when present. Before WP20 the
 * drawer rendered only the merged decision badge and additionalContext —
 * despite its own docstring promising item 4's "permission decision" and
 * despite the Go mapper (core/rpc/views/hooks/dry_run.go:27-38) always
 * marshalling both fields onto HookOutput.
 *
 * The drawer's root is `<Teleport to="body">` (BaseDialog.spec.ts sets the
 * precedent for testing this shape: attachTo: document.body +
 * stubs: { Teleport: false }, then find teleported nodes via
 * document.body rather than the wrapper root — Teleport moves them
 * outside the wrapper's own element).
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, flushPromises, DOMWrapper } from '@vue/test-utils';

import HookDryRunDrawer from '@/components/settings/HookDryRunDrawer.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Hook, DryRunResult } from '@/lib/types';

// NOTE: `Hook.event` is typed `HookEvent` (lib/types.ts), a 4-value union
// that still only covers the original v1 events — it hasn't caught up
// with `HookEventName`'s 18. Not this WP's finding; use a value in the
// narrower type like the sibling HooksPanel.spec.ts fixture does. The
// dry-run results below are constructed independently of this value.
const FAKE_HOOK: Hook = {
  id: 'hk-test',
  name: 'Test hook',
  event: 'pre_send',
  kind: 'shell',
  enabled: true,
  match: {},
  command: 'echo {}',
};

function mountDrawer(dryRunResult: DryRunResult) {
  const client = createFakeHarnessClient({
    hooks: {
      list: async () => [FAKE_HOOK],
      get: async (id) => ({ ...FAKE_HOOK, id }),
      add: async (h) => h,
      update: async () => undefined,
      remove: async () => undefined,
      availableBuiltins: async () => [],
      installStarterMemory: async () => undefined,
      removeStarterMemory: async () => undefined,
      dryRun: vi.fn(async () => dryRunResult),
    },
  });
  mount(HookDryRunDrawer, {
    props: { hook: FAKE_HOOK, open: true },
    attachTo: document.body,
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: { Teleport: false },
    },
  });
  return new DOMWrapper(document.body);
}

async function fire(body: DOMWrapper<HTMLElement>) {
  await body.find('[data-testid="hook-dry-run-fire"]').trigger('click');
  await flushPromises();
}

describe('HookDryRunDrawer — AC-052 permissionDecision + watchPaths', () => {
  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('renders permissionDecision and its reason when the hook output sets one', async () => {
    const result: DryRunResult = {
      output: {
        decision: 'approve',
        permissionDecision: 'deny',
        permissionDecisionReason: 'destructive command',
      },
      merged: { blocked: false, permissionDenied: true, permissionAllowed: false },
      exitCode: 0,
      latencyMs: 3,
    };
    const body = mountDrawer(result);
    await fire(body);

    const el = body.find('[data-testid="hook-dry-run-permission-decision"]');
    expect(el.exists()).toBe(true);
    expect(el.text()).toContain('DENY');
    expect(el.text()).toContain('destructive command');
  });

  it('does not render the permissionDecision block when the hook set none', async () => {
    const result: DryRunResult = {
      output: { decision: 'approve' },
      merged: { blocked: false, permissionDenied: false, permissionAllowed: false },
      exitCode: 0,
      latencyMs: 3,
    };
    const body = mountDrawer(result);
    await fire(body);
    expect(body.find('[data-testid="hook-dry-run-permission-decision"]').exists()).toBe(false);
  });

  it('renders watchPaths as a list when the hook output sets any', async () => {
    const result: DryRunResult = {
      output: { decision: 'approve', watchPaths: ['/repo/src/a.ts', '/repo/src/b.ts'] },
      merged: { blocked: false, permissionDenied: false, permissionAllowed: false },
      exitCode: 0,
      latencyMs: 3,
    };
    const body = mountDrawer(result);
    await fire(body);

    const el = body.find('[data-testid="hook-dry-run-watch-paths"]');
    expect(el.exists()).toBe(true);
    const items = el.findAll('li');
    expect(items.map((i) => i.text())).toEqual(['/repo/src/a.ts', '/repo/src/b.ts']);
  });

  it('does not render the watchPaths block when the hook output has none', async () => {
    const result: DryRunResult = {
      output: { decision: 'approve' },
      merged: { blocked: false, permissionDenied: false, permissionAllowed: false },
      exitCode: 0,
      latencyMs: 3,
    };
    const body = mountDrawer(result);
    await fire(body);
    expect(body.find('[data-testid="hook-dry-run-watch-paths"]').exists()).toBe(false);
  });
});
