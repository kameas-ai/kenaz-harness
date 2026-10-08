<script setup lang="ts">
/**
 * OrgPausedBanner — the single "paused by your organization" state
 * (kenaz-fleet PR 206 staff pause). Rendered by the fleet-health surfaces in
 * place of their tier-gated / upsell copy; never offers an upgrade.
 *
 * Reads the shared fleet-session store by default (`paused` +
 * `pausedCategory`); a caller with its own status (the memory panel) may
 * pass `category` and `force`.
 */
import { computed } from 'vue';
import { fleetOrgPaused, fleetPausedCategory } from '@/lib/fleetSession';
import {
  ORG_PAUSED_DATA_RIGHTS_NOTE,
  ORG_PAUSED_TITLE,
  orgPausedCategoryLine,
} from '@/lib/orgPausedCopy';

const props = defineProps<{
  /** Show even when the session store is not (yet) paused. */
  force?: boolean;
  /** Overrides the store's category. */
  category?: string;
  /** Compact single-line variant (no data-rights note). */
  compact?: boolean;
}>();

const visible = computed(() => props.force || fleetOrgPaused.value);
const line = computed(() => orgPausedCategoryLine(props.category ?? fleetPausedCategory.value));
</script>

<template>
  <div
    v-if="visible"
    class="rounded-sm border border-signal-warn/40 bg-signal-warn/10 px-3 py-2 text-[12px] space-y-0.5"
    role="status"
    data-testid="org-paused-banner"
  >
    <p class="font-semibold text-signal-warn" data-testid="org-paused-title">{{ ORG_PAUSED_TITLE }}</p>
    <p class="text-ink-muted" data-testid="org-paused-category">{{ line }}</p>
    <p v-if="!compact" class="text-ink-muted" data-testid="org-paused-data-rights">
      {{ ORG_PAUSED_DATA_RIGHTS_NOTE }}
    </p>
  </div>
</template>
