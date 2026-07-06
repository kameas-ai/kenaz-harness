<script setup lang="ts">
/**
 * SlashCommandsView — Settings → Slash Commands tab.
 *
 * Lists all user-defined commands with kind / scope / model-invokable
 * filters. Selecting a row opens the inline SlashCommandEditor.
 * New commands open a blank editor.
 *
 * The tab is reached via /settings?tab=slashcmds. SettingsView mounts
 * this component when route.query.tab === 'slashcmds'.
 *
 * Bundled commands (from catalog mission) will arrive here as read-only
 * rows once that mission lands; for now only user-authored commands are
 * shown.
 *
 * user-slash-commands-01KQ8TD9 WP07.
 */
import { computed, onMounted, ref } from 'vue';
import Button from '@/components/ui/Button.vue';
import SlashCommandEditor from '@/views/settings/SlashCommandEditor.vue';
import SkillsPanel from '@/views/settings/SkillsPanel.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type {
  UserCommand,
  UserCommandKind,
  UserCommandScope,
  UserCommandSummary,
} from '@/lib/types';

const client = useHarnessClient();

// ── list state ─────────────────────────────────────────────────────────

const commands = ref<UserCommandSummary[]>([]);
const loading = ref(false);
const listError = ref<string | null>(null);

// ── filters ────────────────────────────────────────────────────────────

const filterKind = ref<UserCommandKind | 'all'>('all');
const filterScope = ref<UserCommandScope | 'all'>('all');
const filterModelInvokable = ref(false);

const filtered = computed(() => {
  return commands.value.filter((c) => {
    if (filterKind.value !== 'all' && c.kind !== filterKind.value) return false;
    if (filterScope.value !== 'all' && c.scope !== filterScope.value) return false;
    if (filterModelInvokable.value && !c.modelInvokable) return false;
    return true;
  });
});

// ── editor state ───────────────────────────────────────────────────────

const editorOpen = ref(false);
const editorCommand = ref<UserCommand | null>(null);
const saving = ref(false);
const saveError = ref<string | null>(null);
const deleteError = ref<string | null>(null);

// ── data loading ────────────────────────────────────────────────────────

async function refresh(): Promise<void> {
  loading.value = true;
  listError.value = null;
  try {
    commands.value = await client.slashcmd.list('');
  } catch (err) {
    listError.value = err instanceof Error ? err.message : String(err);
    commands.value = [];
  } finally {
    loading.value = false;
  }
}

async function openEdit(summary: UserCommandSummary): Promise<void> {
  saveError.value = null;
  deleteError.value = null;
  try {
    const full = await client.slashcmd.get(summary.name, summary.projectId ?? '');
    editorCommand.value = full;
    editorOpen.value = true;
  } catch (err) {
    listError.value = err instanceof Error ? err.message : String(err);
  }
}

function openCreate(): void {
  saveError.value = null;
  deleteError.value = null;
  editorCommand.value = null;
  editorOpen.value = true;
}

function closeEditor(): void {
  editorOpen.value = false;
  editorCommand.value = null;
  saveError.value = null;
  deleteError.value = null;
}

async function handleSave(cmd: UserCommand): Promise<void> {
  saving.value = true;
  saveError.value = null;
  try {
    await client.slashcmd.save(cmd);
    closeEditor();
    await refresh();
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
  } finally {
    saving.value = false;
  }
}

async function handleDelete(name: string): Promise<void> {
  deleteError.value = null;
  const projectId = editorCommand.value?.projectId ?? '';
  if (!window.confirm(`Delete /${name}? This cannot be undone.`)) return;
  try {
    await client.slashcmd.delete(name, projectId);
    closeEditor();
    await refresh();
  } catch (err) {
    deleteError.value = err instanceof Error ? err.message : String(err);
  }
}

onMounted(() => {
  void refresh();
});

// ── display helpers ────────────────────────────────────────────────────

const KIND_CHIP: Record<UserCommandKind, string> = {
  text: 'bg-surface-2 text-ink-muted',
  tool: 'bg-accent-glow text-accent',
  prompt: 'bg-surface-3 text-ink',
};

const KIND_LABEL: Record<UserCommandKind, string> = {
  text: 'text',
  tool: 'tool',
  prompt: 'prompt',
};
</script>

<template>
  <div class="px-6 py-4 max-w-5xl">
    <div class="mb-4 flex justify-end">
      <Button
        variant="accent"
        data-testid="new-slash-command-btn"
        @click="openCreate"
      >
        New command
      </Button>
    </div>
      <!-- Error banner -->
      <div
        v-if="listError || deleteError"
        class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
        data-testid="slashcmds-error"
      >
        {{ listError || deleteError }}
      </div>

      <!-- Layout: list + editor side by side when editor open -->
      <div class="flex gap-6">
        <!-- ── List side ────────────────────────────────────────────── -->
        <div :class="editorOpen ? 'flex-1 min-w-0' : 'w-full'">
          <!-- Filters -->
          <div class="flex gap-3 mb-3 flex-wrap">
            <div class="flex items-center gap-1.5">
              <label
                for="filter-kind"
                class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted"
              >
                Kind
              </label>
              <select
                id="filter-kind"
                v-model="filterKind"
                class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-xs text-ink outline-none focus:border-accent"
                data-testid="filter-kind-select"
              >
                <option value="all">All</option>
                <option value="text">Text</option>
                <option value="prompt">Prompt</option>
                <option value="tool">Tool</option>
              </select>
            </div>

            <div class="flex items-center gap-1.5">
              <label
                for="filter-scope"
                class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted"
              >
                Scope
              </label>
              <select
                id="filter-scope"
                v-model="filterScope"
                class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-xs text-ink outline-none focus:border-accent"
                data-testid="filter-scope-select"
              >
                <option value="all">All</option>
                <option value="global">Global</option>
                <option value="project">Project</option>
              </select>
            </div>

            <div class="flex items-center gap-1.5">
              <input
                id="filter-model-invokable"
                v-model="filterModelInvokable"
                type="checkbox"
                class="accent-accent"
                data-testid="filter-model-invokable-checkbox"
              />
              <label
                for="filter-model-invokable"
                class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-muted cursor-pointer"
              >
                Model-invokable only
              </label>
            </div>
          </div>

          <!-- Loading -->
          <div
            v-if="loading"
            class="font-ui text-[12px] text-ink-muted"
            role="status"
            data-testid="slashcmds-loading"
          >
            Loading commands…
          </div>

          <!-- Empty state -->
          <div
            v-else-if="commands.length === 0"
            class="rounded-md border border-border-muted bg-surface-1 px-4 py-8 text-center"
            data-testid="slashcmds-empty"
          >
            <p class="font-ui text-sm text-ink mb-1">No user commands yet.</p>
            <p class="font-ui text-[12px] text-ink-muted mb-4">
              Create a text expansion, prompt template, or tool dispatch command.
            </p>
            <Button
              variant="accent"
              data-testid="slashcmds-empty-create-btn"
              @click="openCreate"
            >
              Create your first command
            </Button>
          </div>

          <!-- No filter match -->
          <div
            v-else-if="filtered.length === 0"
            class="rounded-md border border-border-muted bg-surface-1 px-4 py-6 text-center"
            data-testid="slashcmds-no-match"
          >
            <p class="font-ui text-[12px] text-ink-muted">
              No commands match the current filters.
            </p>
          </div>

          <!-- Table -->
          <table
            v-else
            class="w-full border-collapse text-left"
            data-testid="slashcmds-table"
          >
            <thead>
              <tr
                class="border-b border-border-muted text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
              >
                <th class="px-3 py-2 font-ui font-medium">Name</th>
                <th class="px-3 py-2 font-ui font-medium">Description</th>
                <th class="px-3 py-2 font-ui font-medium">Kind</th>
                <th class="px-3 py-2 font-ui font-medium">Scope</th>
                <th class="px-3 py-2 font-ui font-medium">Flags</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="cmd in filtered"
                :key="cmd.name"
                class="border-b border-border-muted hover:bg-surface-2 cursor-pointer"
                :class="editorCommand?.name === cmd.name ? 'bg-surface-2' : ''"
                :data-testid="`slashcmd-row-${cmd.name}`"
                @click="openEdit(cmd)"
              >
                <td class="px-3 py-2 font-mono text-[12px] text-ink">
                  /{{ cmd.name }}
                </td>
                <td class="px-3 py-2 font-ui text-[12px] text-ink-muted max-w-xs truncate">
                  {{ cmd.description || '—' }}
                </td>
                <td class="px-3 py-2">
                  <span
                    :class="KIND_CHIP[cmd.kind]"
                    class="font-ui text-[10px] uppercase tracking-[0.18em] rounded px-1 py-0.5"
                    :data-testid="`slashcmd-kind-chip-${cmd.name}`"
                  >
                    {{ KIND_LABEL[cmd.kind] }}
                  </span>
                </td>
                <td class="px-3 py-2 font-ui text-[11px] text-ink-muted">
                  {{ cmd.scope }}
                </td>
                <td class="px-3 py-2">
                  <span
                    v-if="cmd.modelInvokable"
                    class="font-ui text-[10px] uppercase tracking-[0.18em] text-accent border border-accent-hairline rounded px-1 py-0.5 mr-1"
                    data-testid="`slashcmd-model-invokable-badge-${cmd.name}`"
                  >
                    AI
                  </span>
                  <span
                    v-if="cmd.hiddenFromPanel"
                    class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle border border-border-muted rounded px-1 py-0.5"
                    :data-testid="`slashcmd-hidden-badge-${cmd.name}`"
                  >
                    hidden
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- ── Editor side ─────────────────────────────────────────── -->
        <div
          v-if="editorOpen"
          class="w-96 shrink-0 rounded-md border border-border-muted bg-surface-1 px-4 py-4"
          data-testid="slash-editor-panel"
        >
          <SlashCommandEditor
            :command="editorCommand"
            :saving="saving"
            @save="handleSave"
            @cancel="closeEditor"
            @delete="handleDelete"
          />
          <p
            v-if="saveError"
            class="mt-2 font-ui text-[11px] text-signal-danger"
            role="alert"
            data-testid="slashcmds-save-error"
          >
            {{ saveError }}
          </p>
        </div>
      </div>

      <!-- ── Fleet Skills section (fleet-skills-sync-01NDFSEX18 WP04/WP06) ── -->
      <div class="mt-8 border-t border-border-muted pt-6">
        <SkillsPanel />
      </div>
    </div>
</template>
