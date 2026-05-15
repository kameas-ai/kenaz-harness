<script setup lang="ts">
/**
 * LocalRuntimesSection — auto-detected local LLM runtime cards.
 *
 * Shows one card per detected runtime (Ollama, llama-server, LM Studio,
 * Jan, GPT4All). Three card states:
 *   1. running + models — "Add" button + model count badge
 *   2. running + no models — "Add" button with empty-model note
 *   3. installed but not running — greyed "Start runtime" hint
 *
 * The section is absent (not rendered) when HARNESS_LOCAL_RUNTIMES=0
 * (the backend returns an empty array).
 *
 * (local-model-runtimes-01KQ8VMZ WP05)
 */

import { onMounted, ref } from 'vue';
import Button from '@/components/ui/Button.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { LocalRuntimeInfo } from '@/lib/types';

const props = defineProps<{
  /** Called after a successful AutoConfigure to refresh the providers table. */
  onProviderAdded?: () => void;
}>();

const client = useHarnessClient();

const runtimes = ref<LocalRuntimeInfo[]>([]);
const loading = ref(false);
const configuringKind = ref<string | null>(null);
const errorByKind = ref<Record<string, string>>({});
const successKind = ref<string | null>(null);

async function fetchRuntimes(): Promise<void> {
  loading.value = true;
  try {
    const all = await client.llm.listDetectedLocalRuntimes();
    // Only show runtimes that are installed or running — hide completely
    // unknown runtimes that are neither detected nor installed.
    runtimes.value = all.filter((r) => r.running || r.installed);
  } catch {
    // Silently ignore — the section will stay empty.
  } finally {
    loading.value = false;
  }
}

async function rescan(): Promise<void> {
  loading.value = true;
  try {
    const all = await client.llm.rescanLocalRuntimes();
    runtimes.value = all.filter((r) => r.running || r.installed);
  } catch {
    // noop
  } finally {
    loading.value = false;
  }
}

async function addRuntime(kind: string): Promise<void> {
  configuringKind.value = kind;
  delete errorByKind.value[kind];
  successKind.value = null;
  try {
    await client.llm.autoConfigureLocalRuntime(kind);
    successKind.value = kind;
    props.onProviderAdded?.();
  } catch (e) {
    errorByKind.value[kind] =
      e instanceof Error ? e.message : 'Failed to configure provider.';
  } finally {
    configuringKind.value = null;
  }
}

onMounted(() => {
  void fetchRuntimes();
});
</script>

<template>
  <section
    v-if="runtimes.length > 0"
    data-testid="local-runtimes-section"
    class="mb-6"
  >
    <div class="mb-3 flex items-center justify-between">
      <div>
        <span
          class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle font-ui"
        >
          Detected Local Runtimes
        </span>
        <p class="text-xs text-ink-muted mt-0.5">
          LLM runtimes running on this machine. Click "Add" to configure a
          provider profile.
        </p>
      </div>
      <Button
        variant="ghost"
        size="sm"
        :disabled="loading"
        data-testid="rescan-runtimes"
        @click="rescan"
      >
        {{ loading ? 'Scanning…' : 'Re-scan all' }}
      </Button>
    </div>

    <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
      <div
        v-for="rt in runtimes"
        :key="rt.kind"
        class="rounded border bg-surface-1 p-4 flex flex-col gap-2"
        :class="rt.running ? 'border-border-muted' : 'border-border-muted opacity-60'"
        :data-testid="`runtime-card-${rt.kind}`"
      >
        <!-- Card header -->
        <div class="flex items-start justify-between">
          <div>
            <span class="font-ui font-semibold text-sm text-ink">
              {{ rt.name }}
            </span>
            <div class="flex items-center gap-1.5 mt-0.5">
              <span
                class="inline-block h-1.5 w-1.5 rounded-full"
                :class="rt.running ? 'bg-signal-ok' : 'bg-ink-muted'"
              />
              <span class="text-xs text-ink-muted">
                {{ rt.running ? 'Running' : 'Not running' }}
              </span>
            </div>
          </div>

          <!-- Running: show Add button -->
          <Button
            v-if="rt.running"
            variant="accent"
            size="sm"
            :disabled="configuringKind === rt.kind || successKind === rt.kind"
            :data-testid="`add-runtime-${rt.kind}`"
            @click="addRuntime(rt.kind)"
          >
            <span v-if="configuringKind === rt.kind">Adding…</span>
            <span v-else-if="successKind === rt.kind">Added</span>
            <span v-else>Add</span>
          </Button>
        </div>

        <!-- Model count / state details -->
        <div class="text-xs text-ink-muted">
          <template v-if="rt.running && rt.models && rt.models.length > 0">
            {{ rt.models.length }} model{{ rt.models.length === 1 ? '' : 's' }}
            available
          </template>
          <template v-else-if="rt.running">
            No models detected (runtime is running)
          </template>
          <template v-else>
            Installed but not running — start {{ rt.name }} to configure
          </template>
        </div>

        <!-- Error for this card -->
        <p
          v-if="errorByKind[rt.kind]"
          class="text-xs text-signal-danger"
          :data-testid="`runtime-error-${rt.kind}`"
        >
          {{ errorByKind[rt.kind] }}
        </p>
      </div>
    </div>
  </section>
</template>
