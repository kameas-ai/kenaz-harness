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
  approveAllowed: true,
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
  approveAllowed: true,
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
  approveAllowed: true,
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
  approveAllowed: false,
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

  it('ml_notice: body_text rendered verbatim as text; I agree approves by id', async () => {
    const { client, approveItem } = makeClient([view([NOTICE])]);
    const w = await mountModal(client);
    const body = q(w, 'pending-approval-body');
    expect(body.element.textContent).toBe(NOTICE_TEXT);
    expect(body.classes()).toContain('whitespace-pre-wrap');
    expect(q(w, 'pending-approval-accept').exists()).toBe(false);
    expect(q(w, 'pending-approval-approve').text()).toBe('I agree');
    await q(w, 'pending-approval-approve').trigger('click');
    await flushPromises();
    expect(approveItem).toHaveBeenCalledWith('ml_notice:o1:4');
  });

  it('ml_exclusions_change: informational, Dismiss approves it', async () => {
    const { client, approveItem } = makeClient([view([EXCL])]);
    const w = await mountModal(client);
    expect(q(w, 'pending-approval-blocking').exists()).toBe(false);
    expect(q(w, 'pending-approval-approve').text()).toBe('Dismiss');
    await q(w, 'pending-approval-approve').trigger('click');
    await flushPromises();
    expect(approveItem).toHaveBeenCalledWith('ml_exclusions_change:o1:5');
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
