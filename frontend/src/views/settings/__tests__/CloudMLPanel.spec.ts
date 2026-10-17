/**
 * CloudMLPanel — ml-producer-01MLPRD01 WP01 (spec §5).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import CloudMLPanel from '@/views/settings/CloudMLPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { MLStatus, PendingApprovals } from '@/lib/types';

const HARNESS_LINE =
  'From this app, Kenaz sends only what the agent does in your harness sessions (tool used, outcome, ' +
  'timing, hashed file names, the first two words of commands). It never sends file contents, prompts ' +
  'or replies.';

const NOTICE_V3 = 'Acme Corp has turned on hosted inference. (notice v3)';
const NOTICE_V4 = 'Acme Corp has turned on hosted inference. (notice v4)';

function status(over: Partial<MLStatus> = {}): MLStatus {
  return {
    signedIn: true,
    entitled: true,
    orgPaused: false,
    loaded: true,
    orgOffloadEnabled: false,
    orgPolicy: 'member_choice',
    userWorkflowEventsOptedIn: false,
    noticeAckRequired: false,
    effective: false,
    noticeVersion: 1,
    noticeAckedAt: '',
    retentionDays: 90,
    retainOnWithdrawal: false,
    orgName: 'Acme Corp',
    noticeText: '',
    noticeTextRevision: 0,
    ackedTextRevision: 0,
    noticeNeedsDashboard: false,
    noticeFromHub: false,
    exclusionPaths: [],
    exclusionCommands: [],
    excludeBrowser: false,
    exclusionsVersion: 1,
    legacyExclusionNotes: [],
    ...over,
  };
}

function mountWith(
  initial: MLStatus,
  opts: {
    ack?: (v: number) => Promise<MLStatus>;
    optIn?: (v: boolean) => Promise<MLStatus>;
    statuses?: MLStatus[];
    approve?: (id: string) => Promise<PendingApprovals>;
  } = {},
) {
  const queue = [...(opts.statuses ?? [])];
  const mlStatus = vi.fn(async () => queue.shift() ?? initial);
  const mlAckNotice = vi.fn(opts.ack ?? (async () => status()));
  const setWorkflowEventsOptIn = vi.fn(opts.optIn ?? (async () => status()));
  const approveItem = vi.fn(
    opts.approve ??
      (async (): Promise<PendingApprovals> => ({ signedIn: true, available: true, items: [], requiredCount: 0 })),
  );
  const client = createFakeHarnessClient({
    fleet: { mlStatus, mlAckNotice, setWorkflowEventsOptIn, approveItem } as any,
  });
  const wrapper = mount(CloudMLPanel, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  const openExternalURL = vi.fn();
  client.openExternalURL = openExternalURL;
  return { wrapper, mlStatus, mlAckNotice, setWorkflowEventsOptIn, openExternalURL, approveItem };
}

const q = (w: any, id: string) => w.find(`[data-testid="${id}"]`);

describe('CloudMLPanel', () => {
  // Fleet's exclusions paragraph (the backend's port of exclusionsSummary,
  // pinned byte-for-byte against Fleet's fixture in ml_fixture_test.go).
  const SUMMARY =
    'Excluded from upload: file paths matching: legal/**. Your organization also listed these exclusions in ' +
    "its own words. Fleet can't apply them automatically, so matching activity may still be uploaded: " +
    'no HR team; no legal.';
  const EXCL = {
    exclusionPaths: ['legal/**'],
    exclusionCommands: ['ssh'],
    legacyExclusionNotes: ['no HR team', 'no legal'],
    exclusionsSummary: SUMMARY,
  };
  const OLD_HEADING = /Earlier notes from your organization|Your organization excludes:/;

  it("no notice shown: Fleet's exclusions paragraph, read-only, verbatim (WP05)", async () => {
    const { wrapper } = mountWith(status({ ...EXCL, effective: true }));
    await flushPromises();
    const block = q(wrapper, 'cloud-ml-exclusions');
    expect(block.exists()).toBe(true);
    expect(block.text()).toBe(SUMMARY);
    expect(block.findAll('input').length).toBe(0);
    expect(block.findAll('button').length).toBe(0);
    // No markup of our own: no typed list, no legacy-notes heading or list.
    expect(q(wrapper, 'cloud-ml-legacy-notes').exists()).toBe(false);
    expect(q(wrapper, 'cloud-ml-exclusion-path').exists()).toBe(false);
    expect(wrapper.text()).not.toMatch(OLD_HEADING);
  });

  describe('exclusions in the notice (legal, kameas-ai-11, 2026-10-16)', () => {
    const before = (a: Element, b: Element) => !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);

    it("fallback notice: Fleet's exact exclusions text after the notice text, before the first button", async () => {
      const { wrapper } = mountWith(
        status({
          ...EXCL,
          orgOffloadEnabled: true,
          orgPolicy: 'member_choice',
          noticeVersion: 3,
          noticeText: 'plain',
          notice: {
            heading: 'Your organization has enabled workflow data offload',
            intro: 'intro',
            sections: [],
            policyText: 'You decide.',
            policy: 'member_choice',
            exclusionsText: SUMMARY,
            actions: ['Turn on Offload', 'Not now'],
          },
        }),
      );
      await flushPromises();
      const card = q(wrapper, 'cloud-ml-notice');
      const excl = card.find('[data-testid="cloud-ml-exclusions"]');
      expect(excl.exists()).toBe(true);
      expect(excl.text()).toBe(SUMMARY);
      const firstButton = card.find('[data-testid="cloud-ml-notice-action"]');
      expect(before(q(wrapper, 'cloud-ml-notice-policy').element, excl.element)).toBe(true);
      expect(before(excl.element, firstButton.element)).toBe(true);
      // Rendered once, and nothing of the old markup anywhere.
      expect(wrapper.findAll('[data-testid="cloud-ml-exclusions"]').length).toBe(1);
      expect(q(wrapper, 'cloud-ml-legacy-notes').exists()).toBe(false);
      expect(wrapper.text()).not.toMatch(OLD_HEADING);
    });

    it('hub notice: no exclusions of our own; body_text whole, before the button', async () => {
      const BODY = 'What changed\n\nNotice text.\n\n' + SUMMARY;
      const { wrapper } = mountWith(
        status({
          ...EXCL,
          orgOffloadEnabled: true,
          orgPolicy: 'on',
          noticeAckRequired: true,
          noticeVersion: 3,
          noticeText: BODY,
          noticeFromHub: true,
          noticeItemId: 'ml_notice:o1:3',
        }),
      );
      await flushPromises();
      expect(q(wrapper, 'cloud-ml-exclusions').exists()).toBe(false);
      expect(q(wrapper, 'cloud-ml-legacy-notes').exists()).toBe(false);
      const text = q(wrapper, 'cloud-ml-notice-text');
      expect(text.element.textContent).toBe(BODY);
      expect(before(text.element, q(wrapper, 'cloud-ml-ack').element)).toBe(true);
      // No "excludes" wording outside the served body_text.
      const outside = wrapper.text().replace(text.text(), '');
      expect(outside).not.toMatch(/exclud/i);
      expect(wrapper.text()).not.toMatch(OLD_HEADING);
    });
  });

  it('shows no exclusions block when the org excludes nothing', async () => {
    const { wrapper } = mountWith(status({ effective: true, exclusionsSummary: '' }));
    await flushPromises();
    expect(q(wrapper, 'cloud-ml-exclusions').exists()).toBe(false);
    expect(q(wrapper, 'cloud-ml-legacy-notes').exists()).toBe(false);
  });

  it('shows no exclusions while /me/ml is not loaded', async () => {
    const { wrapper } = mountWith(
      status({ loaded: false, fleetError: 'boom', exclusionPaths: ['hr/**'], legacyExclusionNotes: ['x'], exclusionsSummary: 'x' }),
    );
    await flushPromises();
    expect(q(wrapper, 'cloud-ml-exclusions').exists()).toBe(false);
    expect(q(wrapper, 'cloud-ml-legacy-notes').exists()).toBe(false);
  });

  it('is hidden when signed out', async () => {
    const { wrapper } = mountWith(status({ signedIn: false }));
    await flushPromises();
    expect(q(wrapper, 'cloud-ml-panel').exists()).toBe(false);
  });

  it('is hidden without the hosted_inference capability', async () => {
    const { wrapper } = mountWith(status({ entitled: false, loaded: false }));
    await flushPromises();
    expect(q(wrapper, 'cloud-ml-panel').exists()).toBe(false);
  });

  it('offload off: fields shown, no opt-in toggle, no notice, not sending', async () => {
    const { wrapper } = mountWith(status({ orgOffloadEnabled: false }));
    await flushPromises();
    expect(q(wrapper, 'cloud-ml-panel').exists()).toBe(true);
    expect(q(wrapper, 'cloud-ml-offload').text()).toBe('Off');
    expect(q(wrapper, 'cloud-ml-policy').text()).toContain("Each member's choice");
    expect(q(wrapper, 'cloud-ml-effective').text()).toContain('nothing is sent');
    expect(q(wrapper, 'cloud-ml-retention').text()).toBe('90 days');
    expect(q(wrapper, 'cloud-ml-optin').exists()).toBe(false);
    expect(q(wrapper, 'cloud-ml-notice').exists()).toBe(false);
    expect(q(wrapper, 'cloud-ml-harness-line').text()).toBe(HARNESS_LINE);
  });

  it('member_choice, not opted in: toggle shown unchecked and writes the opt-in', async () => {
    const after = status({
      orgOffloadEnabled: true,
      userWorkflowEventsOptedIn: true,
      noticeAckRequired: true,
      noticeVersion: 3,
      noticeText: NOTICE_V3,
    });
    const { wrapper, setWorkflowEventsOptIn } = mountWith(status({ orgOffloadEnabled: true }), {
      optIn: async () => after,
    });
    await flushPromises();
    const toggle = q(wrapper, 'cloud-ml-optin-toggle');
    expect(toggle.exists()).toBe(true);
    expect((toggle.element as HTMLInputElement).checked).toBe(false);
    expect(q(wrapper, 'cloud-ml-optin-state').text()).toBe('Not opted in');
    await toggle.setValue(true);
    await flushPromises();
    expect(setWorkflowEventsOptIn).toHaveBeenCalledWith(true);
    // Opting in under member_choice now owes a notice ack.
    expect(q(wrapper, 'cloud-ml-notice-text').text()).toBe(NOTICE_V3);
  });

  it('policy on: no opt-in toggle (the member choice does not apply)', async () => {
    const { wrapper } = mountWith(status({ orgOffloadEnabled: true, orgPolicy: 'on' }));
    await flushPromises();
    expect(q(wrapper, 'cloud-ml-optin').exists()).toBe(false);
    expect(q(wrapper, 'cloud-ml-policy').text()).toContain('On for every member');
  });

  it('notice required: shows the notice verbatim + harness line; "I have read this" posts the shown version', async () => {
    const effective = status({
      orgOffloadEnabled: true,
      orgPolicy: 'on',
      effective: true,
      noticeVersion: 3,
      noticeAckedAt: '2026-10-09T12:00:00Z',
    });
    const { wrapper, mlAckNotice } = mountWith(
      status({ orgOffloadEnabled: true, orgPolicy: 'on', noticeAckRequired: true, noticeVersion: 3, noticeText: NOTICE_V3 }),
      { ack: async () => effective },
    );
    await flushPromises();
    expect(q(wrapper, 'cloud-ml-notice-text').text()).toBe(NOTICE_V3);
    expect(q(wrapper, 'cloud-ml-notice-status').text()).toContain('required (version 3)');
    expect(q(wrapper, 'cloud-ml-harness-line').text()).toBe(HARNESS_LINE);
    await q(wrapper, 'cloud-ml-ack').trigger('click');
    await flushPromises();
    expect(mlAckNotice).toHaveBeenCalledWith(3, 'I have read this');
    expect(q(wrapper, 'cloud-ml-notice').exists()).toBe(false);
    expect(q(wrapper, 'cloud-ml-effective').text()).toContain('agent activity is sent');
  });

  it('effective: no notice, acknowledged status shown', async () => {
    const { wrapper } = mountWith(
      status({
        orgOffloadEnabled: true,
        userWorkflowEventsOptedIn: true,
        effective: true,
        noticeVersion: 2,
        noticeAckedAt: '2026-10-09T12:00:00Z',
      }),
    );
    await flushPromises();
    expect(q(wrapper, 'cloud-ml-notice').exists()).toBe(false);
    expect(q(wrapper, 'cloud-ml-notice-status').text()).toContain('Acknowledged');
    expect(q(wrapper, 'cloud-ml-effective').text()).toContain('agent activity is sent');
    expect((q(wrapper, 'cloud-ml-optin-toggle').element as HTMLInputElement).checked).toBe(true);
  });

  it('409 policy_changed: the new notice is shown again with a changed banner', async () => {
    const reShown = status({
      orgOffloadEnabled: true,
      orgPolicy: 'on',
      noticeAckRequired: true,
      noticeVersion: 4,
      noticeText: NOTICE_V4,
      noticeChanged: true,
    });
    const { wrapper, mlAckNotice } = mountWith(
      status({ orgOffloadEnabled: true, orgPolicy: 'on', noticeAckRequired: true, noticeVersion: 3, noticeText: NOTICE_V3 }),
      { ack: async () => reShown },
    );
    await flushPromises();
    await q(wrapper, 'cloud-ml-ack').trigger('click');
    await flushPromises();
    expect(mlAckNotice).toHaveBeenCalledWith(3, 'I have read this');
    expect(q(wrapper, 'cloud-ml-notice-changed').exists()).toBe(true);
    expect(q(wrapper, 'cloud-ml-notice-text').text()).toBe(NOTICE_V4);
    expect(q(wrapper, 'cloud-ml-notice-status').text()).toContain('version 4');
  });

  it('409 surfaced as a thrown policy_changed error: re-reads and re-shows', async () => {
    const reRead = status({
      orgOffloadEnabled: true,
      orgPolicy: 'on',
      noticeAckRequired: true,
      noticeVersion: 4,
      noticeText: NOTICE_V4,
    });
    const first = status({ orgOffloadEnabled: true, orgPolicy: 'on', noticeAckRequired: true, noticeVersion: 3, noticeText: NOTICE_V3 });
    const { wrapper, mlStatus } = mountWith(first, {
      statuses: [first, reRead],
      ack: async () => {
        throw new Error('fleet: ml consent: HTTP 409 policy_changed');
      },
    });
    await flushPromises();
    await q(wrapper, 'cloud-ml-ack').trigger('click');
    await flushPromises();
    expect(mlStatus).toHaveBeenCalledTimes(2);
    expect(q(wrapper, 'cloud-ml-notice-changed').exists()).toBe(true);
    expect(q(wrapper, 'cloud-ml-notice-text').text()).toBe(NOTICE_V4);
    expect(q(wrapper, 'cloud-ml-error').exists()).toBe(false);
  });

  it('a Fleet read error shows no consent fields and says nothing is sent', async () => {
    const { wrapper } = mountWith(status({ loaded: false, fleetError: 'HTTP 403 staff_not_permitted' }));
    await flushPromises();
    expect(q(wrapper, 'cloud-ml-fleet-error').text()).toContain('nothing is sent');
    expect(q(wrapper, 'cloud-ml-fields').exists()).toBe(false);
  });

  it('shipping status block stays empty until the shipper reports, then shows counts', async () => {
    const { wrapper } = mountWith(status());
    await flushPromises();
    expect(q(wrapper, 'cloud-ml-shipping').exists()).toBe(true);
    expect(q(wrapper, 'cloud-ml-shipping').text()).toBe('');

    const { wrapper: w2 } = mountWith(
      status({
        shipping: { lastBatchAt: '', accepted: 5, duplicates: 1, rejected: 2, stopReason: 'ml_not_effective' },
      }),
    );
    await flushPromises();
    expect(q(w2, 'cloud-ml-shipping-counts').text()).toContain('Accepted 5');
    expect(q(w2, 'cloud-ml-shipping-counts').text()).toContain('rejected 2');
    expect(q(w2, 'cloud-ml-shipping-stop').text()).toContain('ml_not_effective');
  });

  describe('newer notice text revision (kenaz-fleet PR 225)', () => {
    const DASHBOARD_TEXT =
      'An updated notice is waiting for your approval. Open your Kenaz Fleet dashboard to review and ' +
      'approve it; uploads from this device stay off until you do.';
    const URL = 'https://dev.fleet.kameas.ai/settings#hosted-inference';
    const rev2 = (over: Partial<MLStatus> = {}) =>
      status({
        orgOffloadEnabled: true,
        orgPolicy: 'on',
        noticeAckRequired: true,
        noticeVersion: 3,
        noticeText: '',
        noticeTextRevision: 2,
        ackedTextRevision: 1,
        noticeNeedsDashboard: true,
        ...over,
      });

    it('routes to the dashboard: no notice, no Acknowledge, link opens in the system browser', async () => {
      const { wrapper, mlAckNotice, openExternalURL } = mountWith(rev2({ noticeDashboardUrl: URL }));
      await flushPromises();
      expect(q(wrapper, 'cloud-ml-notice').exists()).toBe(false);
      expect(q(wrapper, 'cloud-ml-notice-text').exists()).toBe(false);
      expect(q(wrapper, 'cloud-ml-ack').exists()).toBe(false);
      expect(q(wrapper, 'cloud-ml-notice-dashboard-text').text()).toBe(DASHBOARD_TEXT);
      expect(q(wrapper, 'cloud-ml-notice-dashboard-url').text()).toBe(URL);
      await q(wrapper, 'cloud-ml-notice-dashboard-open').trigger('click');
      expect(openExternalURL).toHaveBeenCalledWith(URL);
      expect(mlAckNotice).not.toHaveBeenCalled();
    });

    it('without a known dashboard base: the text only, no link or button', async () => {
      const { wrapper, openExternalURL } = mountWith(rev2());
      await flushPromises();
      expect(q(wrapper, 'cloud-ml-notice-dashboard-text').text()).toBe(DASHBOARD_TEXT);
      expect(q(wrapper, 'cloud-ml-notice-dashboard-url').exists()).toBe(false);
      expect(q(wrapper, 'cloud-ml-notice-dashboard-open').exists()).toBe(false);
      expect(q(wrapper, 'cloud-ml-ack').exists()).toBe(false);
      expect(wrapper.findAll('[data-testid="cloud-ml-notice-dashboard"] button').length).toBe(0);
      expect(openExternalURL).not.toHaveBeenCalled();
    });

    it('a rev-1 notice still shows the local notice and Acknowledge (no dashboard block)', async () => {
      const { wrapper } = mountWith(
        status({ noticeAckRequired: true, noticeVersion: 3, noticeText: NOTICE_V3, noticeTextRevision: 1 }),
      );
      await flushPromises();
      expect(q(wrapper, 'cloud-ml-notice-dashboard').exists()).toBe(false);
      expect(q(wrapper, 'cloud-ml-notice-text').text()).toBe(NOTICE_V3);
      expect(q(wrapper, 'cloud-ml-ack').exists()).toBe(true);
    });
  });

  describe('notice from the pending-approvals hub (WP06)', () => {
    const HUB_TEXT =
      'Acme Corp has turned on hosted inference. (rev 2 wording)\n\n' +
      'Changed since you last approved:\n- no longer excluded: hr/**';
    const hub = (over: Partial<MLStatus> = {}) =>
      status({
        orgOffloadEnabled: true,
        orgPolicy: 'on',
        noticeAckRequired: true,
        noticeVersion: 4,
        noticeTextRevision: 2,
        ackedTextRevision: 1,
        noticeText: HUB_TEXT,
        noticeFromHub: true,
        noticeItemId: 'ml_notice:o1:4',
        ...over,
      });

    it('renders the hub body_text verbatim and approves the hub item, not the legacy ack', async () => {
      const after = status({ orgOffloadEnabled: true, orgPolicy: 'on', effective: true, noticeVersion: 4 });
      const { wrapper, approveItem, mlAckNotice, mlStatus } = mountWith(hub(), { statuses: [hub(), after] });
      await flushPromises();
      const text = q(wrapper, 'cloud-ml-notice-text');
      expect(text.element.textContent).toBe(HUB_TEXT);
      expect(text.classes()).toContain('whitespace-pre-wrap');
      expect(q(wrapper, 'cloud-ml-notice-dashboard').exists()).toBe(false);
      await q(wrapper, 'cloud-ml-ack').trigger('click');
      await flushPromises();
      expect(approveItem).toHaveBeenCalledWith('ml_notice:o1:4');
      expect(mlAckNotice).not.toHaveBeenCalled();
      expect(mlStatus).toHaveBeenCalledTimes(2);
      expect(q(wrapper, 'cloud-ml-effective').text()).toContain('On');
      expect(q(wrapper, 'cloud-ml-notice').exists()).toBe(false);
    });

    it('a stale hub item (changed) re-reads and shows the "changed" line', async () => {
      const { wrapper, approveItem } = mountWith(hub(), {
        approve: async () => ({ signedIn: true, available: true, items: [], requiredCount: 1, changed: true }),
      });
      await flushPromises();
      await q(wrapper, 'cloud-ml-ack').trigger('click');
      await flushPromises();
      expect(approveItem).toHaveBeenCalledTimes(1);
      expect(q(wrapper, 'cloud-ml-notice-changed').exists()).toBe(true);
    });

    it('fallback (hub unavailable): the local notice is acknowledged through the legacy path', async () => {
      const { wrapper, approveItem, mlAckNotice } = mountWith(
        status({ noticeAckRequired: true, noticeVersion: 3, noticeText: NOTICE_V3, noticeTextRevision: 1 }),
      );
      await flushPromises();
      await q(wrapper, 'cloud-ml-ack').trigger('click');
      await flushPromises();
      expect(mlAckNotice).toHaveBeenCalledWith(3, 'I have read this');
      expect(approveItem).not.toHaveBeenCalled();
    });

    it("shows the hub item's title as the heading above the verbatim body_text (Fleet rev-2)", async () => {
      const body = "What's new\n\nAI usage is now included.";
      const { wrapper } = mountWith(
        hub({ noticeTitle: 'Your organization has enabled workflow data offload', noticeText: body }),
      );
      await flushPromises();
      const heading = q(wrapper, 'cloud-ml-notice-heading');
      expect(heading.exists()).toBe(true);
      expect(heading.text()).toBe('Your organization has enabled workflow data offload');
      expect(q(wrapper, 'cloud-ml-notice-text').element.textContent).toBe(body);
      // The heading comes before the body.
      const html = q(wrapper, 'cloud-ml-notice').html();
      expect(html.indexOf('cloud-ml-notice-heading')).toBeLessThan(html.indexOf('cloud-ml-notice-text'));
    });
  });

  describe("fallback notice: counsel's rev-1 text (no hub)", () => {
    const SECTIONS = [
      { label: 'What leaves your machine:', text: 'a record of your workflow activity: file paths, …' },
      { label: 'Where it goes:', text: "Kameas's cloud in Ohio, USA (AWS us-east-2), …" },
    ];
    const notice = (policy: string, policyText: string, actions: string[]) => ({
      heading: 'Your organization has enabled workflow data offload',
      intro: 'Acme Corp has turned on Offload for your account. Here is exactly what that means.',
      sections: SECTIONS,
      policyText,
      policy,
      actions,
    });
    const MEMBER =
      'You decide. Offload is off for you until you turn it on below. You can turn it off at any time in Settings → Offload.';
    const REQUIRED =
      'Your organization has made Offload required for all members. If you have questions about this, contact your organization\'s administrator.';
    const memberChoice = (over: Partial<MLStatus> = {}) =>
      status({
        orgOffloadEnabled: true,
        orgPolicy: 'member_choice',
        noticeVersion: 3,
        noticeText: 'plain',
        notice: notice('member_choice', MEMBER, ['Turn on Offload', 'Not now']),
        ...over,
      });
    const required = (over: Partial<MLStatus> = {}) =>
      status({
        orgOffloadEnabled: true,
        orgPolicy: 'on',
        noticeAckRequired: true,
        noticeVersion: 3,
        noticeText: 'plain',
        notice: notice('on', REQUIRED, ['I have read this']),
        ...over,
      });
    const actions = (w: any) => w.findAll('[data-testid="cloud-ml-notice-action"]');

    it('lays out heading, intro, bold labels and the member-choice paragraph', async () => {
      const { wrapper } = mountWith(memberChoice());
      await flushPromises();
      expect(q(wrapper, 'cloud-ml-notice-heading').text()).toBe('Your organization has enabled workflow data offload');
      expect(q(wrapper, 'cloud-ml-notice-intro').text()).toBe(
        'Acme Corp has turned on Offload for your account. Here is exactly what that means.',
      );
      const secs = wrapper.findAll('[data-testid="cloud-ml-notice-section"]');
      expect(secs.length).toBe(2);
      expect(secs[0].find('strong').text()).toBe('What leaves your machine:');
      expect(secs[0].text()).toBe('What leaves your machine: a record of your workflow activity: file paths, …');
      expect(q(wrapper, 'cloud-ml-notice-policy').text()).toBe(MEMBER);
      expect(actions(wrapper).map((b: any) => b.text())).toEqual(['Turn on Offload', 'Not now']);
      // The plain-text fallback and the legacy Acknowledge button are not shown.
      expect(q(wrapper, 'cloud-ml-notice-text').exists()).toBe(false);
      expect(q(wrapper, 'cloud-ml-ack').exists()).toBe(false);
    });

    it('"Turn on Offload" sets the opt-in, then acknowledges the version shown', async () => {
      const optedIn = memberChoice({ userWorkflowEventsOptedIn: true, noticeAckRequired: true, noticeVersion: 3 });
      const done = memberChoice({ userWorkflowEventsOptedIn: true, effective: true, noticeAckedAt: '2026-10-10T12:00:00Z' });
      const { wrapper, setWorkflowEventsOptIn, mlAckNotice, approveItem } = mountWith(memberChoice(), {
        optIn: async () => optedIn,
        ack: async () => done,
      });
      await flushPromises();
      await actions(wrapper)[0].trigger('click');
      await flushPromises();
      expect(setWorkflowEventsOptIn).toHaveBeenCalledWith(true);
      expect(mlAckNotice).toHaveBeenCalledWith(3, 'Turn on Offload');
      expect(setWorkflowEventsOptIn.mock.invocationCallOrder[0]).toBeLessThan(mlAckNotice.mock.invocationCallOrder[0]);
      expect(approveItem).not.toHaveBeenCalled();
      expect(q(wrapper, 'cloud-ml-effective').text()).toContain('On');
      expect(q(wrapper, 'cloud-ml-notice').exists()).toBe(false);
    });

    it('"Not now" closes the notice and makes no request', async () => {
      const { wrapper, setWorkflowEventsOptIn, mlAckNotice, approveItem } = mountWith(memberChoice());
      await flushPromises();
      await actions(wrapper)[1].trigger('click');
      await flushPromises();
      expect(q(wrapper, 'cloud-ml-notice').exists()).toBe(false);
      expect(setWorkflowEventsOptIn).not.toHaveBeenCalled();
      expect(mlAckNotice).not.toHaveBeenCalled();
      expect(approveItem).not.toHaveBeenCalled();
    });

    it('required: the required paragraph and "I have read this" acknowledges', async () => {
      const { wrapper, mlAckNotice, setWorkflowEventsOptIn } = mountWith(required());
      await flushPromises();
      expect(q(wrapper, 'cloud-ml-notice-policy').text()).toBe(REQUIRED);
      expect(actions(wrapper).map((b: any) => b.text())).toEqual(['I have read this']);
      await actions(wrapper)[0].trigger('click');
      await flushPromises();
      expect(mlAckNotice).toHaveBeenCalledWith(3, 'I have read this');
      expect(setWorkflowEventsOptIn).not.toHaveBeenCalled();
    });

    it('re-acknowledging (member_choice, Offload already on): "I have read this" acks, no opt-in write', async () => {
      const { wrapper, mlAckNotice, setWorkflowEventsOptIn } = mountWith(
        memberChoice({
          userWorkflowEventsOptedIn: true,
          noticeAckRequired: true,
          notice: notice('member_choice', MEMBER, ['I have read this']),
        }),
      );
      await flushPromises();
      expect(actions(wrapper).map((b: any) => b.text())).toEqual(['I have read this']);
      await actions(wrapper)[0].trigger('click');
      await flushPromises();
      expect(mlAckNotice).toHaveBeenCalledWith(3, 'I have read this');
      expect(setWorkflowEventsOptIn).not.toHaveBeenCalled();
    });

    it('the ML notice card never says agree / accept / consent (legal, 2026-10-16)', async () => {
      for (const s of [
        memberChoice(),
        required(),
        status({
          orgOffloadEnabled: true,
          orgPolicy: 'on',
          noticeAckRequired: true,
          noticeVersion: 3,
          noticeText: 'Hub body',
          noticeFromHub: true,
          noticeItemId: 'ml_notice:o1:3',
        }),
      ]) {
        const { wrapper } = mountWith(s);
        await flushPromises();
        const card = q(wrapper, 'cloud-ml-notice');
        expect(card.exists()).toBe(true);
        expect(card.text()).not.toMatch(/agree|accept|consent/i);
      }
    });

    it('hub branch: the acknowledge button reads "I have read this"', async () => {
      const { wrapper } = mountWith(
        status({
          orgOffloadEnabled: true,
          orgPolicy: 'on',
          noticeAckRequired: true,
          noticeVersion: 3,
          noticeText: 'Hub body',
          noticeFromHub: true,
          noticeItemId: 'ml_notice:o1:3',
        }),
      );
      await flushPromises();
      expect(q(wrapper, 'cloud-ml-ack').text()).toBe('I have read this');
    });

    it('a 409 on the ack re-shows the notice with the "changed" line', async () => {
      const { wrapper } = mountWith(required(), {
        ack: async () => required({ noticeVersion: 4, noticeChanged: true }),
      });
      await flushPromises();
      await actions(wrapper)[0].trigger('click');
      await flushPromises();
      expect(q(wrapper, 'cloud-ml-notice-changed').exists()).toBe(true);
      expect(q(wrapper, 'cloud-ml-notice-heading').exists()).toBe(true);
    });
  });
});
