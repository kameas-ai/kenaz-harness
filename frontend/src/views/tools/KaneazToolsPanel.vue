<script setup lang="ts">
/**
 * KaneazToolsPanel — built-in toggleable tools shipped with the
 * harness. v1 hosts a single entry, Memory; future entries (web
 * search, fetch, filesystem, …) drop in as new rows once the
 * shipped-MCP-recipes mission lands.
 *
 * Memory toggle wires through Settings.SetMemory + Hooks.Install/Remove
 * StarterMemory so flipping it on auto-installs the memory.persist /
 * memory.retrieve hooks and unhides the Memory tab.
 */
import { onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useHarnessClient } from '@/lib/useHarnessAPI';

const client = useHarnessClient();
const router = useRouter();

const memoryEnabled = ref(false);
const memoryError = ref<string | null>(null);
const memoryBusy = ref(false);

async function refresh() {
  try {
    memoryEnabled.value = await client.settings.getMemory();
  } catch {
    memoryEnabled.value = false;
  }
}

async function toggleMemory(event: Event) {
  if (memoryBusy.value) return;
  const next = (event.target as HTMLInputElement).checked;
  memoryBusy.value = true;
  memoryError.value = null;
  const previous = memoryEnabled.value;
  memoryEnabled.value = next;
  try {
    await client.settings.setMemory(next);
    if (next) {
      await client.hooks.installStarterMemory();
    } else {
      await client.hooks.removeStarterMemory();
    }
  } catch (e) {
    memoryEnabled.value = previous;
    memoryError.value =
      e instanceof Error ? e.message : 'Failed to toggle memory.';
  } finally {
    memoryBusy.value = false;
  }
}

function gotoMemory() {
  void router.push('/memory');
}

onMounted(() => {
  void refresh();
});
</script>

<template>
  <section class="px-6 py-4">
    <h2
      class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle mb-3"
    >
      Kaneaz tools
    </h2>

    <div
      class="rounded-sm border border-border-muted bg-surface-1 divide-y divide-border-muted"
    >
      <!-- Memory tool row -->
      <div class="px-4 py-3 grid gap-3 items-start" style="grid-template-columns: 1fr auto">
        <div>
          <div class="flex items-center gap-2 font-ui text-[13px] text-ink">
            <span>Long-term memory</span>
            <span
              v-if="memoryEnabled"
              class="text-[10px] uppercase tracking-[0.16em] text-signal-ok"
            >
              on
            </span>
          </div>
          <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
            Cross-session memory. Pin messages with 📌 to embed them; the
            harness retrieves relevant memories before each model call. When
            enabled, the memory.retrieve / memory.persist hooks install
            automatically and the Memory tab unhides. Requires an OpenAI
            provider for embeddings. Data lives at
            <span class="font-mono">&lt;DataDir&gt;/memory.gob</span>.
          </p>
          <div v-if="memoryEnabled" class="mt-2">
            <button
              type="button"
              class="px-2 py-0.5 rounded-sm border border-border-muted text-ink text-[10px] uppercase tracking-[0.16em] hover:bg-surface-2"
              data-testid="memory-view-link"
              @click="gotoMemory"
            >
              View saved memories →
            </button>
          </div>
          <div
            v-if="memoryError"
            class="mt-2 text-[11px] text-signal-danger"
            role="alert"
          >
            {{ memoryError }}
          </div>
        </div>
        <label
          class="inline-flex items-center cursor-pointer select-none"
          :class="memoryBusy ? 'opacity-60 cursor-wait' : ''"
        >
          <input
            type="checkbox"
            class="accent-accent w-4 h-4"
            :checked="memoryEnabled"
            :disabled="memoryBusy"
            data-testid="memory-toggle"
            @change="toggleMemory"
          />
        </label>
      </div>
    </div>
  </section>
</template>
