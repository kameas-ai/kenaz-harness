<script setup lang="ts">
/**
 * CustomRecipeTab — form-based recipe author for stdio / http / sse transports.
 *
 * save() persists through `MCP_SaveCustomRecipe`
 * (`mcp-connector-lifecycle-01PMMC01` WP06), which validates the
 * assembled recipe against the same rules every shipped recipe must
 * satisfy and writes it to `<DataDir>/mcp/recipes/<id>.yaml` via
 * `recipes.UserStore.Save`. The saved recipe is visible in the Tools
 * list in the same process, without a restart (WP03's freshness
 * contract) — this tab and the row Edit button that lands on it are
 * reachable unconditionally; the WP02 interim flag that used to gate
 * both is retired in the same commit as this wiring.
 *
 * Test Connection stays gated on a persisted id: `MCP_TestRecipe`
 * (`harnessClient.ts:1612` → `core/rpc/bindings.go:497`) is keyed by a
 * recipe id already on disk, so an unsaved draft has nothing to test
 * against yet. Save first, then use the row's own Test/Edit affordances
 * (out of this tab) to verify connectivity — extending this form with a
 * persist-then-test or inline-spec test path is left to a future WP; the
 * spec deliberately does not choose between the two.
 */
import { ref, computed, watch } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { Recipe, MCPSaveCustomRecipeRequest } from '@/lib/types';

const client = useHarnessClient();

type Transport = 'stdio' | 'http' | 'sse';

const props = withDefaults(
  defineProps<{
    /** Pre-filled recipe for edit mode. Pass null for "new recipe" mode. */
    initialRecipe?: Recipe | null;
    /** List of existing recipe ids to check for shadow collisions. */
    existingIds?: readonly string[];
  }>(),
  {
    initialRecipe: null,
    existingIds: () => [],
  },
);

const emit = defineEmits<{
  (e: 'saved'): void;
  (e: 'cancel'): void;
}>();

// ── form fields ────────────────────────────────────────────────────────
const id = ref('');
const displayName = ref('');
const description = ref('');
const transport = ref<Transport>('stdio');
// stdio
const command = ref('');
const args = ref(''); // space-separated
// http / sse
const url = ref('');
const headersTemplate = ref(''); // JSON string
// sse only
const postUrl = ref('');

// ── state ──────────────────────────────────────────────────────────────
const testResult = ref<string | null>(null);
const testError = ref<string | null>(null);
const testing = ref(false);
const saveError = ref<string | null>(null);
const saving = ref(false);

// ── shadow warning ─────────────────────────────────────────────────────
const shadowWarning = computed(() => {
  const tid = id.value.trim();
  if (!tid) return null;
  return props.existingIds.includes(tid)
    ? `This id already exists in shipped/registry; your custom recipe will shadow it.`
    : null;
});

// ── populate from initialRecipe (edit mode) ────────────────────────────
watch(
  () => props.initialRecipe,
  (recipe) => {
    if (!recipe) return;
    id.value = recipe.id;
    displayName.value = recipe.displayName;
    description.value = recipe.description;
    if (recipe.argsTemplate && recipe.argsTemplate.length > 0) {
      command.value = recipe.argsTemplate[0] ?? '';
      args.value = recipe.argsTemplate.slice(1).join(' ');
      transport.value = 'stdio';
    }
  },
  { immediate: true },
);

// ── validation ─────────────────────────────────────────────────────────
const idError = computed(() => {
  const v = id.value.trim();
  if (!v) return 'ID is required.';
  if (!/^[a-z0-9][a-z0-9_-]*$/.test(v))
    return 'ID must be lowercase alphanumeric with hyphens/underscores.';
  return null;
});

const commandError = computed(() => {
  if (transport.value !== 'stdio') return null;
  return command.value.trim() ? null : 'Command is required for stdio transport.';
});

const urlError = computed(() => {
  if (transport.value === 'stdio') return null;
  const v = url.value.trim();
  if (!v) return 'URL is required.';
  try {
    new URL(v);
    return null;
  } catch {
    return 'Must be a valid URL.';
  }
});

const canSave = computed(
  () =>
    !idError.value &&
    !commandError.value &&
    !urlError.value &&
    displayName.value.trim() !== '',
);

// ── test connection ────────────────────────────────────────────────────
// MCP_TestRecipe (harnessClient.ts:1612 -> bindings.go:497) is live, but it
// is keyed by a persisted recipe id — this form has no id until save()
// persists one, and save() is itself stubbed until WP06. So there is
// nothing to `await` here yet: no try/catch, because nothing can throw. Do
// not reintroduce a catch this function structurally cannot reach — wire a
// real `await client.mcp.testRecipe(...)` call here in the same commit that
// gives this form a persisted id to test (WP06), and remove this comment.
async function testConnection() {
  if (testing.value) return;
  testing.value = true;
  testResult.value = null;
  testError.value =
    'Test Connection is unavailable for an unsaved draft: MCP_TestRecipe ' +
    'requires a persisted recipe id, and saving is not yet implemented.';
  testing.value = false;
}

// ── build wire payload ───────────────────────────────────────────────
// Throws (caught by save()) on malformed headers JSON rather than
// silently dropping it — a swallowed parse error would save a recipe
// missing headers the user typed in.
function buildRecipePayload(): MCPSaveCustomRecipeRequest {
  const payload: MCPSaveCustomRecipeRequest = {
    id: id.value.trim(),
    display_name: displayName.value.trim(),
    description: description.value.trim() || undefined,
    transport: transport.value,
  };
  if (transport.value === 'stdio') {
    payload.command = [
      command.value.trim(),
      ...args.value.trim().split(/\s+/).filter(Boolean),
    ];
    return payload;
  }
  payload.url = url.value.trim();
  const headersRaw = headersTemplate.value.trim();
  if (headersRaw) {
    let parsed: unknown;
    try {
      parsed = JSON.parse(headersRaw);
    } catch {
      throw new Error('Headers must be valid JSON.');
    }
    if (
      !parsed ||
      typeof parsed !== 'object' ||
      Array.isArray(parsed)
    ) {
      throw new Error('Headers must be a JSON object.');
    }
    payload.headers_template = parsed as Record<string, string>;
  }
  if (transport.value === 'sse') {
    payload.post_url = postUrl.value.trim();
  }
  return payload;
}

// ── save ───────────────────────────────────────────────────────────────
async function save() {
  if (!canSave.value) return;
  if (saving.value) return;
  saving.value = true;
  saveError.value = null;
  try {
    await client.mcp.saveCustomRecipe(buildRecipePayload());
    emit('saved');
  } catch (e) {
    saveError.value = e instanceof Error ? e.message : String(e);
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div class="space-y-5 font-ui" data-testid="custom-recipe-tab">
    <!-- ID -->
    <div class="space-y-1">
      <label
        for="custom-id"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        ID <span class="ml-1 text-signal-warn" aria-label="required">*</span>
      </label>
      <input
        id="custom-id"
        v-model="id"
        type="text"
        spellcheck="false"
        autocomplete="off"
        placeholder="my-custom-server"
        class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 font-mono text-sm text-ink focus:border-accent focus:outline-none"
        data-testid="custom-id-input"
      />
      <p
        v-if="idError"
        class="text-[11px] text-signal-danger"
        data-testid="custom-id-error"
      >
        {{ idError }}
      </p>
      <p
        v-if="shadowWarning"
        class="text-[11px] text-signal-warn"
        data-testid="custom-shadow-warning"
      >
        {{ shadowWarning }}
      </p>
    </div>

    <!-- Display name -->
    <div class="space-y-1">
      <label
        for="custom-display-name"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Display name <span class="ml-1 text-signal-warn" aria-label="required">*</span>
      </label>
      <input
        id="custom-display-name"
        v-model="displayName"
        type="text"
        spellcheck="false"
        autocomplete="off"
        placeholder="My Custom Server"
        class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 font-mono text-sm text-ink focus:border-accent focus:outline-none"
        data-testid="custom-display-name-input"
      />
    </div>

    <!-- Description -->
    <div class="space-y-1">
      <label
        for="custom-description"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Description
      </label>
      <textarea
        id="custom-description"
        v-model="description"
        rows="2"
        spellcheck="false"
        class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 font-mono text-sm text-ink focus:border-accent focus:outline-none resize-y"
        data-testid="custom-description-input"
      />
    </div>

    <!-- Transport selector -->
    <div class="space-y-2">
      <div class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Transport
      </div>
      <div class="flex gap-4">
        <label
          v-for="t in (['stdio', 'http', 'sse'] as const)"
          :key="t"
          class="inline-flex items-center gap-1.5 cursor-pointer"
        >
          <input
            v-model="transport"
            type="radio"
            :value="t"
            class="accent-accent w-3.5 h-3.5"
            :data-testid="`custom-transport-${t}`"
          />
          <span class="font-ui text-[12px] text-ink uppercase tracking-[0.16em]">{{ t }}</span>
        </label>
      </div>
    </div>

    <!-- stdio fields -->
    <template v-if="transport === 'stdio'">
      <div class="space-y-1">
        <label
          for="custom-command"
          class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
        >
          Command <span class="ml-1 text-signal-warn" aria-label="required">*</span>
        </label>
        <input
          id="custom-command"
          v-model="command"
          type="text"
          spellcheck="false"
          autocomplete="off"
          placeholder="npx"
          class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 font-mono text-sm text-ink focus:border-accent focus:outline-none"
          data-testid="custom-command-input"
        />
        <p
          v-if="commandError"
          class="text-[11px] text-signal-danger"
          data-testid="custom-command-error"
        >
          {{ commandError }}
        </p>
      </div>
      <div class="space-y-1">
        <label
          for="custom-args"
          class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
        >
          Args (space-separated)
        </label>
        <input
          id="custom-args"
          v-model="args"
          type="text"
          spellcheck="false"
          autocomplete="off"
          placeholder="-y @modelcontextprotocol/server-brave-search"
          class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 font-mono text-sm text-ink focus:border-accent focus:outline-none"
          data-testid="custom-args-input"
        />
      </div>
    </template>

    <!-- http / sse fields -->
    <template v-if="transport === 'http' || transport === 'sse'">
      <div class="space-y-1">
        <label
          for="custom-url"
          class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
        >
          URL <span class="ml-1 text-signal-warn" aria-label="required">*</span>
        </label>
        <input
          id="custom-url"
          v-model="url"
          type="text"
          spellcheck="false"
          autocomplete="off"
          placeholder="https://my-server.example.com/mcp"
          class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 font-mono text-sm text-ink focus:border-accent focus:outline-none"
          data-testid="custom-url-input"
        />
        <p
          v-if="urlError"
          class="text-[11px] text-signal-danger"
          data-testid="custom-url-error"
        >
          {{ urlError }}
        </p>
      </div>
      <div class="space-y-1">
        <label
          for="custom-headers"
          class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
        >
          Headers (JSON)
        </label>
        <textarea
          id="custom-headers"
          v-model="headersTemplate"
          rows="3"
          spellcheck="false"
          autocomplete="off"
          placeholder='{"Authorization": "Bearer $TOKEN"}'
          class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 font-mono text-sm text-ink focus:border-accent focus:outline-none resize-y"
          data-testid="custom-headers-input"
        />
      </div>
    </template>

    <!-- sse-only: post url -->
    <template v-if="transport === 'sse'">
      <div class="space-y-1">
        <label
          for="custom-post-url"
          class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
        >
          Post URL (SSE)
        </label>
        <input
          id="custom-post-url"
          v-model="postUrl"
          type="text"
          spellcheck="false"
          autocomplete="off"
          placeholder="https://my-server.example.com/mcp/send"
          class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 font-mono text-sm text-ink focus:border-accent focus:outline-none"
          data-testid="custom-post-url-input"
        />
      </div>
    </template>

    <!-- Test connection -->
    <div class="space-y-2">
      <button
        type="button"
        class="rounded-sm border border-border-muted px-3 py-1 font-ui text-[12px] text-ink-dim hover:text-ink hover:bg-surface-2 disabled:opacity-50 disabled:cursor-not-allowed"
        :disabled="testing || !canSave"
        data-testid="custom-test-btn"
        @click="testConnection"
      >
        {{ testing ? 'Testing…' : 'Test Connection' }}
      </button>
      <div
        v-if="testResult"
        class="rounded-sm border border-border-muted bg-surface-0 px-3 py-2 font-ui text-[11px] text-ink-muted"
        data-testid="custom-test-result"
      >
        {{ testResult }}
      </div>
      <div
        v-if="testError"
        class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[11px] text-signal-danger"
        role="alert"
        data-testid="custom-test-error"
      >
        {{ testError }}
      </div>
    </div>

    <!-- Save + cancel actions -->
    <div
      v-if="saveError"
      class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="custom-save-error"
    >
      {{ saveError }}
    </div>
    <div class="flex gap-2">
      <button
        type="button"
        class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1.5 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50 disabled:cursor-not-allowed"
        :disabled="!canSave || saving"
        data-testid="custom-save-btn"
        @click="save"
      >
        {{ saving ? 'Saving…' : 'Save recipe' }}
      </button>
      <button
        type="button"
        class="rounded-sm border border-border-muted px-3 py-1.5 font-ui text-[12px] text-ink-dim hover:text-ink"
        data-testid="custom-cancel-btn"
        @click="emit('cancel')"
      >
        Cancel
      </button>
    </div>
  </div>
</template>
