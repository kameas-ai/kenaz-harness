/**
 * MigrationDriftPanel tests — v0.5.1 migration-doctor UI.
 *
 * Covers:
 *   1. Empty / clean state — "No drift · 0 pending" green pill + clean note
 *   2. Entry rendering — id_mismatch entry shown with ledger/expected IDs
 *   3. Fix button click — calls client.storage.applyDriftFix with the version
 *   4. Load error — surfaces error message when getMigrationDriftReport rejects
 *   5. Pending-only state — amber "N pending (no drift)" pill (FR-005)
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import MigrationDriftPanel from '@/views/settings/health/MigrationDriftPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { DriftEntry, DriftReport } from '@/lib/types';

function makeDriftEntry(overrides: Partial<DriftEntry> = {}): DriftEntry {
  return {
    version: 322,
    ledgerId: 'old-migration-id',
    expectedId: 'new-migration-id',
    kind: 'id_mismatch',
    severity: 'error',
    suggestion: 'Apply automatic fix to update the ledger ID.',
    ...overrides,
  };
}

function provideClient(
  report: DriftReport = { drifts: [] },
  applyDriftFixSpy?: ReturnType<typeof vi.fn>,
) {
  const getMigrationDriftReport = vi.fn().mockResolvedValue(report);
  const applyDriftFix =
    applyDriftFixSpy ?? vi.fn().mockResolvedValue(undefined);
  const client = createFakeHarnessClient({
    storage: {
      getMigrationDriftReport,
      applyDriftFix,
    },
  });
  return { client, getMigrationDriftReport, applyDriftFix };
}

describe('MigrationDriftPanel', () => {
  it('renders clean / no-drift state when report has no drifts', async () => {
    const { client } = provideClient({ drifts: [] });
    const w = mount(MigrationDriftPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    // Green "No drift · 0 pending" pill (FR-005: no longer "No drift detected"
    // to avoid contradiction when pending-count note is visible below)
    expect(w.find('[data-testid="drift-status-ok"]').exists()).toBe(true);
    expect(w.find('[data-testid="drift-status-ok"]').text()).toContain(
      'No drift',
    );

    // Clean note text
    expect(w.find('[data-testid="drift-clean-note"]').exists()).toBe(true);
    expect(w.text()).toContain('All registered migrations match the ledger');
  });

  it('renders id_mismatch entry with ledger / expected IDs', async () => {
    const entry = makeDriftEntry();
    const { client } = provideClient({ drifts: [entry] });
    const w = mount(MigrationDriftPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    // Entry exists
    expect(w.find(`[data-testid="drift-entry-${entry.version}"]`).exists()).toBe(
      true,
    );

    // IDs rendered
    const idsBlock = w.find(`[data-testid="drift-ids-${entry.version}"]`);
    expect(idsBlock.exists()).toBe(true);
    expect(
      w.find(`[data-testid="drift-ledger-id-${entry.version}"]`).text(),
    ).toBe(entry.ledgerId);
    expect(
      w.find(`[data-testid="drift-expected-id-${entry.version}"]`).text(),
    ).toBe(entry.expectedId);

    // Suggestion text
    expect(
      w.find(`[data-testid="drift-suggestion-${entry.version}"]`).text(),
    ).toBe(entry.suggestion);

    // Fix button visible
    expect(
      w.find(`[data-testid="drift-fix-btn-${entry.version}"]`).exists(),
    ).toBe(true);
  });

  it('calls applyDriftFix with the correct version when fix button is clicked', async () => {
    const entry = makeDriftEntry({ version: 322 });
    const applyDriftFix = vi.fn().mockResolvedValue(undefined);
    // After fix, reload returns no drifts so the component re-loads cleanly.
    const getMigrationDriftReport = vi
      .fn()
      .mockResolvedValueOnce({ drifts: [entry] })
      .mockResolvedValue({ drifts: [] });
    const client = createFakeHarnessClient({
      storage: { getMigrationDriftReport, applyDriftFix },
    });

    const w = mount(MigrationDriftPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    const fixBtn = w.find(`[data-testid="drift-fix-btn-${entry.version}"]`);
    expect(fixBtn.exists()).toBe(true);
    await fixBtn.trigger('click');
    await flushPromises();

    expect(applyDriftFix).toHaveBeenCalledTimes(1);
    expect(applyDriftFix).toHaveBeenCalledWith(322);
  });

  it('surfaces load error when getMigrationDriftReport rejects', async () => {
    const getMigrationDriftReport = vi
      .fn()
      .mockRejectedValue(new Error('DB unavailable'));
    const client = createFakeHarnessClient({
      storage: {
        getMigrationDriftReport,
        applyDriftFix: vi.fn(),
      },
    });

    const w = mount(MigrationDriftPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(w.find('[data-testid="drift-load-error"]').exists()).toBe(true);
    expect(w.find('[data-testid="drift-load-error"]').text()).toContain(
      'DB unavailable',
    );
    expect(w.find('[data-testid="drift-status-error"]').exists()).toBe(true);
  });

  // FR-005: "No drift detected" + "8 pending" was contradictory.
  // When only code_only (pending) drifts exist: amber "N pending (no drift)" pill.
  it('shows amber pending pill when only code_only drifts are present (FR-005)', async () => {
    const pendingEntry = makeDriftEntry({
      version: 200,
      kind: 'code_only',
      severity: 'info',
      suggestion: 'Will auto-apply on next boot.',
    });
    const { client } = provideClient({ drifts: [pendingEntry] });
    const w = mount(MigrationDriftPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    // Amber pending pill, not the green "no drift" pill
    expect(w.find('[data-testid="drift-status-pending"]').exists()).toBe(true);
    expect(w.find('[data-testid="drift-status-pending"]').text()).toContain('1 pending (no drift)');
    // Green pill must NOT show
    expect(w.find('[data-testid="drift-status-ok"]').exists()).toBe(false);
    // Pending note also visible below the card
    expect(w.find('[data-testid="drift-pending-note"]').exists()).toBe(true);
  });

  it('does not render fix button for ledger_only entries', async () => {
    const entry = makeDriftEntry({
      version: 100,
      kind: 'ledger_only',
      severity: 'warning',
      suggestion: 'Manual inspection required.',
    });
    const { client } = provideClient({ drifts: [entry] });
    const w = mount(MigrationDriftPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    // Entry is shown
    expect(w.find(`[data-testid="drift-entry-${entry.version}"]`).exists()).toBe(
      true,
    );
    // Fix button is NOT shown for ledger_only
    expect(
      w.find(`[data-testid="drift-fix-btn-${entry.version}"]`).exists(),
    ).toBe(false);
  });
});
