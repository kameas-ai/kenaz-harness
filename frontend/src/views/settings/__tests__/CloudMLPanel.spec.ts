/**
 * CloudMLPanel — ml-producer-01MLPRD01 WP01 (spec §5).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import CloudMLPanel from '@/views/settings/CloudMLPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { MLStatus } from '@/lib/types';

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
  } = {},
) {
  const queue = [...(opts.statuses ?? [])];
  const mlStatus = vi.fn(async () => queue.shift() ?? initial);
  const mlAckNotice = vi.fn(opts.ack ?? (async () => status()));
  const setWorkflowEventsOptIn = vi.fn(opts.optIn ?? (async () => status()));
  const client = createFakeHarnessClient({
    fleet: { mlStatus, mlAckNotice, setWorkflowEventsOptIn } as any,
  });
  const wrapper = mount(CloudMLPanel, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  const openExternalURL = vi.fn();
  client.openExternalURL = openExternalURL;
  return { wrapper, mlStatus, mlAckNotice, setWorkflowEventsOptIn, openExternalURL };
}

const q = (w: any, id: string) => w.find(`[data-testid="${id}"]`);

describe('CloudMLPanel', () => {
  it('lists the org exclusions read-only (WP05)', async () => {
    const { wrapper } = mountWith(
      status({
        effective: true,
        exclusionPaths: ['hr/**', '**/secrets/*'],
        exclusionCommands: ['ssh', 'git push'],
        excludeBrowser: true,
        legacyExclusionNotes: ['Nothing from the HR share'],
      }),
    );
    await flushPromises();
    const block = q(wrapper, 'cloud-ml-exclusions');
    expect(block.exists()).toBe(true);
    expect(block.text()).toContain('Your organization excludes:');
    const paths = wrapper.findAll('[data-testid="cloud-ml-exclusion-path"]').map((w) => w.text());
    expect(paths).toEqual(['files matching hr/**', 'files matching **/secrets/*']);
    const cmds = wrapper.findAll('[data-testid="cloud-ml-exclusion-command"]').map((w) => w.text());
    expect(cmds).toEqual(['commands starting with ssh', 'commands starting with git push']);
    // Read-only: no inputs inside the exclusions block.
    expect(block.findAll('input').length).toBe(0);
    expect(block.findAll('button').length).toBe(0);
    const notes = q(wrapper, 'cloud-ml-legacy-notes');
    expect(notes.exists()).toBe(true);
    expect(notes.text()).toContain('not applied as patterns');
    expect(wrapper.findAll('[data-testid="cloud-ml-legacy-note"]').map((w) => w.text())).toEqual([
      'Nothing from the HR share',
    ]);
  });

  it('shows no exclusions block when the org excludes nothing', async () => {
    const { wrapper } = mountWith(status({ effective: true, excludeBrowser: true }));
    await flushPromises();
    expect(q(wrapper, 'cloud-ml-exclusions').exists()).toBe(false);
    expect(q(wrapper, 'cloud-ml-legacy-notes').exists()).toBe(false);
  });

  it('shows no exclusions while /me/ml is not loaded', async () => {
    const { wrapper } = mountWith(
      status({ loaded: false, fleetError: 'boom', exclusionPaths: ['hr/**'], legacyExclusionNotes: ['x'] }),
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

  it('notice required: shows the notice verbatim + harness line; Acknowledge posts the shown version', async () => {
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
    expect(mlAckNotice).toHaveBeenCalledWith(3);
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
    expect(mlAckNotice).toHaveBeenCalledWith(3);
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
});
