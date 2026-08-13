import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ConfirmToolModal from '@/components/chat/ConfirmToolModal.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { ToolConfirmPending } from '@/lib/types';
import { setConnectionState } from '@/lib/useConnectionState';

/**
 * ConfirmToolModal tests (confirm-each-enforcement-01PMAG05 WP02/WP03).
 *
 * These replace an earlier suite that exercised a dialog wired to a
 * topic nothing published (`llm:tool-confirm-request`) and a binding
 * that answered "confirm-each is retired" on every call. It passed. That
 * is the point: a UI test that mocks the client can prove a button calls
 * a method without proving the method does anything, so the whole
 * surface stayed green while confirming nothing.
 *
 * What is asserted here is the part that is testable from the frontend
 * and that MATTERS given the backend contract: a `confirm_each` tool
 * call is parked server-side with no deadline, so every exit from this
 * dialog must call something. The dismissal assertions below exist
 * because a dialog that unmounts without calling cancelBatch leaves a
 * blocked goroutine behind.
 */

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  emit: (topic: string, payload: unknown) => void;
  handlers: Map<string, Set<(payload: unknown) => void>>;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
    handlers,
    EventsOn: (topic, cb) => {
      let s = handlers.get(topic);
      if (!s) {
        s = new Set();
        handlers.set(topic, s);
      }
      s.add(cb);
      return () => s!.delete(cb);
    },
    emit: (topic, payload) => {
      const s = handlers.get(topic);
      if (!s) return;
      for (const cb of s) cb(payload);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

function uninstallRuntime() {
  delete (window as unknown as { runtime?: unknown }).runtime;
}

/** A recording confirm client, so each test can assert the exact call. */
function spyConfirm(overrides: Partial<HarnessClient['confirm']> = {}) {
  return {
    resolve: vi.fn(async () => undefined),
    resolveAlways: vi.fn(async () => undefined),
    approveBatch: vi.fn(async () => 0),
    cancelBatch: vi.fn(async () => 0),
    listPending: vi.fn(async () => [] as ToolConfirmPending[]),
    ...overrides,
  };
}

function mountModal(
  confirm: Partial<HarnessClient['confirm']> = {},
  opts: { activeSessionId?: string; sessions?: Partial<HarnessClient['sessions']> } = {},
) {
  const client = spyConfirm(confirm);
  const seed: Partial<HarnessClient> = { confirm: client };
  if (opts.sessions) {
    seed.sessions = opts.sessions as HarnessClient['sessions'];
  }
  const w = mount(ConfirmToolModal, {
    attachTo: document.body,
    props: { activeSessionId: opts.activeSessionId ?? 'sess-1' },
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, seed);
          },
        },
      ],
    },
  });
  return { w, confirm: client };
}

function row(overrides: Partial<ToolConfirmPending> = {}): ToolConfirmPending {
  return {
    session_id: 'sess-1',
    call_id: 'call-1',
    batch_id: 'batch-1',
    server: 'github',
    tool: 'create_issue',
    args_summary: '2 arguments: body (string), title (string)',
    ...overrides,
  };
}

function q(testid: string): HTMLElement | null {
  return document.body.querySelector(`[data-testid="${testid}"]`);
}

function emit(payload: ToolConfirmPending) {
  (window as unknown as { runtime: FakeRuntime }).runtime.emit(
    'tool:confirm-pending',
    payload,
  );
}

describe('ConfirmToolModal', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
    document.body.innerHTML = '';
  });

  it('renders nothing when nothing is parked', async () => {
    const { w } = mountModal();
    await flushPromises();
    expect(q('confirm-tool-modal')).toBeNull();
    expect(q('confirm-tool-waiting-pill')).toBeNull();
    w.unmount();
  });

  it('renders one dialog with one row per parked call in the batch', async () => {
    const { w } = mountModal();
    await flushPromises();
    emit(row({ call_id: 'c1', tool: 'create_issue' }));
    emit(row({ call_id: 'c2', tool: 'delete_branch' }));
    await flushPromises();

    // ONE dialog (owner decision 3: no modal storm)…
    expect(document.body.querySelectorAll('[data-testid="confirm-tool-modal"]')).toHaveLength(1);
    // …with a row per call.
    expect(q('confirm-tool-row-c1')).not.toBeNull();
    expect(q('confirm-tool-row-c2')).not.toBeNull();
    expect(document.body.textContent).toContain('github.create_issue');
    expect(document.body.textContent).toContain('github.delete_branch');
    w.unmount();
  });

  it('shows the structural args summary verbatim and never fabricates values', async () => {
    const { w } = mountModal();
    await flushPromises();
    emit(row({ call_id: 'c1', args_summary: '1 argument: path (string)' }));
    await flushPromises();

    expect(q('confirm-tool-args-c1')!.textContent).toContain('1 argument: path (string)');
    w.unmount();
  });

  it('approves a single row with rememberSession=false by default', async () => {
    const { w, confirm } = mountModal();
    await flushPromises();
    emit(row({ call_id: 'c1' }));
    await flushPromises();

    q('confirm-tool-approve-c1')!.click();
    await flushPromises();

    expect(confirm.resolve).toHaveBeenCalledWith(
      'sess-1',
      'c1',
      true,
      'approved by user',
      false,
    );
    expect(q('confirm-tool-modal')).toBeNull();
    w.unmount();
  });

  it('carries the per-row "allow for this session" tick into the approval', async () => {
    const { w, confirm } = mountModal();
    await flushPromises();
    emit(row({ call_id: 'c1' }));
    await flushPromises();

    const box = q('confirm-tool-remember-c1') as HTMLInputElement;
    box.checked = true;
    box.dispatchEvent(new Event('change'));
    await flushPromises();

    q('confirm-tool-approve-c1')!.click();
    await flushPromises();

    expect(confirm.resolve).toHaveBeenCalledWith(
      'sess-1',
      'c1',
      true,
      'approved by user',
      true,
    );
    w.unmount();
  });

  it('denies a single row without approving the rest of the batch', async () => {
    const { w, confirm } = mountModal();
    await flushPromises();
    emit(row({ call_id: 'c1' }));
    emit(row({ call_id: 'c2' }));
    await flushPromises();

    q('confirm-tool-deny-c1')!.click();
    await flushPromises();

    expect(confirm.resolve).toHaveBeenCalledWith('sess-1', 'c1', false, 'denied by user', false);
    // c2 is still parked and still on screen — rows resolve independently.
    expect(q('confirm-tool-row-c2')).not.toBeNull();
    expect(confirm.cancelBatch).not.toHaveBeenCalled();
    w.unmount();
  });

  it('requires two clicks before persisting an "always allow" rule', async () => {
    const { w, confirm } = mountModal();
    await flushPromises();
    emit(row({ call_id: 'c1' }));
    await flushPromises();

    // First click arms and explains; it must NOT write anything.
    q('confirm-tool-always-c1')!.click();
    await flushPromises();
    expect(confirm.resolveAlways).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain('saved until revoked');

    // Second click commits.
    q('confirm-tool-always-c1')!.click();
    await flushPromises();
    expect(confirm.resolveAlways).toHaveBeenCalledWith(
      'sess-1',
      'c1',
      'always allow (persisted)',
    );
    w.unmount();
  });

  it('approve-all resolves the whole batch', async () => {
    const { w, confirm } = mountModal();
    await flushPromises();
    emit(row({ call_id: 'c1' }));
    emit(row({ call_id: 'c2' }));
    await flushPromises();

    q('confirm-tool-approve-all')!.click();
    await flushPromises();

    expect(confirm.approveBatch).toHaveBeenCalledWith('batch-1', false);
    expect(q('confirm-tool-modal')).toBeNull();
    w.unmount();
  });

  it('dismissal denies the whole batch — it never approves and never just hides', async () => {
    const { w, confirm } = mountModal();
    await flushPromises();
    emit(row({ call_id: 'c1' }));
    emit(row({ call_id: 'c2' }));
    await flushPromises();

    q('confirm-tool-dismiss')!.click();
    await flushPromises();

    expect(confirm.cancelBatch).toHaveBeenCalledWith('batch-1', 'dismissed by user');
    expect(confirm.approveBatch).not.toHaveBeenCalled();
    // FR-003: no approval slipped through the dismissal path.
    expect(confirm.resolve).not.toHaveBeenCalled();
    w.unmount();
  });

  it('Escape dismisses (and therefore denies) rather than hiding a blocked run', async () => {
    const { w, confirm } = mountModal();
    await flushPromises();
    emit(row({ call_id: 'c1' }));
    await flushPromises();

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await flushPromises();

    expect(confirm.cancelBatch).toHaveBeenCalledWith('batch-1', 'dismissed by user');
    w.unmount();
  });

  it('"decide later" leaves the pause intact and offers a way back', async () => {
    const { w, confirm } = mountModal();
    await flushPromises();
    emit(row({ call_id: 'c1' }));
    await flushPromises();

    q('confirm-tool-decide-later')!.click();
    await flushPromises();

    // Nothing was resolved — the run is still parked.
    expect(confirm.resolve).not.toHaveBeenCalled();
    expect(confirm.cancelBatch).not.toHaveBeenCalled();
    expect(q('confirm-tool-modal')).toBeNull();
    // …and the user can find it again.
    const pill = q('confirm-tool-waiting-pill');
    expect(pill).not.toBeNull();
    pill!.click();
    await flushPromises();
    expect(q('confirm-tool-modal')).not.toBeNull();
    w.unmount();
  });

  it('rebuilds the dialog from listPending on mount (reload / reconnect)', async () => {
    const { w } = mountModal({
      listPending: vi.fn(async () => [row({ call_id: 'survivor' })]),
    });
    await flushPromises();

    expect(q('confirm-tool-row-survivor')).not.toBeNull();
    w.unmount();
  });

  it('dedups a row delivered by both the snapshot and the live topic', async () => {
    const { w } = mountModal({
      listPending: vi.fn(async () => [row({ call_id: 'dup' })]),
    });
    await flushPromises();
    emit(row({ call_id: 'dup' }));
    await flushPromises();

    expect(
      document.body.querySelectorAll('[data-testid="confirm-tool-row-dup"]'),
    ).toHaveLength(1);
    w.unmount();
  });

  it('shows only the oldest batch and counts the rest', async () => {
    const { w } = mountModal();
    await flushPromises();
    emit(row({ call_id: 'a1', batch_id: 'batch-A' }));
    emit(row({ call_id: 'b1', batch_id: 'batch-B' }));
    await flushPromises();

    expect(q('confirm-tool-row-a1')).not.toBeNull();
    expect(q('confirm-tool-row-b1')).toBeNull();
    expect(q('confirm-tool-queue-badge')!.textContent).toContain('+1');
    w.unmount();
  });

  it('surfaces a rejected resolve and keeps the dialog open to retry', async () => {
    const { w } = mountModal({
      resolve: vi.fn(async () => {
        throw new Error('already answered');
      }),
    });
    await flushPromises();
    emit(row({ call_id: 'c1' }));
    await flushPromises();

    q('confirm-tool-approve-c1')!.click();
    await flushPromises();

    expect(q('confirm-tool-error')!.textContent).toContain('already answered');
    expect(q('confirm-tool-modal')).not.toBeNull();
    w.unmount();
  });
});

describe('ConfirmToolModal — session attribution (F3)', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
    document.body.innerHTML = '';
  });

  it('labels every row with its session', async () => {
    const { w } = mountModal({}, { activeSessionId: 'sess-1' });
    await flushPromises();
    emit(row({ call_id: 'c1', session_id: 'sess-1' }));
    await flushPromises();

    const label = q('confirm-tool-session-c1');
    expect(label).not.toBeNull();
    expect(label!.textContent).toContain('sess-1');
    w.unmount();
  });

  // The substance of the finding: a background session asking to run
  // write_file reads as the chat in front of you unless the row says
  // otherwise, and you approve it on that misreading.
  it('marks rows that belong to a session other than the active one', async () => {
    const { w } = mountModal({}, { activeSessionId: 'sess-front' });
    await flushPromises();
    emit(row({ call_id: 'mine', session_id: 'sess-front' }));
    emit(row({ call_id: 'theirs', session_id: 'sess-background' }));
    await flushPromises();

    expect(q('confirm-tool-row-mine')!.getAttribute('data-foreign-session')).toBe('false');
    expect(q('confirm-tool-foreign-mine')).toBeNull();

    expect(q('confirm-tool-row-theirs')!.getAttribute('data-foreign-session')).toBe('true');
    expect(q('confirm-tool-foreign-theirs')).not.toBeNull();
    expect(q('confirm-tool-session-theirs')!.textContent).toContain('other session');
    w.unmount();
  });

  // Attribution is the fix, NOT filtering: a foreign row still has a
  // goroutine blocked on it, so hiding it would strand the run.
  it('still renders and can answer a foreign-session row', async () => {
    const { w, confirm } = mountModal({}, { activeSessionId: 'sess-front' });
    await flushPromises();
    emit(row({ call_id: 'theirs', session_id: 'sess-background' }));
    await flushPromises();

    expect(q('confirm-tool-row-theirs')).not.toBeNull();
    q('confirm-tool-approve-theirs')!.click();
    await flushPromises();
    expect(confirm.resolve).toHaveBeenCalledWith(
      'sess-background',
      'theirs',
      true,
      'approved by user',
      false,
    );
    w.unmount();
  });

  it('prefers the session name when one can be looked up', async () => {
    const { w } = mountModal(
      {},
      {
        activeSessionId: 'sess-front',
        sessions: {
          list: vi.fn(async () => [
            { id: 'sess-background', name: 'Nightly refactor' },
          ]),
        } as unknown as Partial<HarnessClient['sessions']>,
      },
    );
    await flushPromises();
    emit(row({ call_id: 'theirs', session_id: 'sess-background' }));
    await flushPromises();
    await flushPromises();

    expect(q('confirm-tool-session-theirs')!.textContent).toContain('Nightly refactor');
    w.unmount();
  });

  // A failed lookup must degrade to a short id, never to no attribution.
  it('falls back to a short id when the session list is unavailable', async () => {
    const { w } = mountModal(
      {},
      {
        activeSessionId: 'sess-front',
        sessions: {
          list: vi.fn(async () => {
            throw new Error('offline');
          }),
        } as unknown as Partial<HarnessClient['sessions']>,
      },
    );
    await flushPromises();
    emit(row({ call_id: 'theirs', session_id: '01HFXY8B5VJ6T6T7AXJF9JT9F1' }));
    await flushPromises();
    await flushPromises();

    const label = q('confirm-tool-session-theirs')!.textContent ?? '';
    expect(label).toContain('01HFXY8B');
    expect(q('confirm-tool-foreign-theirs')).not.toBeNull();
    w.unmount();
  });
});

describe('ConfirmToolModal — arming hygiene (F4)', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
    document.body.innerHTML = '';
  });

  // An armed "always allow" is a loaded second click. If the user's next
  // act is to reach for the session checkbox instead, they have changed
  // their mind and the durable control must not stay one click from
  // firing.
  it('ticking "allow for this session" disarms a pending always-allow', async () => {
    const { w, confirm } = mountModal();
    await flushPromises();
    emit(row({ call_id: 'c1' }));
    await flushPromises();

    q('confirm-tool-always-c1')!.click();
    await flushPromises();
    expect(document.body.textContent).toContain('saved until revoked');

    const box = q('confirm-tool-remember-c1') as HTMLInputElement;
    box.checked = true;
    box.dispatchEvent(new Event('change'));
    await flushPromises();

    // Disarmed: the label is back to the un-armed wording…
    expect(document.body.textContent).not.toContain('saved until revoked');
    // …and the next click re-arms rather than committing.
    q('confirm-tool-always-c1')!.click();
    await flushPromises();
    expect(confirm.resolveAlways).not.toHaveBeenCalled();
    w.unmount();
  });

  it('only one row can be armed at a time', async () => {
    const { w, confirm } = mountModal();
    await flushPromises();
    emit(row({ call_id: 'c1' }));
    emit(row({ call_id: 'c2' }));
    await flushPromises();

    q('confirm-tool-always-c1')!.click();
    await flushPromises();
    q('confirm-tool-always-c2')!.click();
    await flushPromises();

    // Arming c2 disarmed c1, so a second click on c1 only re-arms it.
    q('confirm-tool-always-c1')!.click();
    await flushPromises();
    expect(confirm.resolveAlways).not.toHaveBeenCalled();
    w.unmount();
  });

  it('denying a row disarms a pending always-allow on it', async () => {
    const { w, confirm } = mountModal();
    await flushPromises();
    emit(row({ call_id: 'c1' }));
    emit(row({ call_id: 'c2' }));
    await flushPromises();

    q('confirm-tool-always-c1')!.click();
    await flushPromises();
    q('confirm-tool-deny-c2')!.click();
    await flushPromises();

    q('confirm-tool-always-c1')!.click();
    await flushPromises();
    expect(confirm.resolveAlways).not.toHaveBeenCalled();
    w.unmount();
  });
});

describe('ConfirmToolModal — reconciliation is a set diff (finding 5)', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
    document.body.innerHTML = '';
  });

  // A row answered in another window is gone server-side. Union-only
  // reconciliation would render a button for it forever.
  it('drops rows the server no longer reports as parked', async () => {
    const listPending = vi
      .fn(async (): Promise<ToolConfirmPending[]> => [])
      .mockResolvedValueOnce([row({ call_id: 'a' }), row({ call_id: 'b' })])
      .mockResolvedValue([row({ call_id: 'b' })]);
    const { w } = mountModal({ listPending });
    await flushPromises();
    expect(q('confirm-tool-row-a')).not.toBeNull();

    // A second reconcile — as happens after an already-resolved click.
    await (w.vm as unknown as { reconcile: () => Promise<void> }).reconcile();
    await flushPromises();

    expect(q('confirm-tool-row-a')).toBeNull();
    expect(q('confirm-tool-row-b')).not.toBeNull();
    w.unmount();
  });

  // "I could not reach the harness" must never read as "nothing is
  // parked" — that would hide a live confirmation.
  it('leaves the queue alone when listPending fails', async () => {
    const listPending = vi
      .fn(async (): Promise<ToolConfirmPending[]> => [])
      .mockResolvedValueOnce([row({ call_id: 'a' })])
      .mockRejectedValue(new Error('offline'));
    const { w } = mountModal({ listPending });
    await flushPromises();
    expect(q('confirm-tool-row-a')).not.toBeNull();

    await (w.vm as unknown as { reconcile: () => Promise<void> }).reconcile();
    await flushPromises();

    expect(q('confirm-tool-row-a')).not.toBeNull();
    w.unmount();
  });

  // An already-resolved row is not a failure to report to the user.
  it('drops the row silently when the server says it is already resolved', async () => {
    const { w } = mountModal({
      resolve: vi.fn(async () => {
        throw new Error('toolloop: unknown or already-resolved confirmation');
      }),
    });
    await flushPromises();
    emit(row({ call_id: 'c1' }));
    await flushPromises();

    q('confirm-tool-approve-c1')!.click();
    await flushPromises();

    expect(q('confirm-tool-error')).toBeNull();
    expect(q('confirm-tool-row-c1')).toBeNull();
    w.unmount();
  });
});
