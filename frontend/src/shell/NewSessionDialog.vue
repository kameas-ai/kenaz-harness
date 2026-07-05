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
import BaseDialog from '@/components/ui/BaseDialog.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { flattenChoices, type ModelChoice } from '@/lib/modelFamily';
import type { Project, Provider } from '@/lib/types';

const props = defineProps<{
  open: boolean;
  /**
   * Optional pre-selection of the project to scope the new session
   * under. Used by ProjectLandingPage's "Start session in this
   * project" CTA so the user doesn't have to re-pick. Empty / unset
   * means the project picker defaults to "(none) — global session".
   */
  initialProjectId?: string;
}>();
const emit = defineEmits<{ (e: 'close'): void }>();

const client = useHarnessClient();
const router = useRouter();

const name = ref('New session');
const providers = ref<readonly Provider[]>([]);
const loadingProviders = ref(false);
const submitting = ref(false);
const errorMsg = ref<string | null>(null);
const selected = ref<ModelChoice | null>(null);

// Project picker state. Empty string => global (no project) session.
const projects = ref<readonly Project[]>([]);
const selectedProjectId = ref<string>('');
const newProjectMode = ref(false);
const newProjectName = ref('');

// Starting-context attachment state. The user picks a small markdown /
// text file; we read it client-side and (depending on the kind toggle)
// either persist it as the session's system prompt or append it as the
// first user message after creation.
type ContextKind = 'system' | 'user_seed';
const MAX_CONTEXT_BYTES = 64 * 1024;
const contextContent = ref<string>('');
const contextFileName = ref<string>('');
const contextByteSize = ref<number>(0);
const contextKind = ref<ContextKind>('system');
const contextError = ref<string | null>(null);

const contextTokenEstimate = computed(() => {
  if (!contextContent.value) return 0;
  return Math.ceil(contextContent.value.length / 4);
});

function clearContext() {
  contextContent.value = '';
  contextFileName.value = '';
  contextByteSize.value = 0;
  contextError.value = null;
}

async function onFileChange(evt: Event) {
  const target = evt.target as HTMLInputElement;
  const file = target.files?.[0];
  if (!file) return;
  contextError.value = null;
  if (file.size > MAX_CONTEXT_BYTES) {
    contextError.value = `File too large (${file.size.toLocaleString()} bytes); max 64 KiB.`;
    target.value = '';
    return;
  }
  try {
    const text = await file.text();
    contextContent.value = text;
    contextFileName.value = file.name;
    contextByteSize.value = file.size;
  } catch (err) {
    contextError.value = err instanceof Error ? err.message : String(err);
    contextContent.value = '';
    contextFileName.value = '';
    contextByteSize.value = 0;
  }
  // Clear the input value so re-selecting the same file fires change.
  target.value = '';
}

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

async function loadProjects() {
  try {
    projects.value = await client.projects.list();
  } catch {
    projects.value = [];
  }
}

function onProjectSelectChange(evt: Event) {
  const v = (evt.target as HTMLSelectElement).value;
  if (v === '__new__') {
    newProjectMode.value = true;
    newProjectName.value = '';
  } else {
    newProjectMode.value = false;
    selectedProjectId.value = v;
  }
}

async function createInlineProject(): Promise<string> {
  const name = newProjectName.value.trim();
  if (!name) {
    return '';
  }
  const created = await client.projects.create(name);
  await loadProjects();
  selectedProjectId.value = created.id;
  newProjectMode.value = false;
  newProjectName.value = '';
  return created.id;
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      name.value = 'New session';
      selected.value = null;
      contextKind.value = 'system';
      selectedProjectId.value = props.initialProjectId ?? '';
      newProjectMode.value = false;
      newProjectName.value = '';
      clearContext();
      void loadProviders();
      void loadProjects();
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
    let projectId = selectedProjectId.value;
    if (newProjectMode.value) {
      projectId = await createInlineProject();
    }
    const session = await client.sessions.create(trimmedName);
    if (projectId) {
      await client.sessions.moveToProject(session.id, projectId);
    }
    // Stash the (provider, model) so SessionsView can seed the
    // active selection on first render — backend Record doesn't
    // carry these fields yet.
    try {
      window.localStorage.setItem(
        `kenaz.session.config.${session.id}`,
        JSON.stringify({
          providerId: selected.value.providerId,
          modelId: selected.value.modelId,
        }),
      );
    } catch {
      /* localStorage unavailable — soft-fail */
    }
    // Apply the starting context if one was attached. system => store
    // as the invisible system prompt; user_seed => append as the
    // visible first user turn AND persist the kind so the rail UI can
    // render context-attached badges later.
    if (contextContent.value) {
      if (contextKind.value === 'system') {
        await client.sessions.setSystemPrompt(
          session.id,
          contextContent.value,
          'system',
        );
      } else {
        await client.sessions.appendMessage(
          session.id,
          'user',
          contextContent.value,
        );
        await client.sessions.setSystemPrompt(session.id, '', 'user_seed');
      }
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
  <BaseDialog
    :open="open"
    title="Configure new session"
    panel-class="z-10 w-[520px] max-w-[90vw] max-h-[80vh] overflow-hidden flex flex-col rounded-md border border-border-muted bg-surface-0 shadow-lg"
    @close="close"
  >
    <div
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
            for="new-session-project"
            class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            Project
          </label>
          <select
            v-if="!newProjectMode"
            id="new-session-project"
            :value="selectedProjectId"
            class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm text-ink focus:border-accent focus:outline-none"
            data-testid="new-session-project"
            @change="onProjectSelectChange"
          >
            <option value="">(none) — global session</option>
            <option v-for="p in projects" :key="p.id" :value="p.id">
              {{ p.name }}
            </option>
            <option value="__new__">+ New project…</option>
          </select>
          <div v-else class="mt-1 flex gap-2">
            <input
              v-model="newProjectName"
              class="flex-1 rounded-sm border border-accent bg-surface-2 px-2.5 py-1.5 text-sm text-ink focus:outline-none"
              placeholder="Project name…"
              data-testid="new-session-new-project-input"
              autofocus
            />
            <button
              type="button"
              class="text-[11px] text-ink-dim hover:text-ink"
              @click="newProjectMode = false; newProjectName = ''"
            >
              Cancel
            </button>
          </div>
          <p
            v-if="selectedProjectId || newProjectMode"
            class="mt-1.5 font-ui text-[11px] text-ink-dim"
            data-testid="new-session-project-scope-hint"
          >
            This session inherits the project's contexts. Files attached
            here go on the SESSION scope only — use the project page
            after creation for project-scope attachments.
          </p>
        </div>
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
            <p>No AI providers configured yet.</p>
            <button
              type="button"
              class="mt-2 inline-block rounded-sm border border-accent px-3 py-1.5 text-xs text-accent hover:bg-accent-glow"
              data-testid="new-session-go-to-providers"
              @click="() => { close(); router.push('/providers'); }"
            >
              Add a provider →
            </button>
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

        <div>
          <span class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            Starting context (optional)
          </span>
          <p class="mt-1 text-[11px] text-ink-dim">
            Attach a Markdown / text file to seed the conversation. Max 64 KiB.
          </p>
          <div class="mt-2 flex items-center gap-2">
            <label
              class="cursor-pointer rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1 text-xs text-ink hover:bg-surface-2"
              :data-testid="'new-session-context-picker'"
            >
              <input
                type="file"
                class="hidden"
                accept=".md,.txt,.json,.yaml,.yml"
                @change="onFileChange"
              />
              Choose file…
            </label>
            <button
              v-if="contextContent"
              type="button"
              class="rounded-sm border border-border-muted px-2 py-1 text-[11px] text-ink-dim hover:text-ink"
              :data-testid="'new-session-context-clear'"
              @click="clearContext"
            >
              Clear
            </button>
          </div>
          <div
            v-if="contextContent"
            class="mt-2 rounded-sm border border-border-muted bg-surface-1 px-3 py-2 text-[11px] text-ink"
            :data-testid="'new-session-context-summary'"
          >
            <div class="font-mono">{{ contextFileName }}</div>
            <div class="mt-0.5 text-ink-dim">
              {{ contextByteSize.toLocaleString() }} bytes ·
              ~{{ contextTokenEstimate }} tokens
            </div>
          </div>
          <div
            v-if="contextError"
            class="mt-2 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 text-[11px] text-signal-danger"
            role="alert"
          >
            {{ contextError }}
          </div>
          <fieldset class="mt-3 space-y-1.5">
            <legend class="text-[10px] uppercase tracking-[0.18em] text-ink-subtle">
              Inject as
            </legend>
            <label class="flex items-center gap-2 text-xs text-ink">
              <input
                type="radio"
                name="new-session-context-kind"
                value="system"
                :checked="contextKind === 'system'"
                :data-testid="'new-session-context-kind-system'"
                @change="contextKind = 'system'"
              />
              <span>System prompt</span>
              <span class="text-ink-dim">(invisible, prepended on every send)</span>
            </label>
            <label class="flex items-center gap-2 text-xs text-ink">
              <input
                type="radio"
                name="new-session-context-kind"
                value="user_seed"
                :checked="contextKind === 'user_seed'"
                :data-testid="'new-session-context-kind-user-seed'"
                @change="contextKind = 'user_seed'"
              />
              <span>First user message</span>
              <span class="text-ink-dim">(visible in transcript)</span>
            </label>
          </fieldset>
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
  </BaseDialog>
</template>
