<script setup lang="ts">
/**
 * BashPermissionsPanel — Settings panel for bash command grants.
 *
 * Lists all bash_allow_*.cedar grants via PermissionList with
 * family="bash". Provides a "Reset all" button that revokes every
 * bash grant after a confirm dialog.
 */

import { ref } from 'vue';
import PermissionList from '@/components/permissions/PermissionList.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { PermissionGrant } from '@/lib/types';

const client = useHarnessClient();
const listRef = ref<InstanceType<typeof PermissionList> | null>(null);
const resetting = ref(false);
const resetError = ref<string | null>(null);

async function resetAll() {
  if (!window.confirm('Revoke ALL bash command grants? This will re-prompt on future bash calls.')) {
    return;
  }
  resetting.value = true;
  resetError.value = null;
  try {
    const all: PermissionGrant[] = await client.permissions.listGrants('bash');
    for (const g of all) {
      await client.permissions.revokeGrant(g.id);
    }
    await listRef.value?.refresh();
  } catch (err) {
    resetError.value = err instanceof Error ? err.message : String(err);
  } finally {
    resetting.value = false;
  }
}
</script>

<template>
  <section data-testid="bash-permissions-panel">
    <div class="flex items-center justify-between">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        ⚡ Bash permissions
      </h2>
      <button
        type="button"
        class="rounded-sm border border-signal-danger px-2 py-1 font-ui text-[11px] uppercase tracking-[0.14em] text-signal-danger hover:bg-signal-danger hover:text-white disabled:opacity-50 transition-colors"
        :disabled="resetting"
        data-testid="bash-permissions-reset-all"
        @click="resetAll"
      >
        Reset all
      </button>
    </div>
    <p class="mt-1 font-ui text-[11px] text-ink-dim">
      Saved "Allow always" grants for bash commands. Revoking re-prompts on next call.
    </p>
    <div
      v-if="resetError"
      class="mt-2 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
    >
      {{ resetError }}
    </div>
    <div class="mt-2">
      <PermissionList ref="listRef" family="bash" />
    </div>
  </section>
</template>
