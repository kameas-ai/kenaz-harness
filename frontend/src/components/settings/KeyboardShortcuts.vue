<script setup lang="ts">
/**
 * KeyboardShortcuts — settings section listing all registered shortcuts
 * with current binding and a Record button per row
 * (keyboard-shortcuts-settings-01KQ8TDR plan §2.3, WP05).
 *
 * Mounted inside SettingsView as the "Keyboard shortcuts" section.
 *
 * Persistence: reads/writes Settings.keyboardShortcuts via
 * client.settings.get / client.settings.set (the full-settings
 * round-trip, same pattern as the rest of SettingsView).
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { debouncedSave } from '@/lib/settings';
import {
  SHORTCUTS,
  RESERVED_SYSTEM_BINDINGS,
  groupByCategory,
  resolveBinding,
  getShortcut,
} from '@/lib/shortcuts/registry';
import { toCanonical } from '@/lib/shortcuts/platform';
import KeycapDisplay from '@/components/shortcuts/KeycapDisplay.vue';
import type { Settings } from '@/lib/types';

const client = useHarnessClient();

// ── state ──────────────────────────────────────────────────────────────

/** Full settings snapshot (needed for the debounced save round-trip). */
const settings = ref<Settings>({
  schemaVersion: 1,
  lastRoute: '/sessions',
  theme: 'system',
  accent: 'default',
  windowSize: { width: 1280, height: 800 },
  memoryEnabled: false,
  confirmEachDisabled: false,
});

/** User overrides map: id → canonical binding. */
const overrides = ref<Record<string, string>>({});

/** The id of the shortcut currently in capture mode (null = none). */
const capturingId = ref<string | null>(null);

/** Error banner shown during capture (reserved binding, pure modifier, etc.). */
const captureError = ref<string | null>(null);

/** Conflict state: the id of the OTHER shortcut that owns the captured binding. */
const conflictingId = ref<string | null>(null);

/** The binding that was captured and triggered the conflict. */
const pendingBinding = ref<string | null>(null);

/** Search filter (debounced 100ms in template). */
const searchQuery = ref('');

/** Whether the reset-all confirm dialog is open. */
const resetAllConfirm = ref(false);

// ── data ───────────────────────────────────────────────────────────────

const groups = computed(() => groupByCategory(SHORTCUTS));

const filteredGroups = computed(() => {
  const q = searchQuery.value.trim().toLowerCase();
  if (!q) return groups.value;
  const filtered = new Map<ReturnType<typeof groupByCategory> extends Map<infer K, unknown> ? K : never, typeof SHORTCUTS[number][]>();
  for (const [cat, shortcuts] of groups.value) {
    const matching = shortcuts.filter(
      (s) =>
        s.label.toLowerCase().includes(q) ||
        s.description.toLowerCase().includes(q) ||
        s.defaultBinding.toLowerCase().includes(q),
    );
    if (matching.length > 0) filtered.set(cat, matching);
  }
  return filtered;
});

function effectiveBinding(id: string): string {
  return resolveBinding(id, overrides.value) ?? '';
}

// ── lifecycle ──────────────────────────────────────────────────────────

async function refresh() {
  try {
    settings.value = await client.settings.get();
    overrides.value = { ...(settings.value.keyboardShortcuts ?? {}) };
  } catch {
    // Keep defaults on error.
  }
}

onMounted(() => {
  void refresh();
});

// ── capture mode ───────────────────────────────────────────────────────

function startCapture(id: string) {
  capturingId.value = id;
  captureError.value = null;
  conflictingId.value = null;
  pendingBinding.value = null;
}

function cancelCapture() {
  capturingId.value = null;
  captureError.value = null;
  conflictingId.value = null;
  pendingBinding.value = null;
}

function onCaptureKeydown(e: KeyboardEvent) {
  if (capturingId.value === null) return;
  e.preventDefault();
  e.stopPropagation();

  if (e.key === 'Escape') {
    cancelCapture();
    return;
  }

  const canonical = toCanonical(e);
  if (!canonical) {
    captureError.value = 'Press a key combination (not just a modifier).';
    return;
  }

  // Reject reserved system bindings.
  if (RESERVED_SYSTEM_BINDINGS.has(canonical)) {
    captureError.value = `"${canonical}" is a system-reserved shortcut and cannot be used.`;
    return;
  }

  // Detect conflict within the same or overlapping scope.
  const thisScope = getShortcut(capturingId.value)?.scope;
  const conflict = SHORTCUTS.find((s) => {
    if (s.id === capturingId.value) return false;
    const effective = resolveBinding(s.id, overrides.value);
    if (effective !== canonical) return false;
    // Scopes that overlap: global overlaps with everything; modal overlaps with global.
    const scopeOverlaps =
      s.scope === 'global' ||
      thisScope === 'global' ||
      s.scope === thisScope;
    return scopeOverlaps;
  });

  if (conflict) {
    conflictingId.value = conflict.id;
    pendingBinding.value = canonical;
    captureError.value = null;
    return;
  }

  applyBinding(capturingId.value, canonical);
}

function applyBinding(id: string, binding: string) {
  overrides.value = { ...overrides.value, [id]: binding };
  persistOverrides();
  cancelCapture();
}

/**
 * Reassign — resolve conflict by applying the pending binding to the
 * capturing shortcut AND clearing the conflicting one.
 */
function reassignAndUnbind() {
  if (!capturingId.value || !pendingBinding.value || !conflictingId.value) return;
  const newOverrides = {
    ...overrides.value,
    [capturingId.value]: pendingBinding.value,
    [conflictingId.value]: '',
  };
  overrides.value = newOverrides;
  persistOverrides();
  cancelCapture();
}

// ── reset ──────────────────────────────────────────────────────────────

function resetShortcut(id: string) {
  const { [id]: _, ...rest } = overrides.value;
  overrides.value = rest;
  persistOverrides();
}

function resetAll() {
  overrides.value = {};
  persistOverrides();
  resetAllConfirm.value = false;
}

// ── persistence ────────────────────────────────────────────────────────

function persistOverrides() {
  debouncedSave(client, {
    ...settings.value,
    keyboardShortcuts: { ...overrides.value },
  });
}

// Keep settings in sync with overrides for the save round-trip.
watch(overrides, (val) => {
  settings.value = { ...settings.value, keyboardShortcuts: { ...val } };
});
</script>

<template>
  <section data-testid="keyboard-shortcuts-section">
    <!-- Section header -->
    <div class="flex items-center justify-between">
      <h2
        id="keyboard-shortcuts-heading"
        class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Keyboard shortcuts
      </h2>
      <button
        v-if="Object.keys(overrides).filter((k) => overrides[k]).length > 0"
        type="button"
        class="font-ui text-[11px] text-accent hover:underline"
        data-testid="reset-all-btn"
        @click="resetAllConfirm = true"
      >
        Reset all
      </button>
    </div>

    <!-- Reset-all confirm dialog -->
    <div
      v-if="resetAllConfirm"
      class="mt-2 rounded-sm border border-signal-danger bg-surface-1 p-3 font-ui text-[12px]"
      role="alertdialog"
      aria-labelledby="reset-all-label"
      data-testid="reset-all-confirm"
    >
      <p id="reset-all-label" class="text-ink">
        Reset all keyboard shortcut overrides to defaults?
      </p>
      <div class="mt-2 flex gap-2">
        <button
          type="button"
          class="rounded-sm border border-signal-danger px-3 py-1 text-signal-danger hover:bg-signal-danger hover:text-white transition-colors"
          data-testid="reset-all-confirm-yes"
          @click="resetAll"
        >
          Reset all
        </button>
        <button
          type="button"
          class="rounded-sm border border-border px-3 py-1 text-ink-muted hover:text-ink transition-colors"
          data-testid="reset-all-confirm-cancel"
          @click="resetAllConfirm = false"
        >
          Cancel
        </button>
      </div>
    </div>

    <!-- Search filter -->
    <div class="mt-3">
      <input
        v-model="searchQuery"
        type="search"
        placeholder="Filter shortcuts…"
        class="w-full rounded-sm border border-border bg-surface-1 px-3 py-1.5 font-ui text-[12px] text-ink placeholder:text-ink-dim"
        aria-label="Filter shortcuts"
        data-testid="shortcut-search"
      />
    </div>

    <!-- Conflict resolution modal -->
    <div
      v-if="conflictingId && pendingBinding"
      class="mt-3 rounded-sm border border-accent bg-surface-1 p-3 font-ui text-[12px]"
      role="alertdialog"
      data-testid="shortcut-conflict-modal"
    >
      <p class="text-ink">
        <KeycapDisplay :binding="pendingBinding" /> is currently bound to
        <strong>{{ getShortcut(conflictingId)?.label }}</strong>.
      </p>
      <div class="mt-2 flex gap-2">
        <button
          type="button"
          class="rounded-sm border border-accent px-3 py-1 text-accent hover:bg-accent hover:text-white transition-colors"
          data-testid="conflict-reassign-btn"
          @click="reassignAndUnbind"
        >
          Reassign and unbind that
        </button>
        <button
          type="button"
          class="rounded-sm border border-border px-3 py-1 text-ink-muted hover:text-ink transition-colors"
          data-testid="conflict-cancel-btn"
          @click="cancelCapture"
        >
          Cancel
        </button>
      </div>
    </div>

    <!-- Grouped shortcut rows -->
    <div class="mt-4 grid gap-6">
      <div
        v-for="[category, shortcuts] in filteredGroups"
        :key="category"
        :data-testid="`shortcut-group-${category.toLowerCase()}`"
      >
        <h3 class="mb-2 font-ui text-[10px] uppercase tracking-[0.2em] text-ink-dim">
          {{ category }}
        </h3>

        <div class="divide-y divide-border-muted">
          <div
            v-for="sc in shortcuts"
            :key="sc.id"
            :id="`shortcut-${sc.id}`"
            class="flex items-center gap-4 py-2"
            :data-testid="`shortcut-row-${sc.id}`"
          >
            <!-- Label + description -->
            <div class="min-w-0 flex-1">
              <div class="font-ui text-[12px] text-ink">{{ sc.label }}</div>
              <div class="font-ui text-[11px] text-ink-muted">{{ sc.description }}</div>
            </div>

            <!-- Capture mode -->
            <div
              v-if="capturingId === sc.id"
              class="flex items-center gap-2"
              :data-testid="`capture-mode-${sc.id}`"
            >
              <div
                class="rounded-sm border border-accent bg-surface-1 px-3 py-1 font-ui text-[11px] text-accent"
                tabindex="0"
                autofocus
                @keydown="onCaptureKeydown"
              >
                <span v-if="captureError" class="text-signal-danger">{{ captureError }}</span>
                <span v-else>Press any key combo… (Esc to cancel)</span>
              </div>
              <button
                type="button"
                class="font-ui text-[11px] text-ink-muted hover:text-ink"
                :aria-label="`Cancel recording for ${sc.label}`"
                @click="cancelCapture"
              >
                Cancel
              </button>
            </div>

            <!-- Normal mode -->
            <div v-else class="flex items-center gap-2 shrink-0">
              <KeycapDisplay :binding="effectiveBinding(sc.id)" />
              <button
                type="button"
                class="font-ui text-[11px] text-accent hover:underline"
                :aria-label="`Record new binding for ${sc.label}`"
                :data-testid="`record-btn-${sc.id}`"
                @click="startCapture(sc.id)"
              >
                Record
              </button>
              <button
                v-if="overrides[sc.id]"
                type="button"
                class="font-ui text-[11px] text-ink-muted hover:text-ink"
                :aria-label="`Reset ${sc.label} to default`"
                :data-testid="`reset-btn-${sc.id}`"
                @click="resetShortcut(sc.id)"
              >
                Reset
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- Empty-state when search yields nothing -->
      <p
        v-if="filteredGroups.size === 0"
        class="font-ui text-[12px] text-ink-muted"
        data-testid="shortcut-empty-search"
      >
        No shortcuts match "{{ searchQuery }}".
      </p>
    </div>
  </section>
</template>
