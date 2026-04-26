<script setup lang="ts">
/**
 * SettingsView — app preferences (NN/SECTION pattern).
 *
 * Theme selector (system / light / dark — currently dark only per
 * Kenaz, structured for v1.x), lastRoute toggle, read-only data-dir
 * + harness build info from AppInfo.
 *
 * Persistence routes through the SettingsStore (Settings_Get +
 * Settings_Set RPC); writes are debounced 250ms via lib/settings.ts
 * to coalesce rapid toggles into a single disk write.
 */
import { onMounted, ref } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import SettingsTabs from '@/views/settings/SettingsTabs.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { debouncedSave } from '@/lib/settings';
import { Plus } from '@/shell/icons';
import AttachmentRow from '@/components/contexts/AttachmentRow.vue';
import AttachmentTreePicker from '@/components/contexts/AttachmentTreePicker.vue';
import type { AppInfo, Attachment, Settings, Theme } from '@/lib/types';

const client = useHarnessClient();

const settings = ref<Settings>({
  schemaVersion: 1,
  lastRoute: '/sessions',
  theme: 'system',
  accent: 'default',
  windowSize: { width: 1280, height: 800 },
  memoryEnabled: false,
});
const appInfo = ref<AppInfo | null>(null);
const restoreOnLaunch = ref(true);

const themes: ReadonlyArray<{ value: Theme; label: string; note?: string }> = [
  { value: 'system', label: 'System' },
  { value: 'dark', label: 'Dark', note: 'v1 default' },
  { value: 'light', label: 'Light', note: 'v1.x' },
];

const globalAttachments = ref<Attachment[]>([]);
const globalAttachmentsError = ref<string | null>(null);
const globalAttachmentsLoading = ref(false);
const globalPickerOpen = ref(false);
const draggedId = ref<string | null>(null);

async function refresh() {
  try {
    settings.value = await client.settings.get();
  } catch {
    // Keep defaults on error.
  }
  try {
    appInfo.value = await client.appInfo();
  } catch {
    appInfo.value = null;
  }
  await loadGlobalAttachments();
}

async function loadGlobalAttachments() {
  globalAttachmentsLoading.value = true;
  globalAttachmentsError.value = null;
  try {
    const rows = await client.attachments.list({
      scopeKind: 'global',
      scopeId: '',
    });
    globalAttachments.value = [...rows];
  } catch (err) {
    globalAttachments.value = [];
    globalAttachmentsError.value =
      err instanceof Error ? err.message : String(err);
  } finally {
    globalAttachmentsLoading.value = false;
  }
}

function openGlobalPicker() {
  globalPickerOpen.value = true;
}

function onGlobalAdded(att: Attachment) {
  globalAttachments.value = [...globalAttachments.value, att];
}

function onGlobalRefreshed(updated: Attachment) {
  globalAttachments.value = globalAttachments.value.map((a) =>
    a.id === updated.id ? updated : a,
  );
}

function onGlobalRemoved(id: string) {
  globalAttachments.value = globalAttachments.value.filter(
    (a) => a.id !== id,
  );
}

function onDragStart(_evt: DragEvent, id: string) {
  draggedId.value = id;
}

function onDragOver(evt: DragEvent, _overId: string) {
  evt.preventDefault();
  if (evt.dataTransfer) evt.dataTransfer.dropEffect = 'move';
}

async function onDrop(_evt: DragEvent, overId: string) {
  const dragged = draggedId.value;
  draggedId.value = null;
  if (!dragged || dragged === overId) return;
  const list = [...globalAttachments.value];
  const fromIdx = list.findIndex((a) => a.id === dragged);
  const toIdx = list.findIndex((a) => a.id === overId);
  if (fromIdx < 0 || toIdx < 0) return;
  const [moved] = list.splice(fromIdx, 1);
  list.splice(toIdx, 0, moved);
  globalAttachments.value = list;
  try {
    await client.attachments.reorder(
      'global',
      '',
      list.map((a) => a.id),
    );
  } catch (err) {
    globalAttachmentsError.value =
      err instanceof Error ? err.message : String(err);
    await loadGlobalAttachments();
  }
}

function onDragEnd() {
  draggedId.value = null;
}

function setTheme(t: Theme) {
  settings.value = { ...settings.value, theme: t };
  void client.settings.saveTheme(t).catch(() => {});
}

function toggleRestore() {
  restoreOnLaunch.value = !restoreOnLaunch.value;
  // When restore is off the chassis still records lastRoute for audit
  // but App.vue's restoreLastRoute respects this preference. We
  // persist the selection alongside the rest of the settings.
  debouncedSave(client, {
    ...settings.value,
    lastRoute: restoreOnLaunch.value ? settings.value.lastRoute : '/sessions',
  });
}

onMounted(() => {
  void refresh();
});
</script>

<template>
  <div>
    <CanvasHead
      number="06"
      section="SETTINGS"
      title="App preferences"
      subtitle="Theme, route restoration, and data-dir info. Settings persist to a single JSON file under your user config dir."
    />
    <SettingsTabs />

    <div class="px-6 py-4 grid gap-6 max-w-3xl" data-testid="settings-form">
      <section>
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Theme
        </h2>
        <div class="mt-2 inline-flex rounded-sm border border-border" role="radiogroup">
          <button
            v-for="t in themes"
            :key="t.value"
            type="button"
            role="radio"
            :aria-checked="settings.theme === t.value"
            class="px-3 py-1.5 font-ui text-[12px] border-r border-border last:border-r-0 transition-colors"
            :class="settings.theme === t.value
              ? 'bg-surface-3 text-ink'
              : 'bg-surface-1 text-ink-muted hover:text-ink'"
            @click="setTheme(t.value)"
          >
            {{ t.label }}
            <span v-if="t.note" class="ml-1 text-[10px] text-ink-subtle">({{ t.note }})</span>
          </button>
        </div>
      </section>

      <section>
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Route restoration
        </h2>
        <label class="mt-2 flex items-center gap-3 font-ui text-[12px] text-ink">
          <input
            type="checkbox"
            class="accent-accent"
            :checked="restoreOnLaunch"
            data-testid="restore-toggle"
            @change="toggleRestore"
          />
          Restore the last visited route on launch
        </label>
        <p class="mt-1 text-[11px] text-ink-muted">
          Last route: <span class="font-mono">{{ settings.lastRoute }}</span>
        </p>
      </section>

      <section>
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Storage
        </h2>
        <dl class="mt-2 grid gap-2 font-ui text-[12px]" style="grid-template-columns: 12ch 1fr">
          <dt class="text-ink-muted">Schema version</dt>
          <dd class="font-mono text-ink">{{ settings.schemaVersion }}</dd>
          <dt class="text-ink-muted">Window size</dt>
          <dd class="font-mono text-ink">
            {{ settings.windowSize.width }} × {{ settings.windowSize.height }}
          </dd>
        </dl>
      </section>

      <section data-testid="global-context-section">
        <div class="flex items-center justify-between">
          <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            Global context
            <span class="ml-1 text-ink-dim">({{ globalAttachments.length }})</span>
          </h2>
          <button
            type="button"
            class="flex items-center gap-1 rounded-sm border border-accent-hairline bg-surface-1 px-2 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow"
            data-testid="global-add-context"
            @click="openGlobalPicker"
          >
            <Plus :size="12" />
            <span>Add context</span>
          </button>
        </div>
        <p class="mt-1 font-ui text-[11px] text-ink-dim">
          Global context applies to every session in the harness. The
          resolved stream concatenates global → project → session, so
          ordering here affects the prefix every conversation receives.
        </p>
        <div
          v-if="globalAttachmentsError"
          class="mt-2 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[11px] text-signal-danger"
          role="alert"
          data-testid="global-attachments-error"
        >
          {{ globalAttachmentsError }}
        </div>
        <div
          v-if="globalAttachmentsLoading"
          class="mt-2 font-ui text-xs text-ink-muted"
          data-testid="global-attachments-loading"
        >
          Loading…
        </div>
        <ul
          v-else-if="globalAttachments.length > 0"
          class="mt-2 space-y-2"
          data-testid="global-attachments-list"
        >
          <li v-for="a in globalAttachments" :key="a.id">
            <AttachmentRow
              :attachment="a"
              :draggable="true"
              @refreshed="onGlobalRefreshed"
              @removed="onGlobalRemoved"
              @drag-start="onDragStart"
              @drag-over="onDragOver"
              @drop="onDrop"
              @drag-end="onDragEnd"
            />
          </li>
        </ul>
        <p
          v-else
          class="mt-2 font-ui text-xs text-ink-muted"
          data-testid="global-attachments-empty"
        >
          No global context yet. Pick a file from the library to attach.
        </p>
      </section>

      <section v-if="appInfo">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Harness build
        </h2>
        <dl class="mt-2 grid gap-2 font-ui text-[12px]" style="grid-template-columns: 12ch 1fr">
          <dt class="text-ink-muted">Build</dt>
          <dd class="font-mono text-ink">{{ appInfo.build }}</dd>
          <dt class="text-ink-muted">Commit</dt>
          <dd class="font-mono text-ink">{{ appInfo.commit }}</dd>
          <dt class="text-ink-muted">Built</dt>
          <dd class="font-mono text-ink">{{ appInfo.buildTime || '—' }}</dd>
          <dt class="text-ink-muted">Go</dt>
          <dd class="font-mono text-ink">{{ appInfo.goVersion || '—' }}</dd>
          <dt class="text-ink-muted">Platform</dt>
          <dd class="font-mono text-ink">{{ appInfo.platform || '—' }}</dd>
        </dl>
      </section>
    </div>

    <AttachmentTreePicker
      :open="globalPickerOpen"
      scope-kind="global"
      scope-id=""
      @added="onGlobalAdded"
      @close="globalPickerOpen = false"
    />
  </div>
</template>
