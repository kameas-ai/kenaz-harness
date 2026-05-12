<script setup lang="ts">
/**
 * HookEditor — create / edit form for a single lifecycle hook.
 *
 * Props:
 *   - hook: the Hook to edit (null = create mode).
 *   - builtins: descriptor list for the builtin picker.
 *   - saving: true while the parent is awaiting the save RPC.
 *
 * Emits:
 *   - save(hook): validated hook ready to persist.
 *   - cancel: user dismissed without saving.
 *   - dryRun(hook): user wants to open the dry-run drawer.
 *
 * hooks-event-surface-expansion-01KZNP3A WP07c.
 */
import { ref, computed, watch } from 'vue';
import Button from '@/components/ui/Button.vue';
import { ALL_HOOK_EVENTS, HOOK_KINDS } from '@/lib/hooks';
import type { Hook, BuiltinDescriptor } from '@/lib/types';

const props = defineProps<{
  hook: Hook | null;
  builtins: readonly BuiltinDescriptor[];
  saving: boolean;
}>();

const emit = defineEmits<{
  (e: 'save', hook: Hook): void;
  (e: 'cancel'): void;
  (e: 'dryRun', hook: Hook): void;
}>();

// ── local draft ────────────────────────────────────────────────────────

function blankHook(): Hook {
  return {
    id: `hk-${Math.random().toString(36).slice(2, 8)}`,
    name: 'New hook',
    event: 'pre_send',
    kind: 'shell',
    enabled: true,
    match: {},
  };
}

const draft = ref<Hook>(props.hook ? JSON.parse(JSON.stringify(props.hook)) : blankHook());

// When the parent swaps the hook prop (e.g. opening a different hook
// without unmounting the editor), resync the draft.
watch(
  () => props.hook,
  (incoming) => {
    draft.value = incoming ? JSON.parse(JSON.stringify(incoming)) : blankHook();
    validationError.value = null;
  },
);

const isCreate = computed(() => !props.hook);

// ── validation ─────────────────────────────────────────────────────────

const validationError = ref<string | null>(null);

function validate(h: Hook): string | null {
  if (!h.id.trim()) return 'ID is required.';
  if (!h.name.trim()) return 'Name is required.';
  if (h.kind === 'shell' && !h.command?.trim()) return 'Command is required for shell hooks.';
  if (h.kind === 'builtin' && !h.builtin?.trim()) return 'Builtin ID is required for builtin hooks.';
  if (h.kind === 'mcp' && !h.mcpTool?.trim()) return 'MCP tool name is required for MCP hooks.';
  return null;
}

// ── match filter helpers ────────────────────────────────────────────────

function joinList(arr?: string[]): string {
  return (arr ?? []).join(', ');
}
function splitList(s: string): string[] {
  return s
    .split(',')
    .map((x) => x.trim())
    .filter(Boolean);
}

// ── actions ─────────────────────────────────────────────────────────────

function handleSave(): void {
  const err = validate(draft.value);
  if (err) {
    validationError.value = err;
    return;
  }
  validationError.value = null;
  emit('save', JSON.parse(JSON.stringify(draft.value)) as Hook);
}

function handleDryRun(): void {
  const err = validate(draft.value);
  if (err) {
    validationError.value = err;
    return;
  }
  validationError.value = null;
  emit('dryRun', JSON.parse(JSON.stringify(draft.value)) as Hook);
}
</script>

<template>
  <div data-testid="hook-editor">
    <header class="flex items-center justify-between mb-4">
      <div>
        <div class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          {{ isCreate ? 'ADD HOOK' : 'EDIT HOOK' }}
        </div>
        <h2 class="mt-0.5 font-ui text-base font-semibold text-ink">
          {{ draft.name || draft.id }}
        </h2>
      </div>
      <Button variant="ghost" data-testid="hook-editor-cancel" @click="emit('cancel')">
        Cancel
      </Button>
    </header>

    <div
      v-if="validationError"
      class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="hook-editor-validation-error"
    >
      {{ validationError }}
    </div>

    <div class="grid gap-3">
      <!-- ID -->
      <label class="grid gap-1">
        <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">ID</span>
        <input
          v-model="draft.id"
          class="font-mono text-xs px-2 py-1.5 rounded-sm border border-border bg-surface-1 text-ink focus:outline-none focus:border-accent"
          data-testid="hook-editor-id"
          placeholder="hk-my-hook"
        />
      </label>

      <!-- Name -->
      <label class="grid gap-1">
        <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">Name</span>
        <input
          v-model="draft.name"
          class="text-sm px-2 py-1.5 rounded-sm border border-border bg-surface-1 text-ink focus:outline-none focus:border-accent"
          data-testid="hook-editor-name"
          placeholder="My hook"
        />
      </label>

      <!-- Event picker -->
      <label class="grid gap-1">
        <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">Event</span>
        <select
          v-model="draft.event"
          class="text-sm px-2 py-1.5 rounded-sm border border-border bg-surface-1 text-ink focus:outline-none focus:border-accent"
          data-testid="hook-editor-event"
        >
          <option v-for="ev in ALL_HOOK_EVENTS" :key="ev" :value="ev">{{ ev }}</option>
        </select>
      </label>

      <!-- Kind picker -->
      <label class="grid gap-1">
        <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">Kind</span>
        <select
          v-model="draft.kind"
          class="text-sm px-2 py-1.5 rounded-sm border border-border bg-surface-1 text-ink focus:outline-none focus:border-accent"
          data-testid="hook-editor-kind"
        >
          <option v-for="k in HOOK_KINDS" :key="k" :value="k">{{ k }}</option>
        </select>
      </label>

      <!-- kind=builtin: builtin picker -->
      <label v-if="draft.kind === 'builtin'" class="grid gap-1">
        <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">Builtin</span>
        <select
          v-model="draft.builtin"
          class="text-sm px-2 py-1.5 rounded-sm border border-border bg-surface-1 text-ink focus:outline-none focus:border-accent"
          data-testid="hook-editor-builtin"
        >
          <option value="">— select builtin —</option>
          <option v-for="d in builtins" :key="d.id" :value="d.id">
            {{ d.id }} — {{ d.name }}
          </option>
        </select>
        <p
          v-if="draft.builtin"
          class="font-ui text-[11px] text-ink-muted"
          data-testid="hook-editor-builtin-desc"
        >
          {{ builtins.find((b) => b.id === draft.builtin)?.description ?? '' }}
        </p>
      </label>

      <!-- kind=shell: command textarea -->
      <label v-if="draft.kind === 'shell'" class="grid gap-1">
        <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">Command</span>
        <textarea
          v-model="draft.command"
          class="text-xs font-mono px-2 py-1.5 rounded-sm border border-border bg-surface-1 text-ink focus:outline-none focus:border-accent resize-y"
          rows="3"
          data-testid="hook-editor-command"
          placeholder="echo '{}' | jq ."
        />
        <p class="font-ui text-[10px] text-ink-subtle">
          Receives <code class="font-mono">{"input": &lt;event payload&gt;}</code> on stdin.
          Write <code class="font-mono">{"decision":"approve"}</code> (or any HookOutput) on stdout.
        </p>
      </label>

      <!-- kind=mcp: tool name -->
      <label v-if="draft.kind === 'mcp'" class="grid gap-1">
        <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">MCP tool</span>
        <input
          v-model="draft.mcpTool"
          class="text-sm px-2 py-1.5 rounded-sm border border-border bg-surface-1 text-ink focus:outline-none focus:border-accent"
          data-testid="hook-editor-mcp-tool"
          placeholder="server_name::tool_name"
        />
      </label>

      <!-- Per-hook timeout (typed wire field as of v0.8.x). -->
      <label class="grid gap-1">
        <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">
          Timeout (ms)
          <span class="normal-case text-[10px] text-ink-subtle ml-1">0 = default (5 000 ms sync)</span>
        </span>
        <input
          :value="draft.timeoutMs ?? 0"
          type="number"
          min="0"
          class="text-sm px-2 py-1.5 rounded-sm border border-border bg-surface-1 text-ink focus:outline-none focus:border-accent w-32"
          data-testid="hook-editor-timeout"
          @input="(e) => {
            const v = parseInt((e.target as HTMLInputElement).value, 10);
            draft.timeoutMs = isNaN(v) ? 0 : v;
          }"
        />
      </label>

      <!-- Match filters -->
      <fieldset class="rounded-md border border-border-muted px-3 py-2">
        <legend class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle px-1">
          Match filters
          <span class="normal-case text-[10px] text-ink-subtle ml-1">(empty = any)</span>
        </legend>
        <label class="block mt-2">
          <span class="font-ui text-[11px] text-ink-muted">Session IDs (comma-separated)</span>
          <input
            :value="joinList(draft.match.sessionIds)"
            class="mt-1 w-full text-xs font-mono px-2 py-1 rounded-sm border border-border bg-surface-1 text-ink focus:outline-none focus:border-accent"
            data-testid="hook-editor-match-sessions"
            @input="(e) => (draft.match.sessionIds = splitList((e.target as HTMLInputElement).value))"
          />
        </label>
        <label class="block mt-2">
          <span class="font-ui text-[11px] text-ink-muted">Kinds (comma-separated)</span>
          <input
            :value="joinList(draft.match.kinds)"
            class="mt-1 w-full text-xs font-mono px-2 py-1 rounded-sm border border-border bg-surface-1 text-ink focus:outline-none focus:border-accent"
            data-testid="hook-editor-match-kinds"
            @input="(e) => (draft.match.kinds = splitList((e.target as HTMLInputElement).value))"
          />
        </label>
        <label class="block mt-2">
          <span class="font-ui text-[11px] text-ink-muted">Models (comma-separated)</span>
          <input
            :value="joinList(draft.match.models)"
            class="mt-1 w-full text-xs font-mono px-2 py-1 rounded-sm border border-border bg-surface-1 text-ink focus:outline-none focus:border-accent"
            data-testid="hook-editor-match-models"
            @input="(e) => (draft.match.models = splitList((e.target as HTMLInputElement).value))"
          />
        </label>
      </fieldset>

      <!-- Enabled toggle -->
      <label class="flex items-center gap-2 cursor-pointer">
        <input
          type="checkbox"
          :checked="draft.enabled"
          data-testid="hook-editor-enabled"
          @change="(e) => (draft.enabled = (e.target as HTMLInputElement).checked)"
        />
        <span class="font-ui text-sm text-ink">Enabled</span>
      </label>

      <!-- Actions -->
      <div class="flex justify-between items-center pt-2 gap-2">
        <!-- Dry-run: only available on shell hooks since builtin/mcp return stubs -->
        <Button
          variant="ghost"
          data-testid="hook-editor-dry-run"
          @click="handleDryRun"
        >
          Dry run
        </Button>
        <div class="flex gap-2">
          <Button variant="ghost" data-testid="hook-editor-cancel-footer" @click="emit('cancel')">
            Cancel
          </Button>
          <Button
            variant="accent"
            :disabled="saving"
            data-testid="hook-editor-save"
            @click="handleSave"
          >
            {{ saving ? 'Saving…' : 'Save' }}
          </Button>
        </div>
      </div>
    </div>
  </div>
</template>
