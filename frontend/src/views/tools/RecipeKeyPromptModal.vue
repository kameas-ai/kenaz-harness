<script setup lang="ts">
/**
 * RecipeKeyPromptModal — modal that prompts the user for the recipe's
 * env-key credentials, calls `installRecipe(id, env)`, and emits
 * `installed` on success.
 *
 * Design notes:
 *   - Shape mirrors the home-grown PinMenu pattern: no radix-vue, no
 *     teleport — a fixed-position overlay + centred panel.
 *   - Required keys carry an asterisk; submit is disabled until every
 *     required key has a non-empty value.
 *   - "Get a key →" links resolve to `EnvKey.docsUrl`; `target=_blank`
 *     + `rel="noopener"` keep browser-dev parity. Wails desktop hands
 *     these off to the OS browser via the same anchor — verified via
 *     the manual A14 walkthrough in `docs/mcp-recipes.md`.
 *   - Keyboard: Esc closes; Enter on the last input submits when all
 *     required keys are filled.
 *   - The plaintext env map is passed once to `installRecipe` and not
 *     retained beyond that call (cleared on close).
 */

import { computed, nextTick, ref, watch } from 'vue';
import type { Recipe, RecipeStatus } from '@/lib/types';

const props = defineProps<{
  open: boolean;
  recipe: Recipe;
  /**
   * Caller-provided installer. Decoupled from the harness client so
   * the surrounding panel can wire in its `useToolsRecipes().install`
   * (or a test stub) without the modal owning the polling lifecycle.
   */
  install: (id: string, env: Record<string, string>) => Promise<RecipeStatus>;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'installed', status: RecipeStatus): void;
}>();

const values = ref<Record<string, string>>({});
const submitting = ref(false);
const errorMsg = ref<string | null>(null);
const inputsContainer = ref<HTMLElement | null>(null);

function resetForm() {
  const next: Record<string, string> = {};
  for (const k of props.recipe.envKeys) next[k.name] = '';
  values.value = next;
  submitting.value = false;
  errorMsg.value = null;
}

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      resetForm();
      void nextTick(() => {
        const first = inputsContainer.value?.querySelector(
          'input[type="password"], input[type="text"]',
        ) as HTMLInputElement | null;
        first?.focus();
      });
    } else {
      // Zero out the in-memory values when the modal closes so the
      // plaintext key never lingers in component state.
      const cleared: Record<string, string> = {};
      for (const k of props.recipe.envKeys) cleared[k.name] = '';
      values.value = cleared;
    }
  },
  { immediate: true },
);

const requiredFilled = computed(() => {
  for (const k of props.recipe.envKeys) {
    if (k.required && !values.value[k.name]?.trim()) return false;
  }
  return true;
});

const lastEnvName = computed(
  () =>
    props.recipe.envKeys[props.recipe.envKeys.length - 1]?.name ?? '',
);

function close() {
  if (submitting.value) return;
  emit('close');
}

async function submit() {
  if (submitting.value) return;
  if (!requiredFilled.value) return;
  submitting.value = true;
  errorMsg.value = null;
  // Clone before handing off so the install callback receives a snapshot
  // independent of further keystrokes (and so callers can zero their copy
  // without racing the modal).
  const env: Record<string, string> = {};
  for (const k of props.recipe.envKeys) {
    const v = values.value[k.name]?.trim() ?? '';
    if (v) env[k.name] = v;
  }
  try {
    const status = await props.install(props.recipe.id, env);
    // Clear plaintext from local state ASAP.
    for (const k of props.recipe.envKeys) values.value[k.name] = '';
    emit('installed', status);
    emit('close');
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : String(e);
  } finally {
    submitting.value = false;
  }
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    close();
    return;
  }
  if (event.key !== 'Enter') return;
  const target = event.target as HTMLInputElement | null;
  if (!target || target.tagName !== 'INPUT') return;
  // Only the last input submits on Enter — earlier inputs let the user
  // tab forward without accidentally firing the install.
  if (target.name === lastEnvName.value && requiredFilled.value) {
    event.preventDefault();
    void submit();
  }
}
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    :aria-label="`Configure ${recipe.displayName}`"
    data-testid="recipe-key-prompt-modal"
    @keydown="onKeydown"
  >
    <div class="absolute inset-0 bg-modal-overlay" @click="close" />
    <div
      class="relative z-10 w-[480px] max-w-[90vw] max-h-[80vh] overflow-hidden flex flex-col rounded-md border border-border-muted bg-surface-0 shadow-lg"
    >
      <header
        class="flex items-center justify-between border-b border-border-muted px-5 py-3"
      >
        <div>
          <div
            class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            CONNECT TOOL
          </div>
          <h2 class="mt-1 font-ui text-base font-semibold text-ink">
            {{ recipe.displayName }}
          </h2>
        </div>
        <button
          type="button"
          class="text-[11px] text-ink-dim hover:text-ink"
          data-testid="recipe-key-modal-close"
          @click="close"
        >
          Close
        </button>
      </header>

      <div
        ref="inputsContainer"
        class="flex-1 overflow-y-auto px-5 py-4 space-y-4 font-ui"
      >
        <p class="text-[12px] text-ink-muted max-w-prose">
          {{ recipe.description }}
        </p>

        <div
          v-for="key in recipe.envKeys"
          :key="key.name"
          class="space-y-1"
        >
          <label
            :for="`recipe-key-${recipe.id}-${key.name}`"
            class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            <span>{{ key.display }}</span>
            <span
              v-if="key.required"
              class="ml-1 text-signal-warn"
              aria-label="required"
            >*</span>
          </label>
          <input
            :id="`recipe-key-${recipe.id}-${key.name}`"
            v-model="values[key.name]"
            :name="key.name"
            type="password"
            autocomplete="off"
            spellcheck="false"
            class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 font-mono text-sm text-ink focus:border-accent focus:outline-none"
            :data-testid="`recipe-key-input-${key.name}`"
          />
          <a
            v-if="key.docsUrl"
            :href="key.docsUrl"
            target="_blank"
            rel="noopener"
            class="inline-block text-[11px] text-accent hover:text-accent-muted"
            :data-testid="`recipe-key-docs-${key.name}`"
          >
            Get a key →
          </a>
        </div>

        <div
          v-if="errorMsg"
          class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 text-[11px] text-signal-danger"
          role="alert"
          data-testid="recipe-key-modal-error"
        >
          {{ errorMsg }}
        </div>
      </div>

      <footer
        class="flex items-center justify-end gap-2 border-t border-border-muted px-5 py-3"
      >
        <button
          type="button"
          class="rounded-sm border border-border-muted px-3 py-1 text-[12px] text-ink-dim hover:text-ink"
          :disabled="submitting"
          @click="close"
        >
          Cancel
        </button>
        <button
          type="button"
          class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1 text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50 disabled:cursor-not-allowed"
          :disabled="!requiredFilled || submitting"
          data-testid="recipe-key-modal-submit"
          @click="submit"
        >
          {{ submitting ? 'Connecting…' : 'Connect' }}
        </button>
      </footer>
    </div>
  </div>
</template>
