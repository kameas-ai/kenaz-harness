<script setup lang="ts">
/**
 * PublishDialog — publish a workflow / agent-pack / bundle to the fleet catalog.
 *
 * Props:
 *   - open: boolean        — controls visibility
 *   - kind: string         — 'workflow' | 'agent_pack' | 'bundle'
 *   - slug: string         — pre-filled item slug (e.g. the workflow ID)
 *   - payloadJson: string  — JSON-serialised item definition
 *
 * Emits:
 *   - close  — dialog should be dismissed
 *   - published(item: CatalogItemView) — after a successful publish
 *
 * (fleet-share-and-sync-01NDFSEX14 WP03)
 */
import { ref, watch } from 'vue';
import BaseDialog from '@/components/ui/BaseDialog.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { CatalogItemView } from '@/lib/types';

const props = defineProps<{
  open: boolean;
  /** item kind to display in the form */
  kind: string;
  /** pre-populated slug */
  slug?: string;
  /** serialised payload — passed opaquely to backend */
  payloadJson?: string;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'published', item: CatalogItemView): void;
}>();

const client = useHarnessClient();

// ── form state ────────────────────────────────────────────────────────────
const version = ref('1.0.0');
const description = ref('');
const visibility = ref<'private' | 'team' | 'org_public'>('team');
const busy = ref(false);
const errorMsg = ref('');

watch(() => props.open, (open) => {
  if (open) {
    // reset on each open so stale state doesn't carry forward
    version.value = '1.0.0';
    description.value = '';
    visibility.value = 'team';
    busy.value = false;
    errorMsg.value = '';
  }
});

// ── submit ─────────────────────────────────────────────────────────────────

async function submit() {
  if (!version.value.trim() || !description.value.trim()) {
    errorMsg.value = 'Version and description are required.';
    return;
  }
  busy.value = true;
  errorMsg.value = '';
  try {
    const item = await client.catalog.publish({
      kind: props.kind,
      slug: props.slug ?? props.kind,
      version: version.value.trim(),
      description: description.value.trim(),
      visibility: visibility.value,
      payload_json: props.payloadJson ?? '{}',
    });
    emit('published', item);
    emit('close');
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <BaseDialog
    :open="open"
    title="Publish to team catalog"
    panel-class="w-full max-w-md rounded-md border border-border-muted bg-surface-1 p-5 shadow-xl"
    @close="emit('close')"
  >
    <div data-testid="publish-dialog">
      <h2 class="font-ui text-base font-semibold text-ink" data-testid="publish-dialog-title">
        Publish to team catalog
      </h2>
      <p class="mt-1 font-ui text-xs text-ink-muted">
        Publishes this <strong>{{ kind }}</strong>
        <template v-if="slug"> (<code>{{ slug }}</code>)</template>
        to the fleet catalog so team members can install it.
      </p>

      <!-- Error banner -->
      <div
        v-if="errorMsg"
        class="mt-3 rounded-sm border border-red-300 bg-red-50 px-3 py-2 text-xs text-red-700"
        role="alert"
        data-testid="publish-dialog-error"
      >
        {{ errorMsg }}
      </div>

      <div class="mt-4 grid gap-3">
        <!-- Version -->
        <label class="grid gap-1">
          <span class="font-ui text-xs font-medium text-ink-muted">Version (SemVer)</span>
          <input
            v-model="version"
            type="text"
            placeholder="1.0.0"
            class="h-8 rounded-sm border border-border-muted bg-surface-0 px-2 font-mono text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
            data-testid="publish-version-input"
          />
        </label>

        <!-- Description -->
        <label class="grid gap-1">
          <span class="font-ui text-xs font-medium text-ink-muted">Description</span>
          <textarea
            v-model="description"
            rows="2"
            placeholder="Brief description for catalog browsers"
            class="rounded-sm border border-border-muted bg-surface-0 px-2 py-1.5 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
            data-testid="publish-description-input"
          />
        </label>

        <!-- Visibility -->
        <label class="grid gap-1">
          <span class="font-ui text-xs font-medium text-ink-muted">Visibility</span>
          <select
            v-model="visibility"
            class="h-8 rounded-sm border border-border-muted bg-surface-0 px-2 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
            data-testid="publish-visibility-select"
          >
            <option value="private">Private (just me, multi-device)</option>
            <option value="team">Team (all team members)</option>
            <option value="org_public">Org-public (any org member)</option>
          </select>
        </label>
      </div>

      <div class="mt-5 flex justify-end gap-2">
        <button
          type="button"
          class="h-8 rounded-sm px-3 font-ui text-sm text-ink-muted hover:bg-surface-2"
          :disabled="busy"
          data-testid="publish-cancel-btn"
          @click="emit('close')"
        >
          Cancel
        </button>
        <button
          type="button"
          class="h-8 rounded-sm bg-accent px-4 font-ui text-sm font-medium text-white hover:bg-accent/90 disabled:opacity-50"
          :disabled="busy"
          data-testid="publish-submit-btn"
          @click="submit"
        >
          {{ busy ? 'Publishing…' : 'Publish' }}
        </button>
      </div>
    </div>
  </BaseDialog>
</template>
