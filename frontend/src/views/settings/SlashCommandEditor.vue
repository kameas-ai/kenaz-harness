<script setup lang="ts">
/**
 * SlashCommandEditor — form for authoring and editing a user-defined
 * slash command.
 *
 * Renders kind-specific fields:
 *   text   → plain body textarea
 *   prompt → body textarea with template variable hint strip
 *   tool   → tool-name input + args template textarea
 *
 * "Edit YAML" toggle reveals the raw frontmatter for power users.
 * The component is stateless about persistence; the parent (SlashCommandsView)
 * calls save/cancel handlers. Inline validation mirrors core/slashcmd/Validate.
 *
 * user-slash-commands-01KQ8TD9 WP07.
 */
import { computed, ref, watch } from 'vue';
import Button from '@/components/ui/Button.vue';
import type { UserCommand, UserCommandKind, UserCommandScope } from '@/lib/types';
import { validateUserCommand } from '@/lib/slashcmdValidation';

const props = defineProps<{
  /** Null means "new command". */
  command: UserCommand | null;
  /** When true the form is read-only (bundled/catalog command). */
  readOnly?: boolean;
  saving?: boolean;
}>();

const emit = defineEmits<{
  (e: 'save', cmd: UserCommand): void;
  (e: 'cancel'): void;
  (e: 'delete', name: string): void;
}>();

// ── form state ─────────────────────────────────────────────────────────

const isNew = computed(() => props.command === null);

function blankCommand(): UserCommand {
  return {
    name: '',
    scope: 'global',
    kind: 'text',
    description: '',
    modelInvokable: false,
    body: '',
  };
}

const form = ref<UserCommand>(
  props.command ? { ...props.command } : blankCommand(),
);

// Keep form in sync when the parent swaps the command (e.g. clicking a
// different row while the editor is open).
watch(
  () => props.command,
  (next) => {
    form.value = next ? { ...next } : blankCommand();
    yamlMode.value = false;
    saveError.value = null;
  },
);

const yamlMode = ref(false);
const yamlText = ref('');
const saveError = ref<string | null>(null);

// ── kind sub-fields ─────────────────────────────────────────────────────

const KIND_LABELS: Record<UserCommandKind, string> = {
  text: 'Text expansion',
  prompt: 'Prompt template',
  tool: 'Tool dispatch',
};

const SCOPE_LABELS: Record<UserCommandScope, string> = {
  global: 'Global',
  project: 'Project',
};

const BUILTIN_VARS = [
  { token: '{{selection}}', hint: 'Selected text in editor' },
  { token: '{{cwd}}', hint: 'Working directory' },
  { token: '{{date}}', hint: 'Today\'s date (ISO 8601)' },
  { token: '{{session_id}}', hint: 'Active session ID' },
  { token: '{{project_id}}', hint: 'Active project ID' },
];

// ── validation ─────────────────────────────────────────────────────────

const validationError = computed(() => validateUserCommand(form.value));
const isValid = computed(() => validationError.value === null);

function fieldError(fieldName: string): string | null {
  const err = validationError.value;
  if (!err) return null;
  return err.field === fieldName ? err.message : null;
}

// ── yaml toggle ────────────────────────────────────────────────────────

function enterYamlMode() {
  // Serialize current form to a minimal frontmatter preview (read-only
  // in this version; full round-trip parse is a v2 enhancement).
  const lines: string[] = [
    `name: ${form.value.name || ''}`,
    `scope: ${form.value.scope}`,
    `kind: ${form.value.kind}`,
    `description: ${JSON.stringify(form.value.description || '')}`,
  ];
  if (form.value.whenToUse) lines.push(`when_to_use: ${JSON.stringify(form.value.whenToUse)}`);
  if (form.value.doesNotHandle)
    lines.push(`does_not_handle: ${JSON.stringify(form.value.doesNotHandle)}`);
  if (form.value.modelInvokable) lines.push(`model_invokable: true`);
  if (form.value.icon) lines.push(`icon: ${form.value.icon}`);
  if (form.value.hiddenFromPanel) lines.push(`hidden_from_panel: true`);
  if (form.value.kind === 'tool') {
    if (form.value.tool) lines.push(`tool: ${form.value.tool}`);
    if (form.value.toolArgsTemplate)
      lines.push(`tool_args_template: |\n  ${form.value.toolArgsTemplate.replace(/\n/g, '\n  ')}`);
  }
  yamlText.value = '---\n' + lines.join('\n') + '\n---\n\n' + (form.value.body ?? '');
  yamlMode.value = true;
}

function exitYamlMode() {
  yamlMode.value = false;
}

// ── submit ──────────────────────────────────────────────────────────────

function onSave() {
  if (!isValid.value) return;
  saveError.value = null;
  emit('save', { ...form.value });
}

function onCancel() {
  emit('cancel');
}

function onDelete() {
  if (!form.value.name) return;
  emit('delete', form.value.name);
}

function insertToken(token: string) {
  const bodyEl = document.querySelector<HTMLTextAreaElement>('[data-testid="cmd-body-textarea"]');
  if (!bodyEl) {
    form.value.body = (form.value.body ?? '') + token;
    return;
  }
  const start = bodyEl.selectionStart ?? 0;
  const end = bodyEl.selectionEnd ?? 0;
  const before = (form.value.body ?? '').slice(0, start);
  const after = (form.value.body ?? '').slice(end);
  form.value.body = before + token + after;
  // Restore focus and cursor after DOM update.
  void Promise.resolve().then(() => {
    bodyEl.focus();
    bodyEl.selectionStart = start + token.length;
    bodyEl.selectionEnd = start + token.length;
  });
}
</script>

<template>
  <div class="flex flex-col gap-4" data-testid="slash-command-editor">
    <!-- YAML toggle strip -->
    <div class="flex items-center justify-between">
      <h3 class="font-ui text-[12px] uppercase tracking-[0.18em] text-ink-muted">
        {{ isNew ? 'New command' : `Edit /${form.name}` }}
      </h3>
      <button
        v-if="!readOnly"
        type="button"
        class="font-ui text-[11px] text-ink-muted hover:text-ink transition-colors"
        :data-testid="yamlMode ? 'exit-yaml-mode' : 'enter-yaml-mode'"
        @click="yamlMode ? exitYamlMode() : enterYamlMode()"
      >
        {{ yamlMode ? 'Form view' : 'Edit YAML' }}
      </button>
    </div>

    <!-- ── YAML view (read-only preview) ────────────────────────────── -->
    <template v-if="yamlMode">
      <div
        class="rounded-md border border-border-muted bg-surface-1 px-3 py-2"
        data-testid="yaml-preview"
      >
        <pre class="font-mono text-[11px] text-ink whitespace-pre-wrap">{{ yamlText }}</pre>
      </div>
      <p class="font-ui text-[11px] text-ink-muted">
        To edit YAML directly, open the file in your editor:
        <code class="font-mono text-accent">{{ form.payloadPath || '(not yet saved)' }}</code>
      </p>
    </template>

    <!-- ── Form view ────────────────────────────────────────────────── -->
    <template v-else>
      <!-- Name -->
      <div class="flex flex-col gap-1">
        <label
          for="cmd-name"
          class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted"
        >
          Name
        </label>
        <input
          id="cmd-name"
          v-model="form.name"
          :disabled="readOnly || !isNew"
          type="text"
          placeholder="deploy"
          class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1.5 font-mono text-sm text-ink outline-none focus:border-accent disabled:opacity-50"
          data-testid="cmd-name-input"
        />
        <p
          v-if="fieldError('name')"
          class="font-ui text-[11px] text-signal-danger"
          data-testid="cmd-name-error"
          role="alert"
        >
          {{ fieldError('name') }}
        </p>
      </div>

      <!-- Description -->
      <div class="flex flex-col gap-1">
        <label
          for="cmd-description"
          class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted"
        >
          Description
        </label>
        <input
          id="cmd-description"
          v-model="form.description"
          :disabled="readOnly"
          type="text"
          placeholder="Deploy current branch to staging"
          class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1.5 font-ui text-sm text-ink outline-none focus:border-accent disabled:opacity-50"
          data-testid="cmd-description-input"
        />
      </div>

      <!-- Kind + Scope row -->
      <div class="flex gap-3">
        <div class="flex flex-col gap-1 flex-1">
          <label
            for="cmd-kind"
            class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted"
          >
            Kind
          </label>
          <select
            id="cmd-kind"
            v-model="form.kind"
            :disabled="readOnly"
            class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1.5 font-ui text-sm text-ink outline-none focus:border-accent disabled:opacity-50"
            data-testid="cmd-kind-select"
          >
            <option v-for="(label, k) in KIND_LABELS" :key="k" :value="k">
              {{ label }}
            </option>
          </select>
          <p
            v-if="fieldError('kind')"
            class="font-ui text-[11px] text-signal-danger"
            data-testid="cmd-kind-error"
            role="alert"
          >
            {{ fieldError('kind') }}
          </p>
        </div>

        <div class="flex flex-col gap-1 flex-1">
          <label
            for="cmd-scope"
            class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted"
          >
            Scope
          </label>
          <select
            id="cmd-scope"
            v-model="form.scope"
            :disabled="readOnly"
            class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1.5 font-ui text-sm text-ink outline-none focus:border-accent disabled:opacity-50"
            data-testid="cmd-scope-select"
          >
            <option v-for="(label, s) in SCOPE_LABELS" :key="s" :value="s">
              {{ label }}
            </option>
          </select>
        </div>
      </div>

      <!-- kind=tool fields -->
      <template v-if="form.kind === 'tool'">
        <div class="flex flex-col gap-1">
          <label
            for="cmd-tool"
            class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted"
          >
            Tool name
          </label>
          <input
            id="cmd-tool"
            v-model="form.tool"
            :disabled="readOnly"
            type="text"
            placeholder="kenaz__bash"
            class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1.5 font-mono text-sm text-ink outline-none focus:border-accent disabled:opacity-50"
            data-testid="cmd-tool-input"
          />
          <p
            v-if="fieldError('tool')"
            class="font-ui text-[11px] text-signal-danger"
            data-testid="cmd-tool-error"
            role="alert"
          >
            {{ fieldError('tool') }}
          </p>
        </div>

        <div class="flex flex-col gap-1">
          <label
            for="cmd-tool-args"
            class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted"
          >
            Args template
          </label>
          <textarea
            id="cmd-tool-args"
            v-model="form.toolArgsTemplate"
            :disabled="readOnly"
            rows="3"
            placeholder="./scripts/deploy.sh --env {{env}} {{#ticket}}--ticket {{ticket}}{{/ticket}}"
            class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1.5 font-mono text-sm text-ink outline-none focus:border-accent disabled:opacity-50 resize-y"
            data-testid="cmd-tool-args-textarea"
          />
          <p
            v-if="fieldError('toolArgsTemplate')"
            class="font-ui text-[11px] text-signal-danger"
            data-testid="cmd-tool-args-error"
            role="alert"
          >
            {{ fieldError('toolArgsTemplate') }}
          </p>
        </div>
      </template>

      <!-- kind=text or kind=prompt body -->
      <template v-else>
        <!-- built-in var chips for prompt kind -->
        <div
          v-if="form.kind === 'prompt'"
          class="flex flex-wrap gap-1.5"
          data-testid="builtin-var-chips"
        >
          <button
            v-for="v in BUILTIN_VARS"
            :key="v.token"
            type="button"
            :title="v.hint"
            class="font-mono text-[10px] px-1.5 py-0.5 rounded border border-border-muted bg-surface-2 text-ink-muted hover:text-ink hover:border-accent transition-colors"
            :data-testid="`var-chip-${v.token}`"
            @click="insertToken(v.token)"
          >
            {{ v.token }}
          </button>
        </div>

        <div class="flex flex-col gap-1">
          <label
            for="cmd-body"
            class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted"
          >
            {{ form.kind === 'prompt' ? 'Prompt template' : 'Expansion text' }}
          </label>
          <textarea
            id="cmd-body"
            v-model="form.body"
            :disabled="readOnly"
            rows="6"
            :placeholder="
              form.kind === 'prompt'
                ? 'Write a standup for the {{project_id}} project. Today is {{date}}.\n\n{{#selection}}Here is the code I worked on:\n{{selection}}{{/selection}}'
                : 'What did I accomplish? What am I working on next? Any blockers?'
            "
            class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1.5 font-mono text-sm text-ink outline-none focus:border-accent disabled:opacity-50 resize-y"
            data-testid="cmd-body-textarea"
          />
          <p
            v-if="fieldError('body')"
            class="font-ui text-[11px] text-signal-danger"
            data-testid="cmd-body-error"
            role="alert"
          >
            {{ fieldError('body') }}
          </p>
        </div>
      </template>

      <!-- Optional metadata section -->
      <details class="group">
        <summary
          class="cursor-pointer list-none font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted hover:text-ink transition-colors"
          data-testid="cmd-metadata-toggle"
        >
          More options
        </summary>
        <div class="mt-3 flex flex-col gap-3">
          <!-- when_to_use -->
          <div class="flex flex-col gap-1">
            <label
              for="cmd-when-to-use"
              class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted"
            >
              When to use
            </label>
            <textarea
              id="cmd-when-to-use"
              v-model="form.whenToUse"
              :disabled="readOnly"
              rows="2"
              placeholder="User has finished a unit of work and asks to deploy"
              class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1.5 font-ui text-sm text-ink outline-none focus:border-accent disabled:opacity-50 resize-y"
              data-testid="cmd-when-to-use-textarea"
            />
          </div>

          <!-- does_not_handle -->
          <div class="flex flex-col gap-1">
            <label
              for="cmd-does-not-handle"
              class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted"
            >
              Does not handle
            </label>
            <textarea
              id="cmd-does-not-handle"
              v-model="form.doesNotHandle"
              :disabled="readOnly"
              rows="2"
              placeholder="Deploying to production (manual gate); rollbacks"
              class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1.5 font-ui text-sm text-ink outline-none focus:border-accent disabled:opacity-50 resize-y"
              data-testid="cmd-does-not-handle-textarea"
            />
          </div>

          <!-- model_invokable toggle -->
          <div class="flex items-center gap-3">
            <input
              id="cmd-model-invokable"
              v-model="form.modelInvokable"
              :disabled="readOnly"
              type="checkbox"
              class="accent-accent disabled:opacity-50"
              data-testid="cmd-model-invokable-checkbox"
            />
            <label
              for="cmd-model-invokable"
              class="font-ui text-[12px] text-ink cursor-pointer"
            >
              Model-invokable
              <span class="text-ink-muted">
                — Claude can invoke this command autonomously when relevant.
              </span>
            </label>
          </div>

          <!-- hidden_from_panel toggle -->
          <div class="flex items-center gap-3">
            <input
              id="cmd-hidden-from-panel"
              v-model="form.hiddenFromPanel"
              :disabled="readOnly"
              type="checkbox"
              class="accent-accent disabled:opacity-50"
              data-testid="cmd-hidden-checkbox"
            />
            <label
              for="cmd-hidden-from-panel"
              class="font-ui text-[12px] text-ink cursor-pointer"
            >
              Hide from panel
              <span class="text-ink-muted">
                — still invokable from the composer, but omitted from the slash-command picker.
              </span>
            </label>
          </div>

          <!-- icon -->
          <div class="flex flex-col gap-1">
            <label
              for="cmd-icon"
              class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted"
            >
              Icon (optional)
            </label>
            <input
              id="cmd-icon"
              v-model="form.icon"
              :disabled="readOnly"
              type="text"
              placeholder="rocket"
              class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1.5 font-mono text-sm text-ink outline-none focus:border-accent disabled:opacity-50"
              data-testid="cmd-icon-input"
            />
          </div>
        </div>
      </details>

      <!-- Save error -->
      <p
        v-if="saveError"
        class="font-ui text-[11px] text-signal-danger"
        role="alert"
        data-testid="cmd-save-error"
      >
        {{ saveError }}
      </p>
    </template>

    <!-- Action buttons -->
    <div v-if="!readOnly" class="flex items-center justify-between pt-1">
      <Button
        v-if="!isNew"
        variant="ghost"
        size="sm"
        data-testid="cmd-delete-btn"
        @click="onDelete"
      >
        Delete
      </Button>
      <div v-else />
      <div class="flex gap-2">
        <Button
          variant="ghost"
          size="sm"
          data-testid="cmd-cancel-btn"
          @click="onCancel"
        >
          Cancel
        </Button>
        <Button
          variant="accent"
          size="sm"
          :disabled="!isValid || saving || yamlMode"
          data-testid="cmd-save-btn"
          @click="onSave"
        >
          {{ saving ? 'Saving…' : isNew ? 'Create' : 'Save' }}
        </Button>
      </div>
    </div>
    <div v-else class="flex justify-end pt-1">
      <Button
        variant="ghost"
        size="sm"
        data-testid="cmd-cancel-btn"
        @click="onCancel"
      >
        Close
      </Button>
    </div>
  </div>
</template>
