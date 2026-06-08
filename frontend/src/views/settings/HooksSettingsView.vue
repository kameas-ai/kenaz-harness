<script setup lang="ts">
/**
 * HooksSettingsView — Settings → Hooks tab.
 *
 * Composes HooksPanel (list), HookEditor (create/edit form), and
 * HookDryRunDrawer (synthetic payload + output viewer) into the
 * standard settings tab layout.
 *
 * Reached via /settings?tab=hooks. SettingsView mounts this component
 * when route.query.tab === 'hooks'.
 *
 * The panel is hidden (with a flag-disabled notice) when the
 * hooks_v2_enabled feature flag is off. The flag is read from
 * client.config.getFlags() — same pattern as SlashCommandsView.
 *
 * hooks-event-surface-expansion-01KZNP3A WP07d.
 */
import { onMounted, ref } from 'vue';
import HooksPanel from '@/components/settings/HooksPanel.vue';
import HookEditor from '@/components/settings/HookEditor.vue';
import HookDryRunDrawer from '@/components/settings/HookDryRunDrawer.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { Hook, BuiltinDescriptor } from '@/lib/types';

const client = useHarnessClient();

// ── feature flag ────────────────────────────────────────────────────────

const flagEnabled = ref(true); // optimistic default; overridden on load

async function loadFlag(): Promise<void> {
  try {
    const flags = await client.config.getFlags();
    const hooksFlag = flags.find((f) => f.name === 'hooks_v2_enabled');
    // Default is "enabled" if the flag is absent or explicitly enabled.
    flagEnabled.value = hooksFlag === undefined || hooksFlag.enabled;
  } catch {
    flagEnabled.value = true;
  }
}

// ── list state ───────────────────────────────────────────────────────────

const hooks = ref<Hook[]>([]);
const builtins = ref<BuiltinDescriptor[]>([]);
const loading = ref(false);
const listError = ref<string | null>(null);

async function refresh(): Promise<void> {
  loading.value = true;
  listError.value = null;
  try {
    const [list, descs] = await Promise.all([
      client.hooks.list(),
      client.hooks.availableBuiltins(),
    ]);
    hooks.value = list;
    builtins.value = descs;
  } catch (err) {
    listError.value = err instanceof Error ? err.message : String(err);
    hooks.value = [];
    builtins.value = [];
  } finally {
    loading.value = false;
  }
}

// ── editor state ─────────────────────────────────────────────────────────

const editorOpen = ref(false);
const editorHook = ref<Hook | null>(null);
const saving = ref(false);
const saveError = ref<string | null>(null);

function openCreate(): void {
  editorHook.value = null;
  editorOpen.value = true;
  saveError.value = null;
}

function openEdit(h: Hook): void {
  editorHook.value = JSON.parse(JSON.stringify(h)) as Hook;
  editorOpen.value = true;
  saveError.value = null;
}

function closeEditor(): void {
  editorOpen.value = false;
  editorHook.value = null;
  saveError.value = null;
}

async function handleSave(h: Hook): Promise<void> {
  saving.value = true;
  saveError.value = null;
  try {
    const existing = hooks.value.find((existing) => existing.id === h.id);
    if (existing) {
      await client.hooks.update(h);
    } else {
      await client.hooks.add(h);
    }
    closeEditor();
    await refresh();
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
  } finally {
    saving.value = false;
  }
}

async function handleRemove(id: string): Promise<void> {
  listError.value = null;
  try {
    await client.hooks.remove(id);
    await refresh();
  } catch (err) {
    listError.value = err instanceof Error ? err.message : String(err);
  }
}

async function handleToggle(h: Hook): Promise<void> {
  listError.value = null;
  try {
    await client.hooks.update({ ...h, enabled: !h.enabled });
    await refresh();
  } catch (err) {
    listError.value = err instanceof Error ? err.message : String(err);
  }
}

async function handleInstallStarter(): Promise<void> {
  listError.value = null;
  try {
    await client.hooks.installStarterMemory();
    await refresh();
  } catch (err) {
    listError.value = err instanceof Error ? err.message : String(err);
  }
}

// ── dry run state ─────────────────────────────────────────────────────────

const dryRunOpen = ref(false);
const dryRunHook = ref<Hook | null>(null);

function openDryRun(h: Hook): void {
  dryRunHook.value = h;
  dryRunOpen.value = true;
}

function closeDryRun(): void {
  dryRunOpen.value = false;
  dryRunHook.value = null;
}

// ── init ──────────────────────────────────────────────────────────────────

onMounted(async () => {
  await loadFlag();
  await refresh();
});
</script>

<template>
  <div>
    <div class="px-6 py-4 max-w-5xl">
      <!-- save error banner (from editor) -->
      <div
        v-if="saveError"
        class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
        data-testid="hooks-settings-save-error"
      >
        {{ saveError }}
      </div>

      <!-- two-column layout: panel | editor -->
      <div class="flex gap-6">
        <!-- Hook list -->
        <div :class="editorOpen ? 'flex-1 min-w-0' : 'w-full'">
          <HooksPanel
            :hooks="hooks"
            :builtins="builtins"
            :loading="loading"
            :error="listError"
            :flag-enabled="flagEnabled"
            @add="openCreate"
            @edit="openEdit"
            @remove="handleRemove"
            @toggle="handleToggle"
            @install-starter="handleInstallStarter"
          />
        </div>

        <!-- Hook editor (inline side panel) -->
        <div
          v-if="editorOpen"
          class="w-96 shrink-0 rounded-md border border-border-muted bg-surface-1 px-4 py-4"
          data-testid="hooks-editor-panel"
        >
          <HookEditor
            :hook="editorHook"
            :builtins="builtins"
            :saving="saving"
            @save="handleSave"
            @cancel="closeEditor"
            @dry-run="openDryRun"
          />
        </div>
      </div>
    </div>

    <!-- Dry-run drawer (teleported to body) -->
    <HookDryRunDrawer
      :hook="dryRunHook"
      :open="dryRunOpen"
      @close="closeDryRun"
    />
  </div>
</template>
