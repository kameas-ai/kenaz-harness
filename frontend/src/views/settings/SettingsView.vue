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
import { computed, onMounted, ref } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import SettingsTabs from '@/views/settings/SettingsTabs.vue';
import BashPermissionsPanel from '@/views/settings/BashPermissionsPanel.vue';
import FilesystemPermissionsPanel from '@/views/settings/FilesystemPermissionsPanel.vue';
import CredentialPermissionsPanel from '@/views/settings/CredentialPermissionsPanel.vue';
import ToolPermissionsPanel from '@/views/settings/ToolPermissionsPanel.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { debouncedSave } from '@/lib/settings';
import { Plus } from '@/shell/icons';
import AttachmentRow from '@/components/contexts/AttachmentRow.vue';
import AttachmentTreePicker from '@/components/contexts/AttachmentTreePicker.vue';
import type {
  AppInfo,
  Attachment,
  CompactionAggressiveness,
  CompactionTierExplain,
  PermissionMode,
  Provider,
  Settings,
  Theme,
} from '@/lib/types';

const client = useHarnessClient();

const settings = ref<Settings>({
  schemaVersion: 1,
  lastRoute: '/sessions',
  theme: 'system',
  accent: 'default',
  windowSize: { width: 1280, height: 800 },
  memoryEnabled: false,
  confirmEachDisabled: false,
});
const appInfo = ref<AppInfo | null>(null);
const restoreOnLaunch = ref(true);
const confirmEachEnabled = ref(true);

/* ── Compaction (mission compaction-strategy-ui-01KQ8TDI §2.9) ─────── */

/**
 * The five-stop slider's locked tier order. Same order as plan §2.2 —
 * off (no compaction) → maximal (rolling). The slider's index maps
 * 1:1 onto this array.
 */
const COMPACTION_TIERS: ReadonlyArray<CompactionAggressiveness> = [
  'off',
  'conservative',
  'balanced',
  'aggressive',
  'maximal',
];

/** Local working copy of the compaction-related fields. The component
 * mutates this on user input and pushes through client.settings.set
 * via debouncedSave. */
const compactionTier = ref<CompactionAggressiveness>('balanced');
const compactionProviderId = ref('');
const compactionModelId = ref('');
const compactionArchiveDays = ref<number>(90);
const compactionRecentWindow = ref<number>(4);
const compactionExplainOpen = ref(false);
const compactionTiers = ref<CompactionTierExplain[]>([]);
const compactionProviders = ref<Provider[]>([]);
const compactionArchiveDaysError = ref<string | null>(null);
const compactionRecentWindowError = ref<string | null>(null);

/** Derived: the explain row matching the currently-selected tier. */
const selectedTierExplain = computed<CompactionTierExplain | null>(() => {
  return (
    compactionTiers.value.find(
      (row) => row.aggressiveness === compactionTier.value,
    ) ?? null
  );
});

/** Derived: model dropdown options.
 *
 * The plan calls for a "ProviderProfileRef dropdown component" that may
 * already exist from another mission. None ships in this repo today, so
 * we render a flat <select> over each provider's authorised model list
 * — replace with the canonical picker when it lands. The first option
 * is "Use session's active model (recommended)" with empty
 * provider/model values.
 *
 * Wire shape: option value = `${providerId}::${modelId}` so the
 * <select> stays a primitive string while we still emit a
 * ProviderProfileRef on save.
 */
const compactionModelOptions = computed<
  { value: string; label: string; providerId: string; modelId: string }[]
>(() => {
  const out: {
    value: string;
    label: string;
    providerId: string;
    modelId: string;
  }[] = [];
  for (const p of compactionProviders.value) {
    const models = p.models && p.models.length > 0 ? p.models : [p.model];
    for (const m of models) {
      out.push({
        value: `${p.id}::${m}`,
        label: `${p.name} • ${m}`,
        providerId: p.id,
        modelId: m,
      });
    }
  }
  return out;
});

const compactionModelValue = computed<string>(() => {
  if (!compactionProviderId.value || !compactionModelId.value) return '';
  return `${compactionProviderId.value}::${compactionModelId.value}`;
});

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

/* ── WP08 — Permission dials ────────────────────────────────────── */

const PERMISSION_MODES: ReadonlyArray<{ value: PermissionMode; label: string; note?: string }> = [
  { value: 'strict', label: 'Strict', note: 'every call prompts' },
  { value: 'normal', label: 'Normal', note: 'default' },
  { value: 'permissive', label: 'Permissive', note: 'non-dangerous skip prompt' },
];

const permissionMode = ref<PermissionMode>('normal');
const permissionCacheDangerousOps = ref(false);

async function setPermissionMode(mode: PermissionMode) {
  if (mode === 'permissive') {
    if (!window.confirm(
      'Switching to Permissive mode allows all non-dangerous operations without prompting. ' +
      'This reduces security. Are you sure?'
    )) {
      return;
    }
  }
  permissionMode.value = mode;
  try {
    await client.settings.setPermissionMode(mode);
  } catch {
    // revert
    permissionMode.value = (await client.settings.getPermissionMode().catch(() => 'normal')) as PermissionMode;
  }
}

async function togglePermissionCacheDangerousOps() {
  const next = !permissionCacheDangerousOps.value;
  if (next) {
    if (!window.confirm(
      'Enabling this allows "Allow always" for dangerous operations like rm, sudo, and system path writes. ' +
      'This is a significant security override. Are you sure?'
    )) {
      return;
    }
  }
  permissionCacheDangerousOps.value = next;
  try {
    await client.settings.setPermissionCacheDangerousOps(next);
  } catch {
    permissionCacheDangerousOps.value = !next;
  }
}

async function refresh() {
  try {
    settings.value = await client.settings.get();
  } catch {
    // Keep defaults on error.
  }
  try {
    confirmEachEnabled.value = await client.settings.getConfirmEach();
  } catch {
    confirmEachEnabled.value = true;
  }
  try {
    appInfo.value = await client.appInfo();
  } catch {
    appInfo.value = null;
  }
  // Hydrate the compaction working copies from the persisted settings.
  compactionTier.value =
    (settings.value.compactionAggressiveness as CompactionAggressiveness) ||
    'balanced';
  compactionProviderId.value = settings.value.compactionModel?.providerId ?? '';
  compactionModelId.value = settings.value.compactionModel?.modelId ?? '';
  compactionArchiveDays.value = settings.value.compactionArchiveDays || 90;
  compactionRecentWindow.value = settings.value.compactionRecentWindow || 4;
  compactionArchiveDaysError.value = null;
  compactionRecentWindowError.value = null;
  // Tier-explain payload + provider list both feed the Compaction
  // section; either failing returns the empty-state UI rather than
  // bricking the page.
  try {
    compactionTiers.value = await client.compaction.getTierExplain();
  } catch {
    compactionTiers.value = [];
  }
  try {
    compactionProviders.value = await client.llm.listProviders();
  } catch {
    compactionProviders.value = [];
  }
  await loadGlobalAttachments();
  // Load permission dials
  try {
    permissionMode.value = (await client.settings.getPermissionMode()) as PermissionMode;
  } catch {
    permissionMode.value = 'normal';
  }
  try {
    permissionCacheDangerousOps.value = await client.settings.getPermissionCacheDangerousOps();
  } catch {
    permissionCacheDangerousOps.value = false;
  }
}

function setCompactionTier(t: CompactionAggressiveness) {
  compactionTier.value = t;
  persistCompactionFields();
}

function onCompactionModelChange(evt: Event) {
  const v = (evt.target as HTMLSelectElement).value;
  if (!v) {
    compactionProviderId.value = '';
    compactionModelId.value = '';
  } else {
    const opt = compactionModelOptions.value.find((o) => o.value === v);
    if (opt) {
      compactionProviderId.value = opt.providerId;
      compactionModelId.value = opt.modelId;
    }
  }
  persistCompactionFields();
}

function onCompactionArchiveDaysInput(evt: Event) {
  const raw = (evt.target as HTMLInputElement).value;
  const n = Number.parseInt(raw, 10);
  if (Number.isNaN(n) || n < 7 || n > 365) {
    compactionArchiveDaysError.value =
      'Archive days must be between 7 and 365.';
    return;
  }
  compactionArchiveDaysError.value = null;
  compactionArchiveDays.value = n;
  persistCompactionFields();
}

function onCompactionRecentWindowInput(evt: Event) {
  const raw = (evt.target as HTMLInputElement).value;
  const n = Number.parseInt(raw, 10);
  if (Number.isNaN(n) || n < 1) {
    compactionRecentWindowError.value =
      'Recent window must be at least 1.';
    return;
  }
  compactionRecentWindowError.value = null;
  compactionRecentWindow.value = n;
  persistCompactionFields();
}

function persistCompactionFields() {
  // Local-only error: don't persist if we're in an invalid state.
  if (
    compactionArchiveDaysError.value !== null ||
    compactionRecentWindowError.value !== null
  ) {
    return;
  }
  debouncedSave(client, {
    ...settings.value,
    compactionAggressiveness: compactionTier.value,
    compactionModel:
      compactionProviderId.value && compactionModelId.value
        ? {
            providerId: compactionProviderId.value,
            modelId: compactionModelId.value,
          }
        : undefined,
    compactionArchiveDays: compactionArchiveDays.value,
    compactionRecentWindow: compactionRecentWindow.value,
  });
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

async function toggleConfirmEach() {
  confirmEachEnabled.value = !confirmEachEnabled.value;
  try {
    await client.settings.setConfirmEach(confirmEachEnabled.value);
  } catch {
    // Revert visually if the write failed.
    confirmEachEnabled.value = !confirmEachEnabled.value;
  }
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
          Tool execution
        </h2>
        <label class="mt-2 flex items-center gap-3 font-ui text-[12px] text-ink">
          <input
            type="checkbox"
            class="accent-accent"
            :checked="confirmEachEnabled"
            data-testid="confirm-each-toggle"
            @change="toggleConfirmEach"
          />
          Show confirmation modal for tools marked <span class="font-mono">confirm_each</span>
        </label>
        <p class="mt-1 text-[11px] text-ink-muted">
          When off, tools whose policy resolves to <span class="font-mono">confirm_each</span>
          dispatch automatically (equivalent to <span class="font-mono">auto_allow</span>).
          Default: ON.
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

      <section data-testid="compaction-section">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Compaction
        </h2>
        <p class="mt-1 font-ui text-[11px] text-ink-muted">
          When a session approaches the model's context cap, the harness
          summarises older turns into a single message so the conversation
          can keep going. Pick a tier — see "What does this mean?" below
          for the trade-off.
        </p>

        <div
          class="mt-3 inline-flex rounded-sm border border-border"
          role="radiogroup"
          aria-label="Compaction aggressiveness"
        >
          <button
            v-for="t in COMPACTION_TIERS"
            :key="t"
            type="button"
            role="radio"
            :aria-checked="compactionTier === t"
            class="px-3 py-1.5 font-ui text-[12px] border-r border-border last:border-r-0 transition-colors capitalize"
            :class="compactionTier === t
              ? 'bg-surface-3 text-ink'
              : 'bg-surface-1 text-ink-muted hover:text-ink'"
            :data-testid="`compaction-tier-${t}`"
            @click="setCompactionTier(t)"
          >
            {{ t }}
          </button>
        </div>

        <button
          type="button"
          class="mt-3 font-ui text-[11px] text-accent hover:underline"
          data-testid="compaction-explain-toggle"
          :aria-expanded="compactionExplainOpen"
          @click="compactionExplainOpen = !compactionExplainOpen"
        >
          {{ compactionExplainOpen ? 'Hide' : 'What does this mean?' }}
        </button>

        <div
          v-if="compactionExplainOpen"
          class="mt-2 rounded-sm border border-border bg-surface-1 p-3 font-ui text-[12px]"
          data-testid="compaction-explain-disclosure"
        >
          <div v-if="selectedTierExplain" class="space-y-1">
            <div class="font-medium text-ink" data-testid="compaction-explain-label">
              {{ selectedTierExplain.label }}
            </div>
            <p class="text-ink-muted" data-testid="compaction-explain-description">
              {{ selectedTierExplain.description }}
            </p>
            <dl
              class="mt-2 grid gap-1 font-mono text-[11px] text-ink-muted"
              style="grid-template-columns: 14ch 1fr"
              data-testid="compaction-explain-numerics"
            >
              <dt>Trigger %</dt>
              <dd>
                {{
                  selectedTierExplain.triggerPct > 0
                    ? `${Math.round(selectedTierExplain.triggerPct * 100)}% of cap`
                    : '—'
                }}
              </dd>
              <dt>Summarize %</dt>
              <dd>
                {{
                  selectedTierExplain.summarizePct > 0
                    ? `${Math.round(selectedTierExplain.summarizePct * 100)}% of oldest tokens`
                    : '—'
                }}
              </dd>
              <dt>Mode</dt>
              <dd>{{ selectedTierExplain.mode }}</dd>
            </dl>
          </div>
          <div v-else class="text-ink-muted">
            No explain row available — using locked defaults.
          </div>
        </div>

        <div class="mt-4 grid gap-2" style="grid-template-columns: 14ch 1fr">
          <label
            for="compaction-model"
            class="self-center font-ui text-[12px] text-ink-muted"
          >
            Compaction model
          </label>
          <select
            id="compaction-model"
            class="rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
            data-testid="compaction-model-select"
            :value="compactionModelValue"
            @change="onCompactionModelChange"
          >
            <option value="">Use session's active model (recommended)</option>
            <option
              v-for="opt in compactionModelOptions"
              :key="opt.value"
              :value="opt.value"
            >
              {{ opt.label }}
            </option>
          </select>
          <!--
            TODO(compaction-strategy-ui-01KQ8TDI WP06): emit a "deprecated
            model" warning chip here when the capability gate exposes a
            programmatic deprecated check. The save still succeeds today;
            the actual deprecation enforcement happens at compaction time
            via the capability gate (plan §R7).
          -->
        </div>

        <div class="mt-2 grid gap-2" style="grid-template-columns: 14ch 1fr">
          <label
            for="compaction-archive-days"
            class="self-center font-ui text-[12px] text-ink-muted"
          >
            Archive days
          </label>
          <div>
            <input
              id="compaction-archive-days"
              type="number"
              min="7"
              max="365"
              :value="compactionArchiveDays"
              class="w-24 rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
              data-testid="compaction-archive-days-input"
              @input="onCompactionArchiveDaysInput"
            />
            <p
              v-if="compactionArchiveDaysError"
              class="mt-1 font-ui text-[11px] text-signal-danger"
              role="alert"
              data-testid="compaction-archive-days-error"
            >
              {{ compactionArchiveDaysError }}
            </p>
            <p v-else class="mt-1 font-ui text-[11px] text-ink-muted">
              Soft-archived originals are deleted after this many days. Default 90.
            </p>
          </div>
        </div>

        <div class="mt-2 grid gap-2" style="grid-template-columns: 14ch 1fr">
          <label
            for="compaction-recent-window"
            class="self-center font-ui text-[12px] text-ink-muted"
          >
            Recent window
          </label>
          <div>
            <input
              id="compaction-recent-window"
              type="number"
              min="1"
              :value="compactionRecentWindow"
              class="w-24 rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
              data-testid="compaction-recent-window-input"
              @input="onCompactionRecentWindowInput"
            />
            <p
              v-if="compactionRecentWindowError"
              class="mt-1 font-ui text-[11px] text-signal-danger"
              role="alert"
              data-testid="compaction-recent-window-error"
            >
              {{ compactionRecentWindowError }}
            </p>
            <p v-else class="mt-1 font-ui text-[11px] text-ink-muted">
              Most-recent user-assistant pairs that compaction never touches. Default 4.
            </p>
          </div>
        </div>
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

      <!-- ── WP08 — Permission mode dial ──────────────────────────── -->
      <section id="permissions-mode" data-testid="permission-mode-section">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Permission mode
        </h2>
        <p class="mt-1 font-ui text-[11px] text-ink-dim">
          Controls when permission prompts appear across all resource families (bash, filesystem, credentials, tools).
        </p>
        <div class="mt-2 inline-flex rounded-sm border border-border" role="radiogroup" aria-label="Permission mode">
          <button
            v-for="m in PERMISSION_MODES"
            :key="m.value"
            type="button"
            role="radio"
            :aria-checked="permissionMode === m.value"
            class="px-3 py-1.5 font-ui text-[12px] border-r border-border last:border-r-0 transition-colors"
            :class="permissionMode === m.value
              ? 'bg-surface-3 text-ink'
              : 'bg-surface-1 text-ink-muted hover:text-ink'"
            :data-testid="`permission-mode-${m.value}`"
            @click="setPermissionMode(m.value)"
          >
            {{ m.label }}
            <span v-if="m.note" class="ml-1 text-[10px] text-ink-subtle">({{ m.note }})</span>
          </button>
        </div>
      </section>

      <!-- Cache dangerous ops toggle -->
      <section data-testid="permission-cache-dangerous-section">
        <div class="flex items-center justify-between">
          <div>
            <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
              Cache dangerous operation grants
            </h2>
            <p class="mt-0.5 font-ui text-[11px] text-ink-dim">
              When enabled, "Allow always" is offered for dangerous ops (rm, sudo, system paths, etc).
              Default off — dangerous ops re-prompt every time.
            </p>
          </div>
          <button
            type="button"
            class="ml-4 shrink-0 rounded-full w-9 h-5 transition-colors"
            :class="permissionCacheDangerousOps ? 'bg-accent' : 'bg-surface-3'"
            role="switch"
            :aria-checked="permissionCacheDangerousOps"
            data-testid="permission-cache-dangerous-toggle"
            @click="togglePermissionCacheDangerousOps"
          >
            <span class="sr-only">Cache dangerous operation grants</span>
            <span
              class="block w-4 h-4 rounded-full bg-white shadow transition-transform mx-0.5"
              :class="permissionCacheDangerousOps ? 'translate-x-4' : 'translate-x-0'"
            />
          </button>
        </div>
      </section>

      <!-- ── WP08 — 4 Permission panels ───────────────────────────── -->
      <section id="permissions-bash" data-testid="permissions-bash-section">
        <BashPermissionsPanel />
      </section>

      <section id="permissions-fs" data-testid="permissions-fs-section">
        <FilesystemPermissionsPanel />
      </section>

      <section id="permissions-credential" data-testid="permissions-credential-section">
        <CredentialPermissionsPanel />
      </section>

      <section id="permissions-tool" data-testid="permissions-tool-section">
        <ToolPermissionsPanel />
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
