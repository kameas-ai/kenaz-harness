<script setup lang="ts">
/**
 * MigrationToast — one-time toast surfaced after the first-boot bash
 * allowlist → Cedar policy migration (cedar-credential-policy-01KQ8TDE
 * WP10). Reads getPermissionsMigrationToastShown(); if false, renders a
 * small corner toast. On dismiss calls setPermissionsMigrationToastShown(true)
 * so the toast never appears again.
 *
 * Mount this once at the SessionsView level, near other permission modals.
 * The toast is non-blocking: it sits fixed in the bottom-right corner and
 * does not block the chat surface.
 */
import { onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';

const client = useHarnessClient();

/** true = show the toast; false = hidden (either already shown or not yet checked) */
const visible = ref(false);

onMounted(async () => {
  try {
    const shown = await client.settings.getPermissionsMigrationToastShown();
    if (!shown) {
      visible.value = true;
    }
  } catch {
    // Graceful degradation: if the RPC fails, don't show the toast.
    // The next boot will retry.
  }
});

async function dismiss() {
  visible.value = false;
  try {
    await client.settings.setPermissionsMigrationToastShown(true);
  } catch {
    // Best-effort: if the write fails the toast may show again next boot,
    // which is acceptable — it's a one-time UX, not a blocking concern.
  }
}
</script>

<template>
  <Transition name="migration-toast-fade">
    <div
      v-if="visible"
      class="fixed bottom-6 right-6 z-50 w-[22rem] max-w-[92vw] rounded-md border border-border-muted bg-surface-1 p-4 shadow-lg"
      role="status"
      aria-live="polite"
      data-testid="migration-toast"
    >
      <div class="flex items-start justify-between gap-3">
        <div class="flex-1 min-w-0">
          <p
            class="font-ui text-[11px] uppercase tracking-[0.18em] text-accent"
            data-testid="migration-toast-title"
          >
            Permissions updated
          </p>
          <p
            class="mt-1 font-ui text-[12px] text-ink leading-relaxed"
            data-testid="migration-toast-body"
          >
            Bash allowlist migrated to Cedar policies. Open
            <span class="font-mono text-ink-muted">Settings &rarr; Bash permissions</span>
            to review.
          </p>
        </div>
        <button
          type="button"
          class="flex-shrink-0 mt-0.5 px-2 py-0.5 rounded-sm border border-border-muted font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
          data-testid="migration-toast-dismiss"
          @click="dismiss"
        >
          Dismiss
        </button>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.migration-toast-fade-enter-active,
.migration-toast-fade-leave-active {
  transition: opacity 0.15s ease;
}
.migration-toast-fade-enter-from,
.migration-toast-fade-leave-to {
  opacity: 0;
}
</style>
