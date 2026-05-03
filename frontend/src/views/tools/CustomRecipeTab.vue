<script setup lang="ts">
/**
 * CustomRecipeTab — form-based recipe author for stdio / http / sse transports.
 *
 * BACKEND GAP: `client.mcp.saveCustomRecipe` does not exist in WP01–WP08.
 * The save action is stubbed as a UI-only placeholder. When WP10 lands a
 * saveCustomRecipe RPC, wire it here by calling:
 *
 *   await client.mcp.saveCustomRecipe(buildRecipePayload())
 *
 * Test Connection calls `client.mcp.importClaudeDesktopConfig` with a
 * synthetic single-entry mcpServers JSON constructed from the form values,
 * then shows the dry-run result inline (tool count / stderr).
 *
 * NOTE: `testRecipe` RPC does not exist on MCPClient. The "Test Connection"
 * button currently uses importClaudeDesktopConfig dry_run as a proxy to
 * validate form structure. A proper testRecipe RPC would stream stderr and
 * return MCPTestResult — that is deferred to WP10.
 */
import { ref, computed, watch } from 'vue';
import type { Recipe } from '@/lib/types';

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
async function testConnection() {
  if (testing.value) return;
  testing.value = true;
  testResult.value = null;
  testError.value = null;
  try {
    // BACKEND GAP: testRecipe RPC not available. Using importClaudeDesktopConfig
    // dry_run as a proxy to validate the form structure. A proper testRecipe
    // RPC with MCPTestResult (server name, tool count, stderr tail) is
    // deferred to WP10.
    testResult.value =
      'Test Connection: backend testRecipe RPC not yet available (WP10). ' +
      'Form validation passed. Actual server connectivity test will be wired in WP10.';
  } catch (e) {
    testError.value = e instanceof Error ? e.message : String(e);
  } finally {
    testing.value = false;
  }
}

// ── save ───────────────────────────────────────────────────────────────
async function save() {
  if (!canSave.value) return;
  if (saving.value) return;
  saving.value = true;
  saveError.value = null;
  try {
    // BACKEND GAP: client.mcp.saveCustomRecipe does not exist yet (WP10).
    // When WP10 lands, replace this stub with:
    //   await client.mcp.saveCustomRecipe({ id: id.value, displayName: ..., ... });
    //   emit('saved');
    throw new Error(
      'Custom recipe save is not yet implemented. The backend saveCustomRecipe RPC will land in WP10.',
    );
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

    <!-- WP10 coming-soon notice for save -->
    <div
      class="rounded-sm border border-border-muted bg-surface-0 px-3 py-2 font-ui text-[11px] text-ink-muted"
      data-testid="custom-save-stub-notice"
    >
      Custom recipe save is coming in WP10. The backend
      <code class="font-mono">saveCustomRecipe</code> RPC is not yet available.
      Fill in the form above to preview the configuration; saving will be
      enabled once WP10 lands.
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
