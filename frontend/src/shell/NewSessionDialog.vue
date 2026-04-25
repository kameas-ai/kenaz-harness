<script setup lang="ts">
/**
 * NewSessionDialog — modal that gates session creation behind a
 * single "configure" step. The user picks a name + a (provider, model)
 * tuple. Submit:
 *   1. Calls client.sessions.create(name) on the harness backend.
 *   2. Stashes the chosen (provider, model) in localStorage keyed by
 *      the new session id so SessionsView can seed activeProvider /
 *      activeModel without a family-default fallback.
 *   3. Navigates to /sessions/<new-id>.
 *
 * Once a session is created with a (kind, model) the chat-surface
 * mid-conversation switcher restricts swaps to the same family —
 * cross-family transfers stay blocked. So the dialog's job is to
 * give the user a one-time, no-family-filter pick.
 */

import { computed, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import Button from '@/components/ui/Button.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { flattenChoices, type ModelChoice } from '@/lib/modelFamily';
import type { Provider } from '@/lib/types';

const props = defineProps<{ open: boolean }>();
const emit = defineEmits<{ (e: 'close'): void }>();

const client = useHarnessClient();
const router = useRouter();

const name = ref('New session');
const providers = ref<readonly Provider[]>([]);
const loadingProviders = ref(false);
const submitting = ref(false);
const errorMsg = ref<string | null>(null);
const selected = ref<ModelChoice | null>(null);

const choices = computed<ModelChoice[]>(() =>
  flattenChoices(providers.value),
);

const choicesByFamily = computed(() => {
  const groups = new Map<string, ModelChoice[]>();
  for (const c of choices.value) {
    const list = groups.get(c.family) ?? [];
    list.push(c);
    groups.set(c.family, list);
  }
  return [...groups.entries()].sort(([a], [b]) => a.localeCompare(b));
});

async function loadProviders() {
  loadingProviders.value = true;
  errorMsg.value = null;
  try {
    providers.value = await client.llm.listProviders();
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
    providers.value = [];
  } finally {
    loadingProviders.value = false;
  }
  // Default selection: first available choice, no family filtering.
  if (!selected.value && choices.value.length > 0) {
    selected.value = choices.value[0];
  }
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      name.value = 'New session';
      selected.value = null;
      void loadProviders();
    }
  },
);

function pick(choice: ModelChoice) {
  selected.value = choice;
}

function close() {
  if (submitting.value) return;
  emit('close');
}

async function onSubmit() {
  if (submitting.value) return;
  if (!selected.value) {
    errorMsg.value = 'Pick a model.';
    return;
  }
  const trimmedName = name.value.trim() || 'New session';
  submitting.value = true;
  errorMsg.value = null;
  try {
    const session = await client.sessions.create(trimmedName);
    // Stash the (provider, model) so SessionsView can seed the
    // active selection on first render — backend Record doesn't
    // carry these fields yet.
    try {
      window.localStorage.setItem(
        `kaneaz.session.config.${session.id}`,
        JSON.stringify({
          providerId: selected.value.providerId,
          modelId: selected.value.modelId,
        }),
      );
    } catch {
      /* localStorage unavailable — soft-fail */
    }
    emit('close');
    await router.push(`/sessions/${session.id}`);
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
  } finally {
    submitting.value = false;
  }
}

const isSelected = (c: ModelChoice) =>
  selected.value !== null &&
  selected.value.providerId === c.providerId &&
  selected.value.modelId === c.modelId;
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    aria-label="Configure new session"
  >
    <div class="absolute inset-0 bg-modal-overlay" @click="close" />
    <div
      class="relative z-10 w-[520px] max-w-[90vw] max-h-[80vh] overflow-hidden flex flex-col rounded-md border border-border-muted bg-surface-0 shadow-lg"
      :data-testid="'new-session-dialog'"
    >
      <header class="flex items-center justify-between border-b border-border-muted px-5 py-3">
        <div>
          <div class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            NEW SESSION
          </div>
          <h2 class="mt-1 font-ui text-base font-semibold text-ink">
            Configure conversation
          </h2>
        </div>
        <Button variant="ghost" size="sm" @click="close">Close</Button>
      </header>

      <div class="flex-1 overflow-y-auto px-5 py-4 space-y-4 font-ui">
        <div>
          <label
            for="new-session-name"
            class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            Name
          </label>
          <input
            id="new-session-name"
            v-model="name"
            class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm text-ink focus:border-accent focus:outline-none"
            autocomplete="off"
            :data-testid="'new-session-name'"
          />
        </div>

        <div>
          <span class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            Model
          </span>
          <p class="mt-1 text-[11px] text-ink-dim">
            Cross-family swaps lock after this point — pick the right family now.
          </p>
          <div
            v-if="loadingProviders"
            class="mt-2 px-2 py-3 text-xs text-ink-muted"
          >
            Loading providers…
          </div>
          <div
            v-else-if="choices.length === 0"
            class="mt-2 rounded-sm border border-signal-warn bg-surface-1 px-3 py-2 text-xs text-ink"
          >
            No providers configured. Add one in /providers first.
          </div>
          <div v-else class="mt-2 space-y-2">
            <div
              v-for="[family, list] in choicesByFamily"
              :key="family"
            >
              <div
                class="px-1 pb-1 text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
              >
                {{ family }}
              </div>
              <div class="rounded-sm border border-border-muted bg-surface-1">
                <button
                  v-for="c in list"
                  :key="`${c.providerId}::${c.modelId}`"
                  type="button"
                  class="block w-full px-3 py-1.5 text-left hover:bg-surface-2 focus:bg-surface-2 focus:outline-none"
                  :class="isSelected(c) ? 'bg-surface-2 ring-1 ring-accent-hairline' : ''"
                  :data-testid="`new-session-choice-${c.providerId}-${c.modelId}`"
                  @click="pick(c)"
                >
                  <div class="flex items-center gap-2">
                    <span
                      class="inline-block h-2 w-2 rounded-full"
                      :class="isSelected(c) ? 'bg-accent' : 'bg-border-muted'"
                    />
                    <span class="font-mono text-xs text-ink">
                      {{ c.modelId }}
                    </span>
                  </div>
                  <div class="mt-0.5 ml-4 text-[10px] text-ink-dim">
                    {{ c.providerName }}
                  </div>
                </button>
              </div>
            </div>
          </div>
        </div>

        <div
          v-if="errorMsg"
          class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 text-xs text-signal-danger"
          role="alert"
        >
          {{ errorMsg }}
        </div>
      </div>

      <footer class="flex items-center justify-end gap-2 border-t border-border-muted px-5 py-3">
        <Button variant="ghost" :disabled="submitting" @click="close">
          Cancel
        </Button>
        <Button
          variant="accent"
          :disabled="!selected || submitting"
          :data-testid="'new-session-create'"
          @click="onSubmit"
        >
          {{ submitting ? 'Creating…' : 'Start session' }}
        </Button>
      </footer>
    </div>
  </div>
</template>
