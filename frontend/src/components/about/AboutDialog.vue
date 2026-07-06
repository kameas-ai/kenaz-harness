<script setup lang="ts">
/**
 * AboutDialog — small modal showing app identity information.
 *
 * Triggered from the OS native menu bar via:
 *   App → About Kenaz Harness (macOS)
 *   Help → About Kenaz Harness (Windows / Linux)
 *
 * The broker event "menu:about:open" flows: Go handler → broker.Publish →
 * Wails EventsEmit → frontend runtime.EventsOn → App.vue → useAboutDialogStore.open().
 * This component reads isOpen from the same store.
 */
import { onMounted, ref } from 'vue';
import BaseDialog from '@/components/ui/BaseDialog.vue';
import { useAboutDialogStore } from '@/lib/aboutDialogStore';
import { useHarnessClient } from '@/lib/useHarnessAPI';

const props = defineProps<{
  /** v-model:open — bound by App.vue */
  open: boolean;
}>();
const emit = defineEmits<{
  (e: 'update:open', value: boolean): void;
}>();

const store = useAboutDialogStore();
const client = useHarnessClient();

const version = ref('…');
const commit = ref('');
const buildTime = ref('');

function close() {
  emit('update:open', false);
  store.close();
}

onMounted(async () => {
  try {
    const info = await client.appInfo();
    version.value = info.build ?? 'dev';
    commit.value = info.commit ?? '';
    buildTime.value = info.buildTime ?? '';
  } catch {
    version.value = 'dev';
  }
});
</script>

<template>
  <BaseDialog
    :open="props.open"
    title="About Kenaz Harness"
    panel-class="w-80"
    @close="close"
  >
    <div class="flex flex-col items-center gap-4 py-2 text-center" data-testid="about-dialog-content">
      <!-- App icon placeholder -->
      <div
        class="grid h-16 w-16 place-items-center rounded-2xl bg-accent-dim"
        aria-hidden="true"
      >
        <svg width="32" height="32" viewBox="0 0 24 24" fill="currentColor" class="text-accent">
          <path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" />
        </svg>
      </div>

      <div>
        <h2 class="font-ui text-base font-semibold text-ink" data-testid="about-app-name">
          Kenaz Harness
        </h2>
        <p class="font-ui text-sm text-ink-muted" data-testid="about-version">
          Version {{ version }}
        </p>
        <p v-if="commit" class="font-mono text-xs text-ink-muted" data-testid="about-commit">
          {{ commit.slice(0, 8) }}
        </p>
        <p v-if="buildTime" class="font-ui text-xs text-ink-muted" data-testid="about-buildtime">
          {{ buildTime }}
        </p>
      </div>

      <p class="font-ui text-xs text-ink-muted">
        © 2026 Kameas AI, Inc.
      </p>

      <a
        href="https://docs.kameas.ai"
        target="_blank"
        rel="noopener noreferrer"
        class="font-ui text-xs text-accent hover:underline"
        data-testid="about-docs-link"
      >
        docs.kameas.ai
      </a>
    </div>
  </BaseDialog>
</template>
