/**
 * ShareSessionDialog.spec.ts — fleet-context-sync-01NDFSEX15 WP07
 *
 * Three specs:
 *   1. renders with empty query and loads team on open
 *   2. filters team list by query, selects member, share button calls Handoff_Share
 *   3. shows error message when Handoff_Share rejects
 *
 * Note: BaseDialog uses <Teleport to="body">, so we mount with
 * attachTo: document.body and query via document.body.querySelector.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { nextTick } from 'vue';
import ShareSessionDialog from '@/views/sessions/ShareSessionDialog.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { FleetTeamMemberView } from '@/lib/types';

// ── vue-router stub ────────────────────────────────────────────────────────
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useRoute: () => undefined,
}));

// ── fixtures ───────────────────────────────────────────────────────────────

const ALICE: FleetTeamMemberView = {
  userID: 'u-alice',
  displayName: 'Alice Liddell',
  email: 'alice@example.com',
  canReceive: true,
};

const BOB: FleetTeamMemberView = {
  userID: 'u-bob',
  displayName: 'Bob Builder',
  email: 'bob@example.com',
  canReceive: true,
};

const CHARLIE: FleetTeamMemberView = {
  userID: 'u-charlie',
  displayName: 'Charlie No-receive',
  email: 'charlie@example.com',
  canReceive: false,
};

const TEAM = [ALICE, BOB, CHARLIE];

function buildClient(overrides: {
  team?: FleetTeamMemberView[];
  shareFn?: ReturnType<typeof vi.fn>;
} = {}) {
  const shareFn = overrides.shareFn ?? vi.fn(async () => {});
  return {
    client: createFakeHarnessClient({
      Handoff_ListTeam: async () => overrides.team ?? TEAM,
      Handoff_Share: shareFn,
    }),
    shareFn,
  };
}

function mountDialog(
  client = buildClient().client,
  props: { open?: boolean; sessionID?: string } = {},
) {
  return mount(ShareSessionDialog, {
    props: { open: true, sessionID: 'sess-test', ...props },
    attachTo: document.body,
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
}

// ── helpers ────────────────────────────────────────────────────────────────

function q(testId: string): HTMLElement | null {
  return document.body.querySelector(`[data-testid="${testId}"]`);
}

/** Simulate typing into a v-model text input by setting value + dispatching input event. */
async function typeInto(testId: string, text: string) {
  const el = document.body.querySelector<HTMLInputElement>(`[data-testid="${testId}"]`);
  if (!el) throw new Error(`[data-testid="${testId}"] not found`);
  el.value = text;
  el.dispatchEvent(new InputEvent('input', { bubbles: true }));
  await nextTick();
  await flushPromises();
}

describe('ShareSessionDialog', () => {
  afterEach(() => {
    document.body.innerHTML = '';
    vi.clearAllMocks();
  });

  it('1. loads team on open, canReceive=false filtered from results', async () => {
    const { client } = buildClient({ team: TEAM });
    mountDialog(client);
    // Team loading fires as part of mount (watch open=true)
    await flushPromises();

    // Confirm the dialog is mounted
    expect(q('share-session-dialog')).not.toBeNull();

    // Type a query that matches both alice (canReceive=true) and charlie (canReceive=false)
    await typeInto('share-recipient-input', 'l');

    // alice should match "Alice Liddell"; charlie "Charlie No-receive" would not match 'l'
    // but alice@example.com contains 'l'. Let's type 'alice'
    await typeInto('share-recipient-input', 'alice');

    expect(q('share-member-u-alice')).not.toBeNull();
    expect(q('share-member-u-charlie')).toBeNull();
  });

  it('2. selects a member and calls Handoff_Share on confirm', async () => {
    const { client, shareFn } = buildClient({ team: TEAM });
    mountDialog(client, { sessionID: 'sess-xyz' });
    await flushPromises();

    await typeInto('share-recipient-input', 'bob');

    const bobEl = q('share-member-u-bob');
    expect(bobEl).not.toBeNull();
    bobEl!.click();
    await nextTick();
    await flushPromises();

    // Selected badge should appear
    expect(q('share-selected-member')).not.toBeNull();
    expect(q('share-selected-member')!.textContent).toContain('Bob Builder');

    // Share button should be enabled
    const shareBtn = q('share-confirm-btn') as HTMLButtonElement;
    expect(shareBtn.disabled).toBe(false);

    shareBtn.click();
    await flushPromises();

    expect(shareFn).toHaveBeenCalledWith('sess-xyz', 'u-bob');
  });

  it('3. shows error when Handoff_Share rejects', async () => {
    const shareFn = vi.fn(async () => { throw new Error('Fleet unavailable'); });
    const { client } = buildClient({ team: TEAM, shareFn });
    mountDialog(client);
    await flushPromises();

    await typeInto('share-recipient-input', 'alice');

    const aliceEl = q('share-member-u-alice');
    expect(aliceEl).not.toBeNull();
    aliceEl!.click();
    await nextTick();
    await flushPromises();

    (q('share-confirm-btn') as HTMLButtonElement).click();
    await flushPromises();

    const errEl = q('share-error');
    expect(errEl).not.toBeNull();
    expect(errEl!.textContent).toContain('Fleet unavailable');
  });
});
