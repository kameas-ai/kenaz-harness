<script setup lang="ts">
/**
 * AdviceAutoActedBanner — laya-advisors-01LAYA001 WP07's "notify" half
 * of spec §3's "auto-act AND notify" contract. Renders when the
 * advisor seam auto-acts a SafetyReversible recommendation at the
 * Autonomous tier (branch_now is v1's only auto-act-eligible kind) so
 * the user always sees that a branch was created on their behalf, not
 * just an audit-log row nobody looks at.
 *
 * Before this component existed, useAdviceChips.ts computed
 * autoActedBanner/clearAutoActedBanner with zero .vue consumers — the
 * app silently branched a session with no visible notice, exactly the
 * silent-action class this repo's rituals exist to catch. This closes
 * that gap.
 *
 * Purely presentational, mirroring AdviceChip.vue / LongSessionNudge
 * .vue's split between a dumb component and its composable.
 */
import type { AdviceAutoActedPayload } from '@/lib/types';

const props = defineProps<{
  notice: AdviceAutoActedPayload;
}>();

const emit = defineEmits<{
  /** Fires when the user clicks "View branch". */
  open: [childSessionId: string];
  /** Fires when the user dismisses the banner. */
  dismiss: [];
}>();

/** Per-kind notice copy (v1: branch_now is the only auto-act-eligible kind). */
const kindCopy: Record<string, string> = {
  branch_now: 'This looked like a separable thread, so it was branched off automatically.',
};

function copy(): string {
  return kindCopy[props.notice.kind_id] ?? 'The advisor took an action on this session automatically.';
}

function handleOpen() {
  emit('open', props.notice.child_session_id);
}

function handleDismiss() {
  emit('dismiss');
}
</script>

<template>
  <div
    class="flex items-start gap-2 rounded-md border border-accent-hairline bg-surface-1 px-3 py-2"
    role="status"
    aria-live="polite"
    :aria-label="`Auto-acted: ${notice.kind_id}`"
    data-testid="advice-auto-acted-banner"
    :data-kind-id="notice.kind_id"
  >
    <div class="flex-shrink-0 text-accent-strong" aria-hidden="true">⚡</div>

    <div class="flex min-w-0 flex-1 flex-col gap-1">
      <span class="font-ui text-sm text-ink" data-testid="advice-auto-acted-banner-body">
        {{ copy() }}
      </span>

      <div class="flex flex-wrap items-center gap-2">
        <button
          v-if="notice.child_session_id"
          type="button"
          class="rounded-md bg-accent-strong px-2.5 py-1 font-ui text-xs text-on-accent"
          data-testid="advice-auto-acted-banner-open"
          @click="handleOpen"
        >
          View branch
        </button>
        <button
          type="button"
          class="font-ui text-xs text-ink-muted hover:text-ink"
          data-testid="advice-auto-acted-banner-dismiss"
          aria-label="Dismiss"
          @click="handleDismiss"
        >
          Dismiss
        </button>
      </div>
    </div>
  </div>
</template>
