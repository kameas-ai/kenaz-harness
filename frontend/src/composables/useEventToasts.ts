/**
 * useEventToasts — WP02 consolidation adapter.
 *
 * Subscribes to backend broker events and pushes them onto the unified
 * useToastQueue pathway instead of rendering bespoke toast components.
 *
 * Migrated toasts:
 *   - CostThresholdToast (cost.threshold.crossed)
 *   - RetryAfterRotateToast (provider:retry-after-rotation-failed)
 *   - MergeSuggestionToast (branches:merge-suggested)
 *   - MigrationToast (one-time settings read on mount)
 *   - UpdateToast (update:available — via useUpdateStore)
 *
 * NOT migrated here (retained as rich-UI components because they need
 * input fields or sliders):
 *   - AuthFailureToast (requires inline API key input)
 *
 * Call once at a top-level component (e.g. SessionsView or App.vue).
 */

import { onMounted, onBeforeUnmount, watch } from 'vue';
import { push, dismiss } from '@/composables/useToastQueue';
import { useEventStream } from '@/lib/useEventStream';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { useUpdateStore } from '@/components/updates/useUpdateStore';
import type { CostThresholdCrossedPayload, RetryAfterRotationFailedPayload } from '@/lib/types';

interface MergeSuggestionPayload {
  branchId: string;
  reason: string;
  detail?: string;
}

function formatUsd(usd: number): string {
  if (usd < 0.01) return '<$0.01';
  if (usd < 1) return '$' + usd.toFixed(3);
  if (usd < 10) return '$' + usd.toFixed(2);
  return '$' + usd.toFixed(2);
}

function costHeadline(pct: number): string {
  if (pct >= 200) return '2× over your monthly budget';
  if (pct >= 150) return '1.5× over your monthly budget';
  if (pct >= 100) return 'Monthly budget reached';
  return `${pct}% of your monthly budget`;
}

function mergeSuggestionLabel(reason: string): string {
  switch (reason) {
    case 'terminal_state_token': return 'Branch reply looks terminal';
    case 'child_run_complete': return 'Branch run finished';
    case 'idle_timeout': return 'Branch has been idle';
    default: return reason;
  }
}

// Track active toast IDs for dedup (keyed by domain identifier).
const costToastIds = new Map<string, number>();
const mergeToastIds = new Map<string, number>();
const retryToastIds = new Map<string, number>();
// Update toast is session-scoped (one per discovered version).
const toastedVersions = new Set<string>();

export function useEventToasts() {
  const client = useHarnessClient();
  const updateStore = useUpdateStore();

  // ── CostThresholdCrossed ────────────────────────────────────────────
  useEventStream<CostThresholdCrossedPayload>('cost.threshold.crossed', (payload) => {
    if (!payload || typeof payload.pct !== 'number') return;
    const key = `${payload.yearMonth}:${payload.pct}`;
    if (costToastIds.has(key)) return;
    const id = push(
      `${costHeadline(payload.pct)} — ${formatUsd(payload.monthTotalUsd)} of ${formatUsd(payload.thresholdUsd)} this month`,
      {
        level: payload.pct >= 100 ? 'warn' : 'info',
        durationMs: 8000,
      },
    );
    costToastIds.set(key, id);
  });

  // ── RetryAfterRotateFailed ──────────────────────────────────────────
  const retryDetailShown = new Map<string, boolean>();
  useEventStream<RetryAfterRotationFailedPayload>(
    'provider:retry-after-rotation-failed',
    (payload) => {
      if (!payload?.profile_id) return;
      if (retryToastIds.has(payload.profile_id)) return;
      const actions = payload.error_message
        ? [
            {
              label: 'View error',
              dismissAfter: false,
              perform: () => {
                if (!retryDetailShown.get(payload.profile_id)) {
                  retryDetailShown.set(payload.profile_id, true);
                  push(payload.error_message!, { level: 'warn', durationMs: 0 });
                }
              },
            },
          ]
        : [];
      const id = push(
        'Your key works, but the request still failed. Resend your last message to try again.',
        { level: 'warn', actions, durationMs: 10000 },
      );
      retryToastIds.set(payload.profile_id, id);
    },
  );

  // Clear retry toast when auth resumes.
  useEventStream<{ profile_id: string }>('provider:auth-resumed', (payload) => {
    if (!payload?.profile_id) return;
    const id = retryToastIds.get(payload.profile_id);
    if (id !== undefined) {
      dismiss(id);
      retryToastIds.delete(payload.profile_id);
    }
  });

  // ── MergeSuggestion ─────────────────────────────────────────────────
  useEventStream<MergeSuggestionPayload>('branches:merge-suggested', (payload) => {
    if (!payload?.branchId) return;
    if (mergeToastIds.has(payload.branchId)) return;
    const branchId = payload.branchId;
    const id = push(
      `${mergeSuggestionLabel(payload.reason)}${payload.detail ? ` — ${payload.detail}` : ''}`,
      {
        level: 'info',
        durationMs: 0, // Persistent until user acts.
        actions: [
          {
            label: 'Merge now',
            perform: async () => {
              try {
                await client.branches.merge(branchId);
              } catch {
                push('Merge failed — try again from the branch panel.', {
                  level: 'warn',
                  durationMs: 6000,
                });
              }
            },
          },
        ],
      },
    );
    mergeToastIds.set(payload.branchId, id);
  });

  // ── UpdateToast ─────────────────────────────────────────────────────
  watch(
    () => updateStore.shouldToast.value,
    (should) => {
      if (!should) return;
      const ver = updateStore.status.value?.availableVersion;
      if (!ver || toastedVersions.has(ver)) return;
      toastedVersions.add(ver);
      updateStore.markToasted(ver);
      // The rail up-arrow affordance (UpdateIndicator/UpdateMenu/UpdateToast)
      // was retired by os-menu-bar-01NDFSEX16 §FR-006/§FR-020/§FR-021 — the
      // update signal moved to the Help menu's update item. This toast used
      // to point at the deleted glyph (self-update-repair-01PMUP01 §1.3);
      // it now names the control that actually exists (and, per WP05,
      // actually does what its label says — §1.4).
      //
      // "Install Update", NOT "Check for Updates…". That item's label is
      // computed from MenuState.UpdateState (core/menu/state.go
      // UpdateMenuLabel), and this toast fires on `update:available` —
      // the SAME event main.go's subscriber uses to set UpdateAvailable
      // and rebuild the menu. So by the time the user reads this line the
      // item reads "Install Update"; naming the idle-state label here
      // would send them looking for a control that, at that exact moment,
      // does not exist — the §1.3 defect re-committed one label over.
      push(`Kenaz ${ver} is available — open the Help menu and choose “Install Update”.`, {
        level: 'info',
        durationMs: 5000,
      });
    },
    { immediate: true },
  );

  // ── MigrationToast ──────────────────────────────────────────────────
  onMounted(async () => {
    try {
      const shown = await client.settings.getPermissionsMigrationToastShown();
      if (!shown) {
        push(
          'Bash allowlist migrated to security policies. Open Settings → Bash permissions to review.',
          {
            level: 'info',
            durationMs: 0,
            actions: [
              {
                label: 'Got it',
                perform: async () => {
                  try {
                    await client.settings.setPermissionsMigrationToastShown(true);
                  } catch {
                    // best-effort
                  }
                },
              },
            ],
          },
        );
      }
    } catch {
      // Graceful degradation — if the RPC fails, skip the toast.
    }
  });

  // Cleanup dedup maps on unmount to avoid leaks in hot-reload dev.
  onBeforeUnmount(() => {
    costToastIds.clear();
    mergeToastIds.clear();
    retryToastIds.clear();
    retryDetailShown.clear();
  });
}
