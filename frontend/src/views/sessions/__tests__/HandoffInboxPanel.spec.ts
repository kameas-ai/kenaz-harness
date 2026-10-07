/**
 * HandoffInboxPanel.spec.ts — device-keys-handoff-01DEVKH01 WP06.
 * Open → Handoff_Accept → emits the new local session id; dismiss →
 * Handoff_Delete; undecryptable rows are disabled with honest copy;
 * backend error copy shown verbatim; this device's receive warning.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import HandoffInboxPanel from '@/views/sessions/HandoffInboxPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { applyFleetSession } from '@/lib/fleetSession';
import type { FleetInboxItemView, FleetSessionView } from '@/lib/types';
import { handoffErrorText } from '@/views/sessions/handoffErrors';

const OK: FleetInboxItemView = {
  inboxItemID: 'item-ok',
  sessionID: 's1',
  senderUserID: 'u-alice',
  senderEmail: 'alice@example.com',
  receivedAt: '2026-10-07T08:00:00Z',
  undecryptable: false,
};
const DEAD: FleetInboxItemView = { ...OK, inboxItemID: 'item-dead', undecryptable: true };

function mountPanel(overrides: Record<string, unknown>, items = [OK, DEAD]) {
  const client = createFakeHarnessClient(overrides);
  return mount(HandoffInboxPanel, {
    props: { items },
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
}

describe('HandoffInboxPanel', () => {
  afterEach(() => {
    applyFleetSession(null);
    vi.clearAllMocks();
  });

  it('opens an item and emits the new local session id', async () => {
    const accept = vi.fn(async () => ({ localSessionID: 'local-1', eventCount: 3, title: 't', alreadyAccepted: false }));
    const w = mountPanel({ Handoff_Accept: accept });
    await w.get('[data-testid="handoff-inbox-item-item-ok"] [data-testid="handoff-inbox-open"]').trigger('click');
    await flushPromises();
    expect(accept).toHaveBeenCalledWith('item-ok');
    expect(w.emitted('opened')?.[0]).toEqual(['local-1']);
    expect(w.emitted('changed')).toBeTruthy();
  });

  it('dismisses via Handoff_Delete', async () => {
    const del = vi.fn(async () => {});
    const w = mountPanel({ Handoff_Delete: del });
    await w.get('[data-testid="handoff-inbox-item-item-dead"] [data-testid="handoff-inbox-dismiss"]').trigger('click');
    await flushPromises();
    expect(del).toHaveBeenCalledWith('item-dead');
  });

  it('disables undecryptable rows with honest copy', () => {
    const w = mountPanel({});
    const dead = w.get('[data-testid="handoff-inbox-item-item-dead"]');
    expect(dead.get('[data-testid="handoff-inbox-open"]').attributes('disabled')).toBeDefined();
    expect(dead.text()).toContain('no longer exists');
    expect(w.get('[data-testid="handoff-inbox-item-item-ok"]').find('[data-testid="handoff-inbox-undecryptable"]').exists()).toBe(false);
  });

  it('shows the backend error copy when opening fails', async () => {
    const copy = 'This shared session was sent to another of your devices (or to an earlier key of this one), so it can\'t be opened here.';
    const w = mountPanel({ Handoff_Accept: vi.fn(async () => { throw new Error(copy); }) });
    await w.get('[data-testid="handoff-inbox-item-item-ok"] [data-testid="handoff-inbox-open"]').trigger('click');
    await flushPromises();
    expect(w.get('[data-testid="handoff-inbox-error"]').text()).toBe(copy);
    expect(w.emitted('opened')).toBeFalsy();
  });

  it("warns when this device can't receive shares", () => {
    applyFleetSession({
      state: 'signed_in', autoRetry: true, tokensUsable: true,
      claims: { hasSubject: true, hasOrgClaim: true },
      deviceKeys: { status: 'too_many_devices', message: "This device can't receive shared sessions: too many devices." },
    } as unknown as FleetSessionView);
    const w = mountPanel({}, []);
    expect(w.get('[data-testid="handoff-receive-warning"]').text()).toContain('too many devices');
  });
});

describe('handoffErrorText', () => {
  it('unwraps Error and hides raw status strings', () => {
    expect(handoffErrorText(new Error('Error: Your teammate is busy.'))).toBe('Your teammate is busy.');
    expect(handoffErrorText('fleet: share session: status 409')).not.toContain('409');
    expect(handoffErrorText('')).toContain('try again');
  });
});
