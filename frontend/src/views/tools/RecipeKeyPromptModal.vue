<script setup lang="ts">
/**
 * RecipeKeyPromptModal — modal that prompts the user for the recipe's
 * env-key credentials AND its declared ConfigOption values, calls
 * `installRecipe(id, env, config)`, and emits `installed` on success.
 *
 * Design notes:
 *   - Shape mirrors the home-grown PinMenu pattern: no radix-vue, no
 *     teleport — a fixed-position overlay + centred panel.
 *   - Required keys carry an asterisk; submit is disabled until every
 *     required env key has a non-empty value AND every required config
 *     option resolves (non-empty for strings, ≥1 entry for
 *     directory_list, booleans always count as filled).
 *   - "Get a key →" links resolve to `EnvKey.docsUrl`; `target=_blank`
 *     + `rel="noopener"` keep browser-dev parity.
 *   - Keyboard: Esc closes; Enter on the LAST text input submits when
 *     all required fields are filled. Directory chips and inline-edits
 *     never submit on Enter (they commit the edit).
 *   - The plaintext env map and config map are passed once to
 *     `installRecipe` and not retained beyond that call (cleared on
 *     close).
 *   - ConfigOption defaults are pre-filled. directory_list defaults
 *     often carry the `${DATA_DIR}` token; we render it literally as
 *     a chip placeholder and the backend expands at install time.
 *
 * `initialConfig` lets the caller seed the form with the persisted
 * config (used by the "Edit configuration" path so the modal opens
 * with the user's current allowed_directories instead of the recipe
 * default).
 */

import { computed, nextTick, ref, watch } from 'vue';
import type { Recipe, RecipeStatus, ConfigOption } from '@/lib/types';
import DirectoryPicker from './DirectoryPicker.vue';

const props = withDefaults(
  defineProps<{
    open: boolean;
    recipe: Recipe;
    /**
     * Caller-provided installer. Decoupled from the harness client so
     * the surrounding panel can wire in its `useToolsRecipes().install`
     * (or a test stub) without the modal owning the polling lifecycle.
     */
    install: (
      id: string,
      env: Record<string, string>,
      config: Record<string, unknown>,
    ) => Promise<RecipeStatus>;
    /**
     * Optional pre-fill for the config form. Keys outside the
     * recipe's declared ConfigOptions are ignored; missing keys fall
     * back to the option's `default`. Used by the "Edit configuration"
     * path on the filesystem row.
     */
    initialConfig?: Record<string, unknown>;
  }>(),
  {
    initialConfig: () => ({}),
  },
);

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'installed', status: RecipeStatus): void;
}>();

const envValues = ref<Record<string, string>>({});
const configValues = ref<Record<string, unknown>>({});
const submitting = ref(false);
const errorMsg = ref<string | null>(null);
const inputsContainer = ref<HTMLElement | null>(null);
// Hazard-recipe acknowledgment (recipes carrying a `warning` string).
// The user must check this box before Install becomes clickable.
const warningAck = ref(false);
const hasWarning = computed(() => Boolean(props.recipe.warning));

const configOptions = computed<readonly ConfigOption[]>(
  () => props.recipe.configOptions ?? [],
);

const hasEnvSection = computed(() => props.recipe.envKeys.length > 0);
const hasConfigSection = computed(() => configOptions.value.length > 0);

function defaultForOption(opt: ConfigOption): unknown {
  if (props.initialConfig && opt.name in props.initialConfig) {
    return props.initialConfig[opt.name];
  }
  if (opt.default !== undefined) return opt.default;
  switch (opt.kind) {
    case 'directory_list':
      return [];
    case 'boolean':
      return false;
    case 'string':
    default:
      return '';
  }
}

function resetForm() {
  const env: Record<string, string> = {};
  for (const k of props.recipe.envKeys) env[k.name] = '';
  envValues.value = env;

  const cfg: Record<string, unknown> = {};
  for (const opt of configOptions.value) {
    cfg[opt.name] = cloneDefault(defaultForOption(opt), opt);
  }
  configValues.value = cfg;
  submitting.value = false;
  errorMsg.value = null;
}

function cloneDefault(value: unknown, opt: ConfigOption): unknown {
  if (opt.kind === 'directory_list') {
    if (Array.isArray(value)) {
      return value.filter((v): v is string => typeof v === 'string').slice();
    }
    return [];
  }
  if (opt.kind === 'boolean') {
    return typeof value === 'boolean' ? value : false;
  }
  return typeof value === 'string' ? value : '';
}

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      resetForm();
      warningAck.value = false;
      void nextTick(() => {
        const first = inputsContainer.value?.querySelector(
          'input[type="password"], input[type="text"]:not([data-testid^="dirpicker-edit"])',
        ) as HTMLInputElement | null;
        first?.focus();
      });
    } else {
      const cleared: Record<string, string> = {};
      for (const k of props.recipe.envKeys) cleared[k.name] = '';
      envValues.value = cleared;
      configValues.value = {};
    }
  },
  { immediate: true },
);

const requiredEnvFilled = computed(() => {
  for (const k of props.recipe.envKeys) {
    if (k.required && !envValues.value[k.name]?.trim()) return false;
  }
  return true;
});

const requiredConfigFilled = computed(() => {
  for (const opt of configOptions.value) {
    if (!opt.required) continue;
    const v = configValues.value[opt.name];
    if (opt.kind === 'directory_list') {
      if (!Array.isArray(v) || v.length === 0) return false;
    } else if (opt.kind === 'string') {
      if (typeof v !== 'string' || v.trim() === '') return false;
    }
    // boolean: presence is implied (false is a valid filled value).
  }
  return true;
});

const canSubmit = computed(
  () =>
    requiredEnvFilled.value &&
    requiredConfigFilled.value &&
    (!hasWarning.value || warningAck.value),
);

const lastEnvName = computed(
  () =>
    props.recipe.envKeys[props.recipe.envKeys.length - 1]?.name ?? '',
);

function close() {
  if (submitting.value) return;
  emit('close');
}

function dirListDirHint(opt: ConfigOption): string | null {
  if (opt.kind !== 'directory_list') return null;
  const def = opt.default;
  if (!Array.isArray(def)) return null;
  const tokenised = def.find(
    (s): s is string => typeof s === 'string' && s.includes('${DATA_DIR}'),
  );
  if (!tokenised) return null;
  return tokenised;
}

function defaultHintFor(opt: ConfigOption): string | null {
  const tokenised = dirListDirHint(opt);
  if (tokenised) {
    return `default: workspace folder under your harness data directory (${tokenised})`;
  }
  return null;
}

async function submit() {
  if (submitting.value) return;
  if (!canSubmit.value) return;
  submitting.value = true;
  errorMsg.value = null;

  // Snapshot env so the install callback receives a frozen copy
  // independent of further keystrokes.
  const env: Record<string, string> = {};
  for (const k of props.recipe.envKeys) {
    const v = envValues.value[k.name]?.trim() ?? '';
    if (v) env[k.name] = v;
  }

  // Snapshot config — only fields the recipe actually declares end
  // up in the payload. directory_list values are filtered to non-empty
  // strings; boolean / string fields are forwarded verbatim.
  const config: Record<string, unknown> = {};
  for (const opt of configOptions.value) {
    const raw = configValues.value[opt.name];
    if (opt.kind === 'directory_list') {
      if (Array.isArray(raw)) {
        const list = raw
          .filter((v): v is string => typeof v === 'string')
          .map((s) => s.trim())
          .filter((s) => s !== '');
        config[opt.name] = list;
      } else {
        config[opt.name] = [];
      }
    } else if (opt.kind === 'boolean') {
      config[opt.name] = typeof raw === 'boolean' ? raw : false;
    } else {
      config[opt.name] = typeof raw === 'string' ? raw : '';
    }
  }

  try {
    const status = await props.install(props.recipe.id, env, config);
    for (const k of props.recipe.envKeys) envValues.value[k.name] = '';
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
  // Inline-edit chips inside DirectoryPicker handle Enter themselves —
  // they carry a `data-testid` starting with `dirpicker-edit-`. The
  // chip's @keydown handler stops propagation by calling
  // `event.preventDefault()` after committing; we still bail here as
  // a belt-and-braces guard against accidental form submission.
  const testId = target.getAttribute('data-testid') ?? '';
  if (testId.startsWith('dirpicker-edit-')) return;
  if (target.type === 'checkbox') return;
  // Only the last env-key input submits on Enter (matching the
  // pre-WP03 behaviour) — earlier inputs (env or config) let the
  // user tab forward.
  if (
    hasEnvSection.value &&
    target.name === lastEnvName.value &&
    canSubmit.value
  ) {
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
      class="relative z-10 w-[520px] max-w-[90vw] max-h-[80vh] overflow-hidden flex flex-col rounded-md border border-border-muted bg-surface-0 shadow-lg"
    >
      <header
        class="flex items-center justify-between border-b border-border-muted px-5 py-3"
      >
        <div>
          <div
            class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            INSTALL TOOL
          </div>
          <h2 class="mt-1 font-ui text-base font-semibold text-ink">
            Install {{ recipe.displayName }}
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
        class="flex-1 overflow-y-auto px-5 py-4 space-y-5 font-ui"
      >
        <p class="text-[12px] text-ink-muted max-w-prose">
          {{ recipe.description }}
        </p>

        <!-- Hazard banner (recipes carrying a `warning` string) -->
        <section
          v-if="hasWarning"
          class="rounded-sm border-2 border-signal-danger bg-signal-danger/10 px-3 py-3 space-y-2"
          role="alert"
          data-testid="recipe-modal-warning"
        >
          <div
            class="flex items-center gap-2 text-[11px] uppercase tracking-[0.18em] font-semibold text-signal-danger"
          >
            <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M12 9v4M12 17h.01M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
            </svg>
            Hazard
          </div>
          <p
            class="text-[12px] text-signal-danger leading-snug max-w-prose"
            data-testid="recipe-modal-warning-text"
          >
            {{ recipe.warning }}
          </p>
          <div
            v-if="recipe.recommendedPolicyTemplate"
            class="text-[11px] text-ink-muted"
            data-testid="recipe-modal-policy-pointer"
          >
            Recommended Cedar policy:
            <code class="font-mono text-[11px] text-ink">{{ recipe.recommendedPolicyTemplate }}</code>
            — copy this file from the harness install into
            <code class="font-mono text-[11px] text-ink">&lt;DataDir&gt;/policy/</code>
            before enabling.
          </div>
          <label
            class="inline-flex items-start gap-2 cursor-pointer select-none mt-1"
            data-testid="recipe-modal-warning-ack-label"
          >
            <input
              v-model="warningAck"
              type="checkbox"
              class="accent-signal-danger w-4 h-4 mt-0.5"
              data-testid="recipe-modal-warning-ack"
            />
            <span class="font-ui text-[12px] text-ink leading-snug">
              I understand the risk and accept that the model will have the access described above.
            </span>
          </label>
        </section>

        <!-- API Keys section -->
        <section
          v-if="hasEnvSection"
          class="space-y-3"
          data-testid="recipe-modal-env-section"
        >
          <h3
            class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            API Keys
          </h3>
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
              v-model="envValues[key.name]"
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
        </section>

        <!-- Configuration section -->
        <section
          v-if="hasConfigSection"
          class="space-y-3"
          data-testid="recipe-modal-config-section"
        >
          <h3
            class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            Configuration
          </h3>
          <div
            v-for="opt in configOptions"
            :key="opt.name"
            class="space-y-1"
            :data-testid="`recipe-config-row-${opt.name}`"
          >
            <label
              class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
            >
              <span>{{ opt.display }}</span>
              <span
                v-if="opt.required"
                class="ml-1 text-signal-warn"
                aria-label="required"
              >*</span>
            </label>
            <p
              v-if="opt.description"
              class="text-[11px] text-ink-muted max-w-prose"
              :data-testid="`recipe-config-desc-${opt.name}`"
            >
              {{ opt.description }}
            </p>
            <DirectoryPicker
              v-if="opt.kind === 'directory_list'"
              :model-value="(configValues[opt.name] as string[]) ?? []"
              :input-id="opt.name"
              @update:model-value="(v: string[]) => configValues[opt.name] = v"
            />
            <input
              v-else-if="opt.kind === 'string'"
              v-model="configValues[opt.name] as string"
              type="text"
              spellcheck="false"
              autocomplete="off"
              class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 font-mono text-sm text-ink focus:border-accent focus:outline-none"
              :data-testid="`recipe-config-string-${opt.name}`"
            />
            <label
              v-else-if="opt.kind === 'boolean'"
              class="inline-flex items-center gap-2 cursor-pointer select-none"
            >
              <input
                v-model="configValues[opt.name] as boolean"
                type="checkbox"
                class="accent-accent w-4 h-4"
                :data-testid="`recipe-config-bool-${opt.name}`"
              />
              <span class="font-ui text-[12px] text-ink">{{ opt.display }}</span>
            </label>
            <p
              v-if="defaultHintFor(opt)"
              class="text-[11px] text-ink-subtle"
              :data-testid="`recipe-config-hint-${opt.name}`"
            >
              {{ defaultHintFor(opt) }}
            </p>
          </div>
        </section>

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
          :class="[
            'rounded-sm px-3 py-1 text-[12px] disabled:opacity-50 disabled:cursor-not-allowed',
            hasWarning
              ? 'border border-signal-danger bg-signal-danger/10 text-signal-danger hover:bg-signal-danger/20'
              : 'border border-accent-hairline bg-surface-1 text-accent hover:bg-accent-glow',
          ]"
          :disabled="!canSubmit || submitting"
          data-testid="recipe-key-modal-submit"
          @click="submit"
        >
          {{ submitting ? 'Installing…' : hasWarning ? 'Install with risk' : 'Install' }}
        </button>
      </footer>
    </div>
  </div>
</template>
