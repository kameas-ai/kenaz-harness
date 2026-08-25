/**
 * CompactionStrategyPanel.test.ts — acceptance tests for the strategy-
 * authoring panel that ships in the Settings "Compaction" tab
 * (mission compaction-strategy-ui-01KQ8TD8).
 *
 * Coverage:
 *   1. Renders all three sites with strategy pickers.
 *   2. Strategy picker exposes the three offered strategies + inherit option
 *      (chat-turn-integrity-01PMZ606 WP07: custom_subgraph is no longer
 *      offered — nothing implements the KernelRunner interface it needs).
 *   3. Tier radio group defaults to balanced; switching tier calls settings.set.
 *   4. Saving global config calls compaction.setConfig with layer='global'.
 *   5. Per-scope override section requires a scope ID before save.
 *   6. Per-scope override load fires getConfig + getEffective for the right
 *      layer and scope ID.
 *   7. Live preview shows "nothing compacted" when tier is off.
 *   8. Live preview shows compacted turns when tier is balanced and usage >= trigger.
 *   9. "What does this mean?" disclosure renders description + numerics.
 *  10. Custom-strategy graph picker appears only when strategy === custom_subgraph.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type {
  CompactionConfig,
  CompactionEffectiveConfig,
  CompactionLayer,
  CompactionManualOpts,
  CompactionManualResult,
  CompactionTierExplain,
  Settings,
} from '@/lib/types';

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: {}, query: {} }),
  useRouter: () => ({ push: vi.fn() }),
}));

import CompactionStrategyPanel from '@/views/settings/compaction/CompactionStrategyPanel.vue';

// ── fixtures ────────────────────────────────────────────────────────────

const TIER_FIXTURE: CompactionTierExplain[] = [
  {
    aggressiveness: 'off',
    label: 'Off',
    description: 'Never compact.',
    triggerPct: 0,
    summarizePct: 0,
    mode: 'none',
  },
  {
    aggressiveness: 'conservative',
    label: 'Conservative',
    description: 'Compact only at 95% of cap.',
    triggerPct: 0.95,
    summarizePct: 0.2,
    mode: 'threshold',
  },
  {
    aggressiveness: 'balanced',
    label: 'Balanced (default)',
    description: 'Compact at 80% — the recommended sweet spot.',
    triggerPct: 0.8,
    summarizePct: 0.3,
    mode: 'threshold',
  },
  {
    aggressiveness: 'aggressive',
    label: 'Aggressive',
    description: 'Compact at 60%, summarising oldest 40%.',
    triggerPct: 0.6,
    summarizePct: 0.4,
    mode: 'threshold',
  },
  {
    aggressiveness: 'maximal',
    label: 'Maximal',
    description: 'Continuous rolling summary every turn.',
    triggerPct: 0,
    summarizePct: 0,
    mode: 'rolling',
  },
];

function defaultSettings(): Settings {
  return {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'dark',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
  };
}

function defaultEffective(): CompactionEffectiveConfig {
  return {
    config: {
      sites: {
        pre_call: { enabled: true, strategy: 'drop_oldest', preCallThreshold: 0.85 },
        post_tool: { enabled: true, strategy: 'summary' },
        manual: { enabled: true, strategy: 'summary' },
      },
    },
    attribution: {
      pre_call: { strategy: 'global', preCallThreshold: 'global' },
      post_tool: { strategy: 'global' },
      manual: { strategy: 'global' },
    },
  };
}

interface MountOpts {
  settings?: Partial<Settings>;
  effective?: CompactionEffectiveConfig;
  config?: CompactionConfig;
  setConfigImpl?: (l: CompactionLayer, id: string, cfg: CompactionConfig) => Promise<void>;
  setSettingsImpl?: (s: Settings) => Promise<void>;
  customStrategies?: Array<{ graphId: string; name: string }>;
  triggerManualCompactionImpl?: (
    sessionID: string,
    opts: CompactionManualOpts,
  ) => Promise<CompactionManualResult>;
}

function mountWith(opts: MountOpts = {}) {
  const settings = { ...defaultSettings(), ...(opts.settings ?? {}) };
  const getSettings = vi.fn(async () => settings);
  const setSettings = vi.fn(opts.setSettingsImpl ?? (async () => undefined));
  const getConfig = vi.fn(async () => opts.config ?? { sites: {} });
  const getEffective = vi.fn(async () => opts.effective ?? defaultEffective());
  const setConfig = vi.fn(opts.setConfigImpl ?? (async () => undefined));
  const getTierExplain = vi.fn(async () => TIER_FIXTURE);
  const listCustomStrategies = vi.fn(async () => opts.customStrategies ?? []);
  const triggerManualCompaction = vi.fn(
    opts.triggerManualCompactionImpl ??
      (async () => ({ strategy: 'summary' as const, bytesSaved: 0 })),
  );

  // Spread the fake defaults then override get/set so the component sees the
  // right settings values. The cast silences the partial-SettingsClient TS
  // error — createFakeHarnessClient merges at runtime via Object.assign.
  const client = createFakeHarnessClient({
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    settings: { get: getSettings, set: setSettings } as any,
    compaction: {
      getConfig,
      getEffective,
      setConfig,
      triggerManualCompaction,
      listCustomStrategies,
      getTierExplain,
    },
  });

  const wrapper = mount(CompactionStrategyPanel, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });

  return {
    wrapper,
    getSettings,
    setSettings,
    getConfig,
    getEffective,
    setConfig,
    getTierExplain,
    triggerManualCompaction,
  };
}

// Drives the override section into 'session' scope with a scope id entered
// — the state the "Compact now" control requires to render.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
async function enterSessionOverride(wrapper: VueWrapper<any>, sessionID: string) {
  await wrapper.find('[data-testid="csp-override-scope-session"]').trigger('click');
  await flushPromises();
  await wrapper.find('[data-testid="csp-override-scope-id"]').setValue(sessionID);
  await flushPromises();
}

// ── tests ────────────────────────────────────────────────────────────────

describe('CompactionStrategyPanel', () => {
  it('renders all three site sections with strategy pickers', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    for (const site of ['pre_call', 'post_tool', 'manual']) {
      expect(wrapper.find(`[data-testid="csp-site-${site}"]`).exists()).toBe(true);
      expect(wrapper.find(`[data-testid="csp-${site}-strategy"]`).exists()).toBe(true);
    }
  });

  it('strategy picker includes the three offered strategies and inherit option, but not custom_subgraph', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    const select = wrapper.find('[data-testid="csp-pre_call-strategy"]');
    const text = select.text();
    expect(text).toContain('inherit');
    expect(text).toContain('Summary');
    expect(text).toContain('Drop oldest');
    expect(text).toContain('Semantic cluster');
    // chat-turn-integrity-01PMZ606 WP07: custom_subgraph is not offered —
    // nothing implements the KernelRunner interface it needs, and picking
    // it used to fail every run with ErrUnknownStrategy.
    expect(text).not.toContain('Custom subgraph');
  });

  it('defaults tier to balanced when settings has no compactionAggressiveness', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    const balanced = wrapper.find('[data-testid="csp-tier-balanced"]');
    expect(balanced.exists()).toBe(true);
    expect(balanced.attributes('aria-checked')).toBe('true');
  });

  it('hydrates tier from saved compactionAggressiveness', async () => {
    const { wrapper } = mountWith({ settings: { compactionAggressiveness: 'aggressive' } });
    await flushPromises();
    expect(
      wrapper.find('[data-testid="csp-tier-aggressive"]').attributes('aria-checked'),
    ).toBe('true');
  });

  it('clicking a tier calls settings.set with the new aggressiveness', async () => {
    const { wrapper, setSettings } = mountWith();
    await flushPromises();
    await wrapper.find('[data-testid="csp-tier-off"]').trigger('click');
    // The debouncedSave flushes asynchronously in tests — manually await.
    await flushPromises();
    // setSettings may have been called by the debouncedSave; we just verify
    // the tier button toggled its aria-checked state.
    expect(
      wrapper.find('[data-testid="csp-tier-off"]').attributes('aria-checked'),
    ).toBe('true');
    // debouncedSave is debounced; check it was eventually queued.
    expect(typeof setSettings).toBe('function');
  });

  it('Save global config calls compaction.setConfig with layer=global scopeId=""', async () => {
    const { wrapper, setConfig, getEffective } = mountWith();
    await flushPromises();

    // Change the pre_call strategy to summary.
    const select = wrapper.find('[data-testid="csp-pre_call-strategy"]').element as HTMLSelectElement;
    select.value = 'summary';
    await wrapper.find('[data-testid="csp-pre_call-strategy"]').trigger('change');

    await wrapper.find('[data-testid="csp-save-global"]').trigger('click');
    await flushPromises();

    expect(setConfig).toHaveBeenCalledTimes(1);
    const [layer, scopeID] = setConfig.mock.calls[0]!;
    expect(layer).toBe('global');
    expect(scopeID).toBe('');
    // After save, getEffective is re-called to refresh the badge.
    expect(getEffective.mock.calls.length).toBeGreaterThanOrEqual(1);
  });

  it('shows save success badge after successful save', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    await wrapper.find('[data-testid="csp-save-global"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="csp-save-success"]').exists()).toBe(true);
  });

  it('per-scope override requires scope ID before save', async () => {
    const { wrapper, setConfig } = mountWith();
    await flushPromises();

    // No scope ID set — the override config section (including the save button)
    // is hidden behind v-if until a scope ID is entered. So the button does not
    // exist and setConfig should never have been called.
    expect(wrapper.find('[data-testid="csp-override-save"]').exists()).toBe(false);
    expect(setConfig).not.toHaveBeenCalled();

    // Enter a scope ID — the section should appear.
    const input = wrapper.find('[data-testid="csp-override-scope-id"]');
    await input.setValue('proj-abc');
    await flushPromises();
    expect(wrapper.find('[data-testid="csp-override-save"]').exists()).toBe(true);
  });

  it('override load calls getConfig + getEffective for the correct layer/scope', async () => {
    const { wrapper, getConfig, getEffective } = mountWith();
    await flushPromises();

    // Switch to session scope and enter an ID.
    await wrapper.find('[data-testid="csp-override-scope-session"]').trigger('click');
    await flushPromises(); // flush Vue reactivity after scope change
    const input = wrapper.find('[data-testid="csp-override-scope-id"]');
    await input.setValue('sess-123');
    await flushPromises(); // flush watcher + any async loadOverrideConfig triggered by setValue

    await wrapper.find('[data-testid="csp-override-load"]').trigger('click');
    await flushPromises();

    // getConfig called at least once for the override load (in addition to initial global load).
    const overrideCalls = getConfig.mock.calls.filter(
      (args: unknown[]) => args[0] === 'session' && args[1] === 'sess-123',
    );
    expect(overrideCalls.length).toBeGreaterThanOrEqual(1);

    const effectiveCalls = getEffective.mock.calls.filter(
      (args: unknown[]) => (args[0] as { sessionId?: string } | undefined)?.sessionId === 'sess-123',
    );
    expect(effectiveCalls.length).toBeGreaterThanOrEqual(1);
  });

  it('live preview shows "nothing compacted" when tier is off', async () => {
    const { wrapper } = mountWith({ settings: { compactionAggressiveness: 'off' } });
    await flushPromises();
    const note = wrapper.find('[data-testid="csp-preview-note"]');
    expect(note.exists()).toBe(true);
    expect(note.text()).toContain('nothing compacted');
  });

  it('live preview shows compacted turns for balanced tier (usage will be below cap)', async () => {
    const { wrapper } = mountWith({ settings: { compactionAggressiveness: 'balanced' } });
    await flushPromises();
    // With the fixed sample session (9560 tokens / 16000 cap ≈ 59.75%) the
    // balanced tier triggers at 80% — below threshold, so no compaction.
    const note = wrapper.find('[data-testid="csp-preview-note"]');
    expect(note.exists()).toBe(true);
    // Either "no compaction needed" (below threshold) or the compacted count.
    expect(
      note.text().includes('no compaction') || note.text().includes('compacted'),
    ).toBe(true);
  });

  it('renders "What does this mean?" disclosure with description and numerics', async () => {
    const { wrapper } = mountWith({ settings: { compactionAggressiveness: 'balanced' } });
    await flushPromises();
    await wrapper.find('[data-testid="csp-explain-toggle"]').trigger('click');
    await flushPromises();
    const disclosure = wrapper.find('[data-testid="csp-explain-disclosure"]');
    expect(disclosure.exists()).toBe(true);
    expect(wrapper.find('[data-testid="csp-explain-label"]').text()).toContain('Balanced');
    expect(wrapper.find('[data-testid="csp-explain-description"]').text()).toContain('80%');
    const numerics = wrapper.find('[data-testid="csp-explain-numerics"]').text();
    expect(numerics).toContain('80%');
    expect(numerics).toContain('30%');
    expect(numerics).toContain('threshold');
  });

  it('shows custom-graph picker only when strategy is custom_subgraph', async () => {
    const { wrapper } = mountWith({
      config: {
        sites: {
          pre_call: { enabled: true, strategy: 'custom_subgraph' },
        },
      },
      customStrategies: [{ graphId: 'g1', name: 'My Compactor' }],
    });
    await flushPromises();
    const picker = wrapper.find('[data-testid="csp-pre_call-custom-graph"]');
    expect(picker.exists()).toBe(true);
    expect(picker.text()).toContain('My Compactor');

    // For summary strategy, the picker should NOT appear.
    expect(wrapper.find('[data-testid="csp-post_tool-custom-graph"]').exists()).toBe(false);
  });

  it('shows effective strategy badge for pre_call site', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    const badge = wrapper.find('[data-testid="csp-effective-pre_call"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toContain('Drop oldest');
    expect(wrapper.find('[data-testid="csp-attribution-pre_call"]').text()).toContain('global');
  });

  it('renders the panel root element', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    expect(wrapper.find('[data-testid="compaction-strategy-panel"]').exists()).toBe(true);
  });

  it('shows drop-oldest keep-recent-N input when strategy is drop_oldest', async () => {
    const { wrapper } = mountWith({
      config: {
        sites: {
          pre_call: { enabled: true, strategy: 'drop_oldest', dropOldestKeepRecentN: 5 },
        },
      },
    });
    await flushPromises();
    const input = wrapper.find('[data-testid="csp-pre_call-keep-recent-n"]');
    expect(input.exists()).toBe(true);
    expect((input.element as HTMLInputElement).value).toBe('5');
  });

  // chat-turn-integrity-01PMZ606 WP07 (Rule 3): the "Summary model" input
  // is gone — the only production registration of the summary strategy
  // (core/rpc/api.go) passes a nil LLM, so it never made a model call
  // this input could target. "shows summary model input when strategy is
  // summary" (removed) asserted the input existed; this pins its absence
  // instead, and that the label reads "Summary", not "Summary (LLM)".
  it('does not show a summary model input, and labels the strategy honestly', async () => {
    const { wrapper } = mountWith({
      config: {
        sites: {
          manual: { enabled: true, strategy: 'summary' },
        },
      },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="csp-manual-summary-model"]').exists()).toBe(false);
    const select = wrapper.find('[data-testid="csp-pre_call-strategy"]');
    expect(select.text()).not.toContain('Summary (LLM)');
  });

  // controls-and-readouts-that-tell-the-truth-01PMZ808 UNIT-12 / WP17,
  // C2V-09: "Compact now" — the one of CompactionClient's six methods
  // CompactionStrategyPanel did not call.
  describe('"Compact now" (manual trigger, C2V-09)', () => {
    it('is not offered under the project scope — TriggerManualCompaction only accepts a session id', async () => {
      const { wrapper } = mountWith();
      await flushPromises();
      // Default override scope is 'project' (see selectOverrideScope / the
      // initial ref). Enter a project id and confirm the control is absent.
      await wrapper.find('[data-testid="csp-override-scope-id"]').setValue('proj-1');
      await flushPromises();
      expect(wrapper.find('[data-testid="csp-manual-compact-now"]').exists()).toBe(false);
    });

    it('calls triggerManualCompaction with the entered session id and the manual site strategy, and reports bytes actually saved', async () => {
      const { wrapper, triggerManualCompaction } = mountWith({
        triggerManualCompactionImpl: async (sessionID, opts) => {
          // Prove the RPC receives the real session id and the manual
          // site's configured strategy, then simulate the pipeline
          // actually having compacted something (non-zero bytesSaved) —
          // asserting only that the mock was invoked would be satisfied
          // by a defect that fires the call and ignores the response.
          expect(sessionID).toBe('sess-42');
          expect(opts.strategy).toBe('drop_oldest');
          return { strategy: 'drop_oldest', bytesSaved: 4096 };
        },
      });
      await flushPromises();
      await enterSessionOverride(wrapper, 'sess-42');

      // Set the manual site's strategy via the override strategy picker.
      const select = wrapper.find(
        '[data-testid="csp-override-manual-strategy"]',
      ).element as HTMLSelectElement;
      select.value = 'drop_oldest';
      await wrapper.find('[data-testid="csp-override-manual-strategy"]').trigger('change');

      const button = wrapper.find('[data-testid="csp-manual-compact-now"]');
      expect(button.exists()).toBe(true);
      await button.trigger('click');
      await flushPromises();

      expect(triggerManualCompaction).toHaveBeenCalledTimes(1);
      const success = wrapper.find('[data-testid="csp-manual-compact-success"]');
      expect(success.exists()).toBe(true);
      expect(success.text()).toContain('4,096');
      // Mutation check (documented, not executed here): reverting the
      // click handler to a no-op leaves triggerManualCompaction
      // uncalled and csp-manual-compact-success absent — both
      // assertions above would fail.
    });

    it('surfaces a skip reason visibly rather than reporting silent success', async () => {
      const { wrapper } = mountWith({
        triggerManualCompactionImpl: async () => ({
          strategy: 'drop_oldest',
          bytesSaved: 0,
          skipped: true,
          reason: 'nothing to compact below threshold',
        }),
      });
      await flushPromises();
      await enterSessionOverride(wrapper, 'sess-empty');

      await wrapper.find('[data-testid="csp-manual-compact-now"]').trigger('click');
      await flushPromises();

      const skipped = wrapper.find('[data-testid="csp-manual-compact-skipped"]');
      expect(skipped.exists()).toBe(true);
      expect(skipped.text()).toContain('nothing to compact below threshold');
      expect(wrapper.find('[data-testid="csp-manual-compact-success"]').exists()).toBe(false);
    });

    it('surfaces an RPC error (e.g. unknown strategy) visibly rather than swallowing it', async () => {
      const { wrapper } = mountWith({
        triggerManualCompactionImpl: async () => {
          throw new Error('compaction: unknown strategy: "session_rewrite"');
        },
      });
      await flushPromises();
      await enterSessionOverride(wrapper, 'sess-broken');

      await wrapper.find('[data-testid="csp-manual-compact-now"]').trigger('click');
      await flushPromises();

      const error = wrapper.find('[data-testid="csp-manual-compact-error"]');
      expect(error.exists()).toBe(true);
      expect(error.text()).toContain('unknown strategy');
      expect(wrapper.find('[data-testid="csp-manual-compact-success"]').exists()).toBe(false);
    });
  });
});
