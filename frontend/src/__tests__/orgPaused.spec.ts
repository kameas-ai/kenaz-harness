/**
 * orgPaused.spec.ts — the staff "pause paid features" org state
 * (kenaz-fleet PR 206) across the fleet-health surfaces.
 *
 * Pins: the copy table (one headline, a one-liner per category, unknown →
 * other), that NO surface on the paused path carries upsell copy (upgrade /
 * plan / Pro+ / Team+ / subscription / tier), that the paused state
 * short-circuits the existing tier gates, and that the data-rights actions
 * (turn off + delete from Fleet) stay enabled while paused.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import OrgPausedBanner from '@/components/ui/OrgPausedBanner.vue';
import AccountPanel from '@/views/settings/AccountPanel.vue';
import CompliancePanel from '@/views/settings/CompliancePanel.vue';
import MemorySyncPanel from '@/views/settings/MemorySyncPanel.vue';
import FleetHealthChip from '@/shell/FleetHealthChip.vue';
import { createFakeHarnessClient, fakeFleetSession } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import {
  _resetFleetSessionForTest,
  applyFleetSession,
  describeFleetReason,
  describeSyncReason,
  fleetDegradedLanes,
} from '@/lib/fleetSession';
import {
  ORG_PAUSED_CATEGORIES,
  ORG_PAUSED_CATEGORY_COPY,
  ORG_PAUSED_TITLE,
  normalizeOrgPausedCategory,
  orgPausedCategoryLine,
} from '@/lib/orgPausedCopy';
import type { FleetSessionView, MemorySyncStatus } from '@/lib/types';

/** Words that mean "pay more" — forbidden anywhere on the paused path. */
const UPSELL = /upgrade|your plan|current plan|pro\+|team\+|subscription|not included|tier/i;

function pausedSession(category = 'billing_review', over: Partial<FleetSessionView> = {}): FleetSessionView {
  return fakeFleetSession({
    state: 'signed_in',
    identity: { userId: 'u', orgId: 'o', teamId: 't', email: 'a@example.com', orgName: 'Org' },
    tokensUsable: true,
    paused: true,
    pausedCategory: category,
    ...over,
  });
}

function sessionClient(s: FleetSessionView, extra: Record<string, unknown> = {}) {
  return createFakeHarnessClient({
    settings: {
      fleetSession: vi.fn(async () => s),
      fleetProfile: vi.fn(async () => s.profile),
      fleetHealth: vi.fn(async () => ({
        configDistributionEnabled: true,
        configSource: 'fleet',
        configLastError: '',
        signedIn: true,
      })),
      fleetRefreshIdentity: vi.fn(async () => s.identity),
    } as any,
    ...extra,
  });
}

beforeEach(() => _resetFleetSessionForTest());

// ── the copy table ──────────────────────────────────────────────────────────

describe('org paused copy table', () => {
  it('has one headline and a one-liner for every fleet category', () => {
    expect(ORG_PAUSED_TITLE).toBe(
      "Paused by your organization's account status — contact your admin",
    );
    expect([...ORG_PAUSED_CATEGORIES].sort()).toEqual(
      ['abuse', 'billing_review', 'legal', 'other', 'security'],
    );
    for (const c of ORG_PAUSED_CATEGORIES) {
      expect(ORG_PAUSED_CATEGORY_COPY[c]).toBeTruthy();
      expect(orgPausedCategoryLine(c)).toBe(ORG_PAUSED_CATEGORY_COPY[c]);
    }
    // Distinct lines: a category is never silently rendered as another.
    expect(new Set(Object.values(ORG_PAUSED_CATEGORY_COPY)).size).toBe(5);
  });

  it('maps an absent or unknown category to "other"', () => {
    expect(normalizeOrgPausedCategory(undefined)).toBe('other');
    expect(normalizeOrgPausedCategory('')).toBe('other');
    expect(normalizeOrgPausedCategory('tax_hold')).toBe('other');
    expect(orgPausedCategoryLine('tax_hold')).toBe(ORG_PAUSED_CATEGORY_COPY.other);
  });

  it('names org_paused as a session reason without tier copy', () => {
    expect(describeFleetReason('org_paused')).toBe("Paused by your organization's account status");
  });

  it('never carries upsell copy', () => {
    for (const s of [ORG_PAUSED_TITLE, ...Object.values(ORG_PAUSED_CATEGORY_COPY), describeSyncReason('org_paused')]) {
      expect(s).not.toMatch(UPSELL);
    }
  });
});

// ── the banner ──────────────────────────────────────────────────────────────

describe('OrgPausedBanner', () => {
  it('is hidden while not paused', () => {
    applyFleetSession(fakeFleetSession({ state: 'signed_in' }));
    const w = mount(OrgPausedBanner);
    expect(w.find('[data-testid="org-paused-banner"]').exists()).toBe(false);
  });

  it.each(ORG_PAUSED_CATEGORIES)('renders the headline + the %s one-liner from the session', (c) => {
    applyFleetSession(pausedSession(c));
    const w = mount(OrgPausedBanner);
    expect(w.find('[data-testid="org-paused-title"]').text()).toBe(ORG_PAUSED_TITLE);
    expect(w.find('[data-testid="org-paused-category"]').text()).toBe(ORG_PAUSED_CATEGORY_COPY[c]);
    expect(w.text()).not.toMatch(UPSELL);
  });
});

// ── fleet session helpers ───────────────────────────────────────────────────

describe('fleet session while paused', () => {
  it('hides org_paused / not_entitled lanes behind the banner (no tier copy)', () => {
    const lane = { status: 'off', reason: 'org_paused', consecutiveFailures: 0 };
    const s = pausedSession('security');
    s.sync = {
      contextSync: { status: 'degraded', reason: 'org_paused', consecutiveFailures: 1 },
      unitPoll: lane,
      telemetry: { status: 'unknown', consecutiveFailures: 0 },
      catalogRevocation: { status: 'degraded', reason: 'not_entitled', consecutiveFailures: 1 },
    };
    applyFleetSession(s);
    expect(fleetDegradedLanes.value).toEqual([]);
  });
});

// ── surfaces ────────────────────────────────────────────────────────────────

describe('Settings › Account while paused', () => {
  it('shows the paused banner and no upsell copy', async () => {
    const s = pausedSession('legal');
    const w = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: sessionClient(s) } },
    });
    await flushPromises();
    const banner = w.find('[data-testid="org-paused-banner"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain(ORG_PAUSED_CATEGORY_COPY.legal);
    expect(w.text()).not.toMatch(UPSELL);
  });
});

describe('fleet health chip while paused', () => {
  it('says "paused by org" in warn style with the banner copy as tooltip', async () => {
    const s = pausedSession('abuse');
    const w = mount(FleetHealthChip, {
      global: { provide: { [HarnessClientKey as symbol]: sessionClient(s) } },
    });
    await flushPromises();
    const chip = w.find('[data-testid="fleet-health-chip"]');
    expect(chip.text()).toBe('fleet: paused by org');
    expect(chip.classes()).toContain('text-signal-warn');
    expect(chip.attributes('title')).toContain(ORG_PAUSED_TITLE);
    expect(chip.attributes('title')).toContain(ORG_PAUSED_CATEGORY_COPY.abuse);
    expect(chip.attributes('title')).not.toMatch(UPSELL);
  });
});

describe('Compliance panel while paused', () => {
  it('short-circuits "Not available on your current plan / Upgrade" with the paused banner', async () => {
    applyFleetSession(pausedSession('billing_review'));
    const client = createFakeHarnessClient({
      compliance: {
        status: vi.fn(async () => ({
          lastArchivedAt: '', pendingCount: 0, chainBreak: false, retentionDays: 90,
          enabled: false, archiverRunning: false,
        })),
      } as any,
    });
    const w = mount(CompliancePanel, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();
    expect(w.find('[data-testid="org-paused-banner"]').exists()).toBe(true);
    expect(w.find('[data-testid="compliance-disabled-notice"]').exists()).toBe(false);
    expect(w.text()).not.toMatch(UPSELL);
  });
});

describe('Memory sync panel while paused', () => {
  function status(over: Partial<MemorySyncStatus> = {}): MemorySyncStatus {
    return {
      wired: true, entitled: false, enabled: true, scopes: [], consentVersion: '',
      optedInAt: '', currentConsentVersion: '2026-10-07', liveRecords: 0, liveBytes: 0,
      maxRecords: 0, maxBytes: 0, blockedCount: 0, pendingCount: 0,
      lane: { status: 'off', reason: 'org_paused', consecutiveFailures: 0 },
      orgPaused: true, pausedCategory: 'security',
      ...over,
    };
  }
  function mountMem(s: MemorySyncStatus) {
    const memorySyncDisable = vi.fn(async () => status({ enabled: false }));
    const client = createFakeHarnessClient({
      fleet: { memorySyncStatus: vi.fn(async () => s), memorySyncDisable } as any,
    });
    const w = mount(MemorySyncPanel, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    return { w, memorySyncDisable };
  }

  it('stays visible with the banner and keeps "turn off + delete from Fleet" enabled', async () => {
    const { w, memorySyncDisable } = mountMem(status());
    await flushPromises();
    expect(w.find('[data-testid="memory-sync-panel"]').exists()).toBe(true);
    expect(w.find('[data-testid="org-paused-category"]').text()).toBe(ORG_PAUSED_CATEGORY_COPY.security);
    expect(w.text()).not.toMatch(UPSELL);

    const off = w.find('[data-testid="memory-sync-disable"]');
    expect(off.exists()).toBe(true);
    expect(off.attributes('disabled')).toBeUndefined();
    await off.trigger('click');
    await w.find('[data-testid="memory-sync-delete-fleet"]').setValue(true);
    await w.find('[data-testid="memory-sync-confirm-text"]').setValue('forget-all');
    const confirm = w.find('[data-testid="memory-sync-disable-confirm-button"]');
    expect(confirm.attributes('disabled')).toBeUndefined();
    await confirm.trigger('click');
    await flushPromises();
    expect(memorySyncDisable).toHaveBeenCalledWith(true, 'forget-all');
  });

  it('does not offer turning sync ON (a paid action) while paused', async () => {
    const { w } = mountMem(status({ enabled: false }));
    await flushPromises();
    expect(w.find('[data-testid="org-paused-banner"]').exists()).toBe(true);
    expect(w.find('[data-testid="memory-sync-enable"]').exists()).toBe(false);
  });
});
