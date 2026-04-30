<script setup lang="ts">
/**
 * PermissionList — shared list component for the four permission
 * Settings panels.
 *
 * Calls client.permissions.listGrants(family) on mount + refresh.
 * Renders each grant as a row with a "Revoke" button that calls
 * client.permissions.revokeGrant(id). After revoke, re-fetches.
 *
 * Props:
 *   - family — resource family filter; empty = all families.
 */

import { onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { PermissionGrant, PermissionFamily } from '@/lib/types';

const props = withDefaults(
  defineProps<{
    family?: PermissionFamily | '';
  }>(),
  { family: '' },
);

const client = useHarnessClient();
const grants = ref<PermissionGrant[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);
const revoking = ref<Set<string>>(new Set());

const FAMILY_ICONS: Record<string, string> = {
  bash: '⚡',
  fs: '📁',
  credential: '🔑',
  tool: '🔧',
};

async function refresh() {
  loading.value = true;
  error.value = null;
  try {
    grants.value = await client.permissions.listGrants(props.family ?? '');
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    grants.value = [];
  } finally {
    loading.value = false;
  }
}

async function revoke(id: string) {
  revoking.value = new Set([...revoking.value, id]);
  try {
    await client.permissions.revokeGrant(id);
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    const next = new Set(revoking.value);
    next.delete(id);
    revoking.value = next;
  }
}

onMounted(refresh);

defineExpose({ refresh, grants });
</script>

<template>
  <div data-testid="permission-list">
    <div
      v-if="loading"
      class="font-ui text-[12px] text-ink-muted"
      data-testid="permission-list-loading"
    >
      Loading…
    </div>
    <div
      v-else-if="error"
      class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="permission-list-error"
    >
      {{ error }}
    </div>
    <p
      v-else-if="grants.length === 0"
      class="font-ui text-[12px] text-ink-muted"
      data-testid="permission-list-empty"
    >
      No saved grants yet. "Allow always" decisions will appear here.
    </p>
    <ul
      v-else
      class="divide-y divide-border-muted"
      data-testid="permission-list-rows"
    >
      <li
        v-for="grant in grants"
        :key="grant.id"
        class="flex items-center justify-between gap-3 py-2"
        :data-testid="`permission-list-row-${grant.id}`"
      >
        <div class="flex items-center gap-2 min-w-0">
          <span
            class="shrink-0 inline-flex items-center justify-center rounded-sm border border-border-muted bg-surface-2 px-1.5 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-subtle"
            :title="grant.family"
          >
            {{ FAMILY_ICONS[grant.family] ?? '🔒' }}
            {{ grant.family }}
          </span>
          <span
            class="font-mono text-[12px] text-ink truncate"
            :title="grant.resourceKey"
          >
            {{ grant.resourceKey }}
          </span>
        </div>
        <button
          type="button"
          class="shrink-0 rounded-sm border border-signal-danger px-2 py-1 font-ui text-[11px] uppercase tracking-[0.14em] text-signal-danger hover:bg-signal-danger hover:text-white disabled:opacity-50 transition-colors"
          :disabled="revoking.has(grant.id)"
          :data-testid="`permission-revoke-${grant.id}`"
          @click="revoke(grant.id)"
        >
          Revoke
        </button>
      </li>
    </ul>
  </div>
</template>
