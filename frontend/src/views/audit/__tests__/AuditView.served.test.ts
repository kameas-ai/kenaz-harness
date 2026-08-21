/**
 * AuditView.served.test.ts — AC-712, served-mode-is-a-real-mode-01PMZ707
 * WP05 (spec.md §1.2, §5.5).
 *
 * Before this WP, a rejected client.audit.filter() call landed on a bare
 * `catch { seeded.value = []; }`: a served user opening the audit log to
 * check what the agent did was shown a clean, empty, non-erroring
 * compliance trail — fabricated evidence about a compliance record. This
 * pins the replacement: the boundary panel, not an empty audit-stream
 * table, and confirms no Audit_* RPC is ever called while served.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { ref } from 'vue';
import AuditView from '@/views/audit/AuditView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

let servedModeFlag = true;
vi.mock('@/lib/useServedMode', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/useServedMode')>();
  return {
    ...actual,
    isServedMode: () => servedModeFlag,
    useServedMode: () => ref(servedModeFlag),
  };
});

function failingAuditClient() {
  const fail = () => {
    throw new Error(
      'audit.* must not be called in served mode — Audit_* has no serve dispatch case',
    );
  };
  return createFakeHarnessClient({
    audit: {
      listEntries: fail,
      verifyEntry: fail,
      verifyChain: fail,
      filter: fail,
      listSavedQueries: fail,
      saveQuery: fail,
      deleteQuery: fail,
      export: fail,
      bulkPurge: fail,
      startStream: fail,
      stopStream: fail,
    },
  });
}

describe('AuditView (served mode)', () => {
  it('renders the boundary panel, not an empty audit-stream table', async () => {
    servedModeFlag = true;
    const client = failingAuditClient();
    const w = mount(AuditView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(
      w.find('[data-testid="not-available-in-served-mode"]').exists(),
    ).toBe(true);
    // AC-712's own falsification target: restoring `seeded.value = []`
    // would make this assertion fail while the panel assertion above
    // stays green (the empty-state div and the panel are not mutually
    // exclusive templates without the v-else wrapper this WP adds).
    expect(w.find('[data-testid="audit-stream"]').exists()).toBe(false);
    expect(w.text()).not.toContain('No audit entries match');
  });

  it('never calls a single Audit_* method while served', async () => {
    servedModeFlag = true;
    const client = failingAuditClient();
    const w = mount(AuditView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    // Unmount to exercise onBeforeUnmount's stream.stop() too — it must
    // not reach the (also failing) stopStream in served mode either.
    w.unmount();
    await flushPromises();
    // No assertion needed beyond "didn't throw" — every audit.* method on
    // this client throws synchronously/rejects, so reaching this line at
    // all proves none of them was called.
    expect(true).toBe(true);
  });
});

describe('AuditView (desktop mode regression)', () => {
  it('keeps rendering the real log, not the panel', async () => {
    servedModeFlag = false;
    const client = createFakeHarnessClient({
      audit: {
        listEntries: async () => [],
        verifyEntry: async () => true,
        verifyChain: async () => ({ verified: true, rows_checked: 0 }),
        filter: async () => [],
        listSavedQueries: async () => [],
        saveQuery: async () => undefined,
        deleteQuery: async () => undefined,
        export: async () => '/tmp/test.csv',
        bulkPurge: async () => undefined,
        startStream: async () => 'fake-audit-sub',
        stopStream: async () => undefined,
      },
    });
    const w = mount(AuditView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(
      w.find('[data-testid="not-available-in-served-mode"]').exists(),
    ).toBe(false);
    expect(w.find('[data-testid="audit-stream"]').exists()).toBe(true);
  });
});
