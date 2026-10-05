<script setup lang="ts">
/**
 * MemoryCaptureToggle — the long-term memory on/off switch, at the top of
 * Knowledge › Learned (knowledge-home-01DOGF0E WP03, spec FR-4 / FR-5).
 *
 * Moved here from Tools › KenazToolsPanel's "Long-term memory" row with the
 * SAME backend calls in the SAME order: `settings.setMemory(next)`, then
 * `hooks.installStarterMemory()` when turning on or
 * `hooks.removeStarterMemory()` when turning off. The switch flips
 * optimistically and rolls back if any call fails.
 *
 * Honesty (FR-5): MemoryView used to render as if capture were live whatever
 * the setting said. This component reads `settings.getMemory()` and says so
 * plainly when capture is off — existing chunks stay listed below (the store
 * is untouched by the switch). When the setting is on but the two starter
 * hooks that do the work (memory.retrieve on pre-send, memory.persist on
 * post-send — core/hooks/memory_builtins.go StarterMemoryHooks) are missing
 * or disabled, it says that too and offers a one-click repair (the same
 * idempotent installStarterMemory call the switch makes).
 */
import { computed, onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';

const client = useHarnessClient();

/** null = not read yet, or the read failed (see loadError). */
const enabled = ref<boolean | null>(null);
const loadError = ref<string | null>(null);
const busy = ref(false);
const error = ref<string | null>(null);

/** Builtins the starter hooks run; both must be installed and enabled. */
const REQUIRED_BUILTINS = ['memory.retrieve', 'memory.persist'] as const;
/** Builtins missing or disabled while the setting is on. Empty = healthy. */
const missingHooks = ref<string[]>([]);
const repairing = ref(false);

const partialInstall = computed(() => enabled.value === true && missingHooks.value.length > 0);

async function checkHooks(): Promise<void> {
  if (enabled.value !== true) {
    missingHooks.value = [];
    return;
  }
  try {
    const hooks = await client.hooks.list();
    missingHooks.value = REQUIRED_BUILTINS.filter(
      (b) => !hooks.some((h) => h.builtin === b && h.enabled),
    );
  } catch {
    // Cannot tell — do not claim a broken install we have not observed.
    missingHooks.value = [];
  }
}

async function refresh(): Promise<void> {
  loadError.value = null;
  try {
    enabled.value = await client.settings.getMemory();
  } catch (e) {
    enabled.value = null;
    loadError.value = e instanceof Error ? e.message : 'Could not read the memory setting.';
    return;
  }
  await checkHooks();
}

async function toggle(event: Event): Promise<void> {
  if (busy.value) return;
  const next = (event.target as HTMLInputElement).checked;
  busy.value = true;
  error.value = null;
  const previous = enabled.value;
  enabled.value = next;
  try {
    await client.settings.setMemory(next);
    if (next) {
      await client.hooks.installStarterMemory();
    } else {
      await client.hooks.removeStarterMemory();
    }
  } catch (e) {
    enabled.value = previous;
    error.value = e instanceof Error ? e.message : 'Failed to toggle memory.';
    busy.value = false;
    // Re-read the truth rather than trusting the rollback: setMemory may
    // have landed before the hook call failed, which is exactly a partial
    // install — surface it now, not on the next load (review F7a).
    await refresh();
    return;
  }
  busy.value = false;
  await checkHooks();
}

async function repair(): Promise<void> {
  if (repairing.value) return;
  repairing.value = true;
  error.value = null;
  try {
    await client.hooks.installStarterMemory();
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to reinstall the memory hooks.';
  } finally {
    repairing.value = false;
  }
  await checkHooks();
}

onMounted(refresh);
</script>

<template>
  <section class="px-6 py-3 border-b border-border-muted" data-testid="memory-capture-section">
    <label class="flex items-start gap-3 cursor-pointer" :class="busy ? 'opacity-60 cursor-wait' : ''">
      <input
        type="checkbox"
        class="accent-accent w-4 h-4 mt-0.5"
        :checked="enabled === true"
        :disabled="busy || enabled === null"
        aria-label="Capture long-term memory"
        data-testid="memory-toggle"
        @change="toggle"
      />
      <span class="flex flex-col gap-0.5">
        <span class="flex items-center gap-2 font-ui text-[13px] text-ink">
          <span>Capture long-term memory</span>
          <span
            v-if="enabled === true"
            class="text-[10px] uppercase tracking-[0.16em] text-signal-ok"
            data-testid="memory-capture-state"
          >on</span>
          <span
            v-else-if="enabled === false"
            class="text-[10px] uppercase tracking-[0.16em] text-ink-subtle"
            data-testid="memory-capture-state"
          >off</span>
        </span>
        <span class="font-ui text-[11px] text-ink-muted max-w-prose">
          When on, each finished turn is captured into memory and the most
          relevant chunks are added before every model call. Turning it off
          stops both; chunks already saved stay below until you forget them.
          Memory needs an embeddings-capable provider: OpenAI, OpenRouter,
          Azure OpenAI, or a custom OpenAI-compatible endpoint (Anthropic and
          Bedrock profiles cannot embed). The Health tab shows which one is in
          use.
        </span>
      </span>
    </label>

    <p
      v-if="enabled === false"
      class="mt-2 rounded-sm border border-border-muted bg-surface-1 px-3 py-2 font-ui text-[12px] text-ink"
      role="status"
      data-testid="memory-off-banner"
    >
      Memory capture is off — nothing new is being captured, and saved chunks
      are not added to conversations.
    </p>

    <div
      v-if="partialInstall"
      class="mt-2 flex flex-wrap items-center gap-3 rounded-sm border border-signal-warning bg-surface-1 px-3 py-2 font-ui text-[12px] text-ink"
      role="alert"
      data-testid="memory-partial-install"
    >
      <span>
        Memory is switched on, but its
        {{ missingHooks.join(' and ') }} hook{{ missingHooks.length === 1 ? ' is' : 's are' }}
        missing or disabled, so {{ missingHooks.length === 2 ? 'nothing is being captured or retrieved' : missingHooks[0] === 'memory.persist' ? 'nothing new is being captured' : 'saved chunks are not being retrieved' }}.
      </span>
      <button
        type="button"
        class="rounded-sm border border-border-muted px-2 py-0.5 text-[11px] text-ink hover:bg-surface-2 disabled:opacity-50"
        :disabled="repairing"
        data-testid="memory-repair-hooks"
        @click="repair"
      >
        {{ repairing ? 'Repairing…' : 'Repair' }}
      </button>
    </div>

    <p
      v-if="loadError"
      class="mt-2 font-ui text-[11px] text-signal-danger"
      role="alert"
      data-testid="memory-setting-load-error"
    >
      Could not read whether memory is on: {{ loadError }}
    </p>
    <p
      v-if="error"
      class="mt-2 font-ui text-[11px] text-signal-danger"
      role="alert"
      data-testid="memory-toggle-error"
    >
      {{ error }}
    </p>
  </section>
</template>
