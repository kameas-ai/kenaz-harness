/**
 * Pending-approvals hub in Settings — ml-producer-01MLPRD01 WP06.
 *
 * The banner provider ("N items need your approval": signed in + count > 0,
 * error when anything is required), the fix opening the step-through modal,
 * and the modal per kind: legal checkbox gating, ml_notice body verbatim,
 * informational exclusions change, unknown kinds rendered generically,
 * non-allowlisted items not approvable, stale items re-shown.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { ref, readonly } from 'vue';

const served = ref(false);
vi.mock('@/lib/useServedMode', () => ({
  isServedMode: () => served.value,
  useServedMode: () => readonly(served),
}));

import SettingsIssuesBanner from '@/views/settings/SettingsIssuesBanner.vue';
import PendingApprovalsModal from '@/views/settings/PendingApprovalsModal.vue';
import { createFakeHarnessClient, type HarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { collectSettingsIssues } from '@/lib/settingsIssues';
import { formatAcceptByDate } from '@/lib/approvalDeferral';
import { closePendingApprovalsDialog, pendingApprovalsDialog } from '@/lib/pendingApprovalsDialog';
import type { PendingApprovalItem, PendingApprovals } from '@/lib/types';

const NOTICE_TEXT =
  'Acme Corp has turned on hosted inference.\n\nChanged since you last approved:\n- no longer excluded: hr/**';

const LEGAL: PendingApprovalItem = {
  id: 'legal:terms:v1.1',
  kind: 'legal_acceptance',
  title: 'Terms of Use',
  summary: 'Review and accept the current Terms of Use (v1.1).',
  bodyText: '',
  documentUrl: 'https://kenaz.kameas.ai/terms.html',
  documentSha256: 'a'.repeat(64),
  version: 'v1.1',
  blocking: 'Continued use of Fleet',
  required: true,
  deferrableUntil: '',
  approveAllowed: true,
  localDismiss: false,
};
const NOTICE: PendingApprovalItem = {
  id: 'ml_notice:o1:4',
  kind: 'ml_notice',
  title: 'Hosted inference notice',
  summary: 'Read how your activity is uploaded.',
  bodyText: NOTICE_TEXT,
  documentUrl: '',
  documentSha256: '',
  version: '4',
  blocking: 'Hosted inference uploads for Acme Corp',
  required: true,
  deferrableUntil: '',
  approveAllowed: true,
  localDismiss: false,
};
const EXCL: PendingApprovalItem = {
  id: 'ml_exclusions_change:o1:5',
  kind: 'ml_exclusions_change',
  title: 'Exclusions changed',
  summary: 'Your organization now excludes more.',
  bodyText: 'Added: secrets/**',
  documentUrl: '',
  documentSha256: '',
  version: '5',
  blocking: 'Nothing is paused',
  required: false,
  deferrableUntil: '',
  approveAllowed: true,
  localDismiss: false,
};
const FUTURE: PendingApprovalItem = {
  id: 'future:1',
  kind: 'future_kind',
  title: 'Something new',
  summary: 'A kind this build does not know.',
  bodyText: 'Plain text body.',
  documentUrl: '',
  documentSha256: '',
  version: '1',
  blocking: 'Nothing is paused',
  required: false,
  deferrableUntil: '',
  approveAllowed: false,
  localDismiss: false,
};

function view(items: PendingApprovalItem[], over: Partial<PendingApprovals> = {}): PendingApprovals {
  return {
    signedIn: true,
    available: true,
    items,
    requiredCount: items.filter((i) => i.required).length,
    ...over,
  };
}

function makeClient(lists: PendingApprovals[], approve?: (id: string) => Promise<PendingApprovals>) {
  const base = createFakeHarnessClient();
  const queue = [...lists];
  const pendingApprovals = vi.fn(async () => (queue.length > 1 ? queue.shift()! : queue[0]));
  const approveItem = vi.fn(approve ?? (async () => view([])));
  const openExternalURL = vi.fn();
  const client: HarnessClient = {
    ...base,
    openExternalURL,
    fleet: { ...base.fleet, pendingApprovals, approveItem },
  };
  return { client, pendingApprovals, approveItem, openExternalURL };
}

const STUBS = { teleport: true };
const q = (w: any, id: string) => w.find(`[data-testid="${id}"]`);

beforeEach(() => {
  served.value = false;
  closePendingApprovalsDialog();
});

describe('pending-approvals settings issue provider', () => {
  it('no issue when signed out or nothing pending', async () => {
    for (const v of [view([NOTICE], { signedIn: false }), view([]), view([], { available: false })]) {
      const { client } = makeClient([v]);
      const issues = await collectSettingsIssues(client, false);
      expect(issues.find((i) => i.id === 'pending-approvals')).toBeUndefined();
    }
  });

  it('error severity when any item is required; plural count', async () => {
    const { client } = makeClient([view([LEGAL, NOTICE])]);
    const [issue] = (await collectSettingsIssues(client, false)).filter((i) => i.id === 'pending-approvals');
    expect(issue.severity).toBe('error');
    expect(issue.title).toBe('2 items need your approval');
    expect(issue.fix?.label).toBe('Review');
  });

  it('warning severity when everything is informational', async () => {
    const { client } = makeClient([view([EXCL])]);
    const [issue] = (await collectSettingsIssues(client, false)).filter((i) => i.id === 'pending-approvals');
    expect(issue.severity).toBe('warning');
    expect(issue.title).toBe('1 item needs your approval');
  });

  it('warning with "Accept by <date>" while every required item is still deferrable', async () => {
    const soon = { ...LEGAL, deferrableUntil: new Date(Date.now() + 10 * 86_400_000).toISOString() };
    const later = { ...LEGAL, id: 'legal:privacy:v1.2', deferrableUntil: new Date(Date.now() + 40 * 86_400_000).toISOString() };
    const { client } = makeClient([view([later, soon])]);
    const [issue] = (await collectSettingsIssues(client, false)).filter((i) => i.id === 'pending-approvals');
    expect(issue.severity).toBe('warning');
    expect(issue.body).toBe(`Accept by ${formatAcceptByDate(new Date(soon.deferrableUntil))} to keep using Fleet. Nothing is paused yet.`);
    expect(issue.fix?.label).toBe('Review');
  });

  it('error once a deferral has passed, or when another required item blocks now', async () => {
    const past = { ...LEGAL, deferrableUntil: new Date(Date.now() - 60_000).toISOString() };
    const future = { ...LEGAL, deferrableUntil: new Date(Date.now() + 86_400_000).toISOString() };
    for (const items of [[past], [future, NOTICE]]) {
      const { client } = makeClient([view(items)]);
      const [issue] = (await collectSettingsIssues(client, false)).filter((i) => i.id === 'pending-approvals');
      expect(issue.severity).toBe('error');
      expect(issue.body).toBe('Some things are paused until you review and approve them.');
    }
  });

  it('skipped in a served build (desktop-only bindings)', async () => {
    const { client, pendingApprovals } = makeClient([view([NOTICE])]);
    await collectSettingsIssues(client, true);
    expect(pendingApprovals).not.toHaveBeenCalled();
  });
});

describe('banner → modal', () => {
  it('Review opens the modal; closing re-reads and keeps the issue while items stay pending', async () => {
    const { client, pendingApprovals } = makeClient([view([NOTICE])]);
    const w = mount(SettingsIssuesBanner, {
      global: { provide: { [HarnessClientKey as symbol]: client }, stubs: STUBS },
    });
    await flushPromises();
    expect(q(w, 'settings-issue-pending-approvals').text()).toContain('1 item needs your approval');
    expect(q(w, 'pending-approvals-modal').exists()).toBe(false);

    await q(w, 'settings-issue-fix').trigger('click');
    await flushPromises();
    expect(pendingApprovalsDialog.open).toBe(true);
    expect(q(w, 'pending-approvals-modal').exists()).toBe(true);
    const before = pendingApprovals.mock.calls.length;

    await q(w, 'pending-approvals-close').trigger('click');
    await flushPromises();
    expect(q(w, 'pending-approvals-modal').exists()).toBe(false);
    expect(pendingApprovals.mock.calls.length).toBeGreaterThan(before);
    expect(q(w, 'settings-issue-pending-approvals').exists()).toBe(true);
  });

  it('after approving everything the issue disappears', async () => {
    const { client } = makeClient([view([NOTICE]), view([NOTICE]), view([])], async () => view([]));
    const w = mount(SettingsIssuesBanner, {
      global: { provide: { [HarnessClientKey as symbol]: client }, stubs: STUBS },
    });
    await flushPromises();
    await q(w, 'settings-issue-fix').trigger('click');
    await flushPromises();
    await q(w, 'pending-approval-approve').trigger('click');
    await flushPromises();
    expect(q(w, 'pending-approvals-empty').exists()).toBe(true);
    await q(w, 'pending-approvals-close').trigger('click');
    await flushPromises();
    expect(q(w, 'settings-issues-banner').exists()).toBe(false);
  });
});

describe('PendingApprovalsModal', () => {
  async function mountModal(client: HarnessClient) {
    const w = mount(PendingApprovalsModal, { props: { client }, global: { stubs: STUBS } });
    await flushPromises();
    return w;
  }

  it('legal: Accept is disabled until the per-document box is ticked; document opens externally', async () => {
    const { client, approveItem, openExternalURL } = makeClient([view([LEGAL])]);
    const w = await mountModal(client);
    expect(q(w, 'pending-approval-blocking').text()).toContain('Continued use of Fleet');
    expect(q(w, 'pending-approval-document-url').text()).toBe(LEGAL.documentUrl);
    await q(w, 'pending-approval-document-open').trigger('click');
    expect(openExternalURL).toHaveBeenCalledWith(LEGAL.documentUrl);

    expect(q(w, 'pending-approval-accept').text()).toBe('I have read and accept Terms of Use (v1.1)');
    const btn = q(w, 'pending-approval-approve');
    expect(btn.text()).toBe('Accept');
    expect(btn.attributes('disabled')).toBeDefined();
    await btn.trigger('click');
    expect(approveItem).not.toHaveBeenCalled();

    await q(w, 'pending-approval-accept-box').setValue(true);
    expect(q(w, 'pending-approval-approve').attributes('disabled')).toBeUndefined();
    await q(w, 'pending-approval-approve').trigger('click');
    await flushPromises();
    expect(approveItem).toHaveBeenCalledWith('legal:terms:v1.1');
  });

  it('legal, still deferrable: "Accept by <date>" only; Fleet\'s present-tense blocking text is not appended', async () => {
    const until = new Date(Date.now() + 10 * 86_400_000);
    // What Fleet actually sends for a deferrable item (pending_approvals.go).
    const blocking = `Nothing is paused until ${formatAcceptByDate(until)}`;
    const { client } = makeClient([view([{ ...LEGAL, blocking, deferrableUntil: until.toISOString() }])]);
    const w = await mountModal(client);
    expect(q(w, 'pending-approval-blocking').exists()).toBe(false);
    expect(q(w, 'pending-approval-deferrable').text()).toBe(`Accept by ${formatAcceptByDate(until)}.`);
    expect(w.text()).not.toContain('paused until you approve: Nothing is paused');
  });

  it('an open dialog flips to "Paused until you approve" when the deadline passes', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] });
    try {
      const until = new Date(Date.now() + 90_000);
      const blocking = `Nothing is paused until ${formatAcceptByDate(until)}`;
      const { client } = makeClient([view([{ ...LEGAL, blocking, deferrableUntil: until.toISOString() }])]);
      const w = await mountModal(client);
      expect(q(w, 'pending-approval-deferrable').exists()).toBe(true);
      await vi.advanceTimersByTimeAsync(120_000);
      await flushPromises();
      expect(q(w, 'pending-approval-deferrable').exists()).toBe(false);
      // Fleet's stale pre-deadline text is not shown after the flip.
      expect(q(w, 'pending-approval-blocking').text()).toBe('Paused until you approve.');
      w.unmount();
    } finally {
      vi.useRealTimers();
    }
  });

  it('several deferrable legal items: each shows its own date', async () => {
    const sa = new Date(Date.now() + 10 * 86_400_000);
    const privacy = new Date(Date.now() + 20 * 86_400_000);
    const { client, approveItem } = makeClient([
      view([
        { ...LEGAL, id: 'legal:sa:v1.2', deferrableUntil: sa.toISOString() },
        { ...LEGAL, id: 'legal:privacy:v2.2', title: 'Privacy Policy', deferrableUntil: privacy.toISOString() },
      ]),
    ]);
    approveItem.mockImplementation(async () =>
      view([{ ...LEGAL, id: 'legal:privacy:v2.2', title: 'Privacy Policy', deferrableUntil: privacy.toISOString() }]),
    );
    const w = await mountModal(client);
    expect(q(w, 'pending-approval-deferrable').text()).toContain(`Accept by ${formatAcceptByDate(sa)}.`);
    await q(w, 'pending-approval-accept-box').setValue(true);
    await q(w, 'pending-approval-approve').trigger('click');
    await flushPromises();
    expect(q(w, 'pending-approval-title').text()).toBe('Privacy Policy');
    expect(q(w, 'pending-approval-deferrable').text()).toContain(`Accept by ${formatAcceptByDate(privacy)}.`);
  });

  it('legal, deferral passed on a stale copy: "Paused until you approve." without the stale text', async () => {
    const past = new Date(Date.now() - 60_000);
    const blocking = `Nothing is paused until ${formatAcceptByDate(past)}`;
    const { client } = makeClient([view([{ ...LEGAL, blocking, deferrableUntil: past.toISOString() }])]);
    const w = await mountModal(client);
    expect(q(w, 'pending-approval-deferrable').exists()).toBe(false);
    expect(q(w, 'pending-approval-blocking').text()).toBe('Paused until you approve.');
  });

  it('ml_notice: body_text rendered verbatim as text; "I have read this" approves by id', async () => {
    const { client, approveItem } = makeClient([view([NOTICE])]);
    const w = await mountModal(client);
    const body = q(w, 'pending-approval-body');
    expect(body.element.textContent).toBe(NOTICE_TEXT);
    expect(body.classes()).toContain('whitespace-pre-wrap');
    expect(q(w, 'pending-approval-accept').exists()).toBe(false);
    expect(q(w, 'pending-approval-approve').text()).toBe('I have read this');
    await q(w, 'pending-approval-approve').trigger('click');
    await flushPromises();
    expect(approveItem).toHaveBeenCalledWith('ml_notice:o1:4');
  });

  it('ml_notice: the button never reads agree / accept / consent (legal, 2026-10-16)', async () => {
    for (const over of [{}, { required: false }, { documentUrl: 'https://example.test/notice' }]) {
      const { client } = makeClient([view([{ ...NOTICE, ...over }])]);
      const w = await mountModal(client);
      expect(q(w, 'pending-approval-approve').text()).toBe('I have read this');
      expect(q(w, 'pending-approval-approve').text()).not.toMatch(/agree|accept|consent/i);
    }
  });

  // Legal (kameas-ai-11, 2026-10-16): only "I have read this" records the
  // exclusions change (exclusions-seen); the full body shows untruncated.
  it('ml_exclusions_change: "I have read this" is the only action that records it', async () => {
    const longBody = 'Excluded now:\n' + Array.from({ length: 80 }, (_, i) => `- path/${i}/**`).join('\n') + '\n\nChanged: added path/79/**';
    const { client, approveItem } = makeClient([view([{ ...EXCL, bodyText: longBody }])]);
    const w = await mountModal(client);
    expect(q(w, 'pending-approval-blocking').exists()).toBe(false);
    expect(q(w, 'pending-approval-body').text()).toBe(longBody);
    expect(q(w, 'pending-approval-approve').text()).toBe('I have read this');
    expect(w.text()).not.toMatch(/\b(agree|consent|accept|approval|approve|OK, continue)\b/i);
    await q(w, 'pending-approval-approve').trigger('click');
    await flushPromises();
    expect(approveItem).toHaveBeenCalledTimes(1);
    expect(approveItem).toHaveBeenCalledWith('ml_exclusions_change:o1:5');
  });

  it('ml_exclusions_change: Close records nothing', async () => {
    const { client, approveItem } = makeClient([view([EXCL])]);
    const w = await mountModal(client);
    await q(w, 'pending-approvals-close').trigger('click');
    await flushPromises();
    expect(approveItem).not.toHaveBeenCalled();
    expect(w.emitted('close')).toHaveLength(1);
  });

  it('ml_exclusions_change: Esc records nothing', async () => {
    const { client, approveItem } = makeClient([view([EXCL])]);
    const w = await mountModal(client);
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await flushPromises();
    expect(w.emitted('close')).toHaveLength(1); // Esc reached the dialog…
    expect(approveItem).not.toHaveBeenCalled(); // …and posted nothing
  });

  it('ml_exclusions_change: an outside click records nothing and does not close', async () => {
    const { client, approveItem } = makeClient([view([EXCL])]);
    const w = await mountModal(client);
    await w.find('.bg-modal-overlay').trigger('click');
    await flushPromises();
    expect(approveItem).not.toHaveBeenCalled();
    expect(w.emitted('close')).toBeUndefined();
  });

  it('ml_exclusions_change: neutral dialog title and never a read-and-accept checkbox', async () => {
    const withDoc = { ...EXCL, documentUrl: 'https://kenaz.kameas.ai/x.html', documentSha256: 'b'.repeat(64) };
    const { client } = makeClient([view([withDoc])]);
    const w = await mountModal(client);
    expect(q(w, 'pending-approvals-title').text()).toBe('Items that need your attention');
    expect(q(w, 'pending-approval-accept').exists()).toBe(false);
    expect(q(w, 'pending-approval-approve').text()).toBe('I have read this');
    expect(w.text()).not.toMatch(/\b(agree|consent|accept|approval|approve|OK, continue)\b/i);
  });

  it('ml_notice with a documentUrl: never the read-and-accept checkbox', async () => {
    const withDoc = { ...NOTICE, documentUrl: 'https://kenaz.kameas.ai/notice.html', documentSha256: 'c'.repeat(64) };
    const { client } = makeClient([view([withDoc])]);
    const w = await mountModal(client);
    expect(q(w, 'pending-approval-accept').exists()).toBe(false);
    expect(q(w, 'pending-approval-approve').text()).toBe('I have read this');
    expect(q(w, 'pending-approval-approve').attributes('disabled')).toBeUndefined();
  });

  it('dialog title says "approval" only when every item is required', async () => {
    const { client } = makeClient([view([LEGAL, NOTICE])]);
    const w = await mountModal(client);
    expect(q(w, 'pending-approvals-title').text()).toBe('Items that need your approval');
  });

  it('"Offload available" offer: Not now sends the hub\'s own (member-private) dismissal', async () => {
    const OFFER: PendingApprovalItem = {
      ...FUTURE,
      id: 'ml_opt_in_available:o1',
      kind: 'ml_opt_in_available',
      title: 'Turn on offload?',
      approveAllowed: true,
      localDismiss: false,
    };
    const { client, approveItem } = makeClient([view([OFFER])]);
    const dismissItemLocally = vi.fn(async () => view([]));
    client.fleet.dismissItemLocally = dismissItemLocally;
    const w = await mountModal(client);
    expect(q(w, 'pending-approval-approve').text()).toBe('Not now');
    await q(w, 'pending-approval-approve').trigger('click');
    await flushPromises();
    expect(approveItem).toHaveBeenCalledWith('ml_opt_in_available:o1');
    expect(dismissItemLocally).not.toHaveBeenCalled();
  });

  it('a malformed opt-in offer (not approvable) gets no approve button, only the local Not now', async () => {
    const BAD: PendingApprovalItem = {
      ...FUTURE,
      id: 'ml_opt_in_available:o1',
      kind: 'ml_opt_in_available',
      approveAllowed: false,
      localDismiss: true,
    };
    const { client, approveItem } = makeClient([view([BAD])]);
    const w = await mountModal(client);
    expect(q(w, 'pending-approval-approve').exists()).toBe(false);
    expect(q(w, 'pending-approval-dismiss-local').exists()).toBe(true);
    expect(approveItem).not.toHaveBeenCalled();
  });

  it('informational item the app sends nothing for: Not now hides it on this device only', async () => {
    const INFO: PendingApprovalItem = { ...FUTURE, id: 'future_info:1', kind: 'future_info', approveAllowed: false, localDismiss: true };
    const { client, approveItem } = makeClient([view([INFO])]);
    const dismissItemLocally = vi.fn(async () => view([]));
    client.fleet.dismissItemLocally = dismissItemLocally;
    const w = await mountModal(client);
    expect(q(w, 'pending-approval-approve').exists()).toBe(false);
    expect(q(w, 'pending-approval-local-dismiss-note').text()).toContain('this device only');
    await q(w, 'pending-approval-dismiss-local').trigger('click');
    await flushPromises();
    expect(dismissItemLocally).toHaveBeenCalledWith('future_info:1');
    expect(approveItem).not.toHaveBeenCalled();
    expect(q(w, 'pending-approvals-empty').exists()).toBe(true);
  });

  it('unknown kind rendered generically; a non-allowlisted action offers no approve', async () => {
    const { client, approveItem } = makeClient([view([FUTURE])]);
    const w = await mountModal(client);
    expect(q(w, 'pending-approval-future_kind').exists()).toBe(true);
    expect(q(w, 'pending-approval-title').text()).toBe('Something new');
    expect(q(w, 'pending-approval-body').text()).toBe('Plain text body.');
    expect(q(w, 'pending-approval-not-allowed').exists()).toBe(true);
    expect(q(w, 'pending-approval-approve').exists()).toBe(false);
    expect(approveItem).not.toHaveBeenCalled();
  });

  it('steps through items; an approved item is replaced by the next', async () => {
    const { client, approveItem } = makeClient([view([NOTICE, EXCL, FUTURE])], async () => view([FUTURE]));
    const w = await mountModal(client);
    expect(q(w, 'pending-approvals-step').text()).toBe('1 of 3');
    await q(w, 'pending-approvals-next').trigger('click');
    expect(q(w, 'pending-approval-title').text()).toBe('Exclusions changed');
    await q(w, 'pending-approvals-prev').trigger('click');
    expect(q(w, 'pending-approval-title').text()).toBe('Hosted inference notice');
    await q(w, 'pending-approval-approve').trigger('click');
    await flushPromises();
    expect(approveItem).toHaveBeenCalledWith('ml_notice:o1:4');
    expect(q(w, 'pending-approval-title').text()).toBe('Something new');
  });

  it('a stale item is shown again with its fresh text and the box unticked', async () => {
    const fresh = { ...LEGAL, version: 'v1.2', title: 'Terms of Use' };
    const { client } = makeClient([view([LEGAL])], async () => view([{ ...fresh, id: LEGAL.id }], { changed: true }));
    const w = await mountModal(client);
    await q(w, 'pending-approval-accept-box').setValue(true);
    await q(w, 'pending-approval-approve').trigger('click');
    await flushPromises();
    expect(q(w, 'pending-approval-changed').exists()).toBe(true);
    expect(q(w, 'pending-approval-accept').text()).toBe('I have read and accept Terms of Use (v1.2)');
    expect(q(w, 'pending-approval-approve').attributes('disabled')).toBeDefined();
  });

  it('an approve error is shown and nothing changes', async () => {
    const { client } = makeClient([view([NOTICE])], async () => {
      throw new Error('fleet: pending approval action is not in the harness allowlist; nothing was sent');
    });
    const w = await mountModal(client);
    await q(w, 'pending-approval-approve').trigger('click');
    await flushPromises();
    expect(q(w, 'pending-approvals-error').text()).toContain('allowlist');
    expect(q(w, 'pending-approval-title').text()).toBe('Hosted inference notice');
  });
});
