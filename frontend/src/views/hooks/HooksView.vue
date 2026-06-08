<script setup lang="ts">
/**
 * HooksView — lifecycle-hook configuration surface (NN/SECTION pattern).
 *
 * Lists every configured hook (id, name, event, kind, enabled,
 * match-summary, actions). Selecting a row opens a drawer with an
 * edit form: name, event picker, kind picker (builtin / shell / mcp),
 * builtin/command/mcp-tool fields with conditional rendering, match
 * filters (sessions / kinds / models), enabled toggle.
 *
 * The "Defaults" CTA on first visit installs the two starter memory
 * hooks (memory.retrieve + memory.persist). Settings's long-term-memory
 * toggle drives the same install/remove pair.
 */
import { computed, onMounted, ref } from 'vue';
import SettingsShell from '@/views/settings/SettingsShell.vue';
import Button from '@/components/ui/Button.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { Hook, HookEvent, HookKind, BuiltinDescriptor } from '@/lib/types';

const client = useHarnessClient();

const hooks = ref<readonly Hook[]>([]);
const builtins = ref<readonly BuiltinDescriptor[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);
const drawerOpen = ref(false);
const editingHook = ref<Hook | null>(null);

const hasStarterHooks = computed(() =>
  hooks.value.some(
    (h) => h.id === 'starter:memory.retrieve' || h.id === 'starter:memory.persist',
  ),
);

async function refresh(): Promise<void> {
  loading.value = true;
  error.value = null;
  try {
    const [list, descs] = await Promise.all([
      client.hooks.list(),
      client.hooks.availableBuiltins(),
    ]);
    hooks.value = list;
    builtins.value = descs;
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    hooks.value = [];
    builtins.value = [];
  } finally {
    loading.value = false;
  }
}

async function installStarterMemory(): Promise<void> {
  error.value = null;
  try {
    await client.hooks.installStarterMemory();
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

async function removeHook(id: string): Promise<void> {
  if (!window.confirm(`Remove hook ${id}?`)) return;
  error.value = null;
  try {
    await client.hooks.remove(id);
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

function openCreateDrawer(): void {
  editingHook.value = blankHook();
  drawerOpen.value = true;
}

function openEditDrawer(h: Hook): void {
  editingHook.value = JSON.parse(JSON.stringify(h)) as Hook;
  drawerOpen.value = true;
}

function closeDrawer(): void {
  drawerOpen.value = false;
  editingHook.value = null;
}

async function saveHook(): Promise<void> {
  if (!editingHook.value) return;
  error.value = null;
  try {
    const existing = hooks.value.find((h) => h.id === editingHook.value!.id);
    if (existing) {
      await client.hooks.update(editingHook.value);
    } else {
      await client.hooks.add(editingHook.value);
    }
    closeDrawer();
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

async function toggleEnabled(h: Hook): Promise<void> {
  error.value = null;
  try {
    await client.hooks.update({ ...h, enabled: !h.enabled });
    await refresh();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

function blankHook(): Hook {
  return {
    id: `hk-${Math.random().toString(36).slice(2, 8)}`,
    name: 'New hook',
    event: 'pre_send',
    kind: 'builtin',
    enabled: true,
    match: {},
  };
}

function matchSummary(h: Hook): string {
  const parts: string[] = [];
  if (h.match.sessionIds?.length) parts.push(`${h.match.sessionIds.length} session(s)`);
  if (h.match.kinds?.length) parts.push(h.match.kinds.join(','));
  if (h.match.models?.length) parts.push(h.match.models.join(','));
  return parts.length === 0 ? 'any' : parts.join(' · ');
}

function joinList(arr?: string[]): string {
  return (arr ?? []).join(',');
}
function splitList(s: string): string[] {
  return s
    .split(',')
    .map((x) => x.trim())
    .filter(Boolean);
}

onMounted(() => {
  void refresh();
});

const events: HookEvent[] = [
  'pre_send',
  'post_send',
  'pre_save_session',
  'post_assistant_turn_complete',
];

const kinds: HookKind[] = ['builtin', 'shell', 'mcp'];
</script>

<template>
  <SettingsShell
    number="08"
    section="HOOKS"
    title="Lifecycle hooks"
    subtitle="Configurable side-effects that fire on chat-pipeline events. Memory.retrieve / memory.persist are the v1 preinstalled builtins."
  >
    <template #head-trailing>
      <Button
        variant="accent"
        :data-testid="'open-add-hook'"
        @click="openCreateDrawer"
      >
        Add hook
      </Button>
    </template>

    <div class="px-6 py-4 max-w-5xl">
      <div
        v-if="error"
        class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
        data-testid="hooks-error"
      >
        {{ error }}
      </div>

      <div v-if="loading" class="font-ui text-[12px] text-ink-muted" role="status">
        Loading hooks…
      </div>

      <div
        v-else-if="hooks.length === 0"
        class="rounded-md border border-border-muted bg-surface-1 px-4 py-6 text-center"
        data-testid="hooks-empty-state"
      >
        <p class="font-ui text-sm text-ink">
          No hooks configured yet. Install the starter memory hooks to get
          long-term memory across sessions.
        </p>
        <div class="mt-3">
          <Button
            variant="accent"
            data-testid="install-starter-hooks"
            @click="installStarterMemory"
          >
            Install starter memory hooks
          </Button>
        </div>
      </div>

      <div v-else>
        <div v-if="!hasStarterHooks" class="mb-3">
          <Button
            variant="ghost"
            data-testid="install-starter-hooks"
            @click="installStarterMemory"
          >
            Install starter memory hooks
          </Button>
        </div>

        <table
          class="w-full border-collapse text-left"
          data-testid="hooks-table"
        >
          <thead>
            <tr
              class="border-b border-border-muted text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
            >
              <th class="px-3 py-2 font-ui font-medium">Name</th>
              <th class="px-3 py-2 font-ui font-medium">Event</th>
              <th class="px-3 py-2 font-ui font-medium">Kind</th>
              <th class="px-3 py-2 font-ui font-medium">Match</th>
              <th class="px-3 py-2 font-ui font-medium">Enabled</th>
              <th class="px-3 py-2 font-ui font-medium text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="h in hooks"
              :key="h.id"
              class="border-b border-border-muted hover:bg-surface-2 cursor-pointer"
              :data-testid="`hook-row-${h.id}`"
              @click="openEditDrawer(h)"
            >
              <td class="px-3 py-2 font-ui text-sm text-ink">
                {{ h.name }}
                <div class="font-mono text-[10px] text-ink-subtle">{{ h.id }}</div>
              </td>
              <td class="px-3 py-2 font-mono text-xs text-ink-muted">{{ h.event }}</td>
              <td class="px-3 py-2 font-mono text-xs text-ink-muted">
                {{ h.kind }}
                <span v-if="h.builtin" class="text-ink-subtle"> · {{ h.builtin }}</span>
              </td>
              <td class="px-3 py-2 font-mono text-xs text-ink-muted">{{ matchSummary(h) }}</td>
              <td class="px-3 py-2">
                <input
                  type="checkbox"
                  :checked="h.enabled"
                  :aria-label="`Enable hook ${h.id}`"
                  :data-testid="`hook-toggle-${h.id}`"
                  @click.stop
                  @change="toggleEnabled(h)"
                />
              </td>
              <td class="px-3 py-2 text-right">
                <button
                  type="button"
                  class="px-2 py-1 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:text-signal-danger hover:bg-surface-3"
                  :data-testid="`hook-remove-${h.id}`"
                  @click.stop="removeHook(h.id)"
                >
                  Remove
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Right-anchored drawer for the edit form -->
    <div
      v-if="drawerOpen && editingHook"
      class="fixed inset-0 z-50 flex"
      role="dialog"
      aria-modal="true"
      aria-label="Edit hook"
    >
      <div
        class="flex-1 bg-modal-overlay"
        data-testid="hook-drawer-scrim"
        @click="closeDrawer"
      />
      <div
        class="w-[460px] max-w-full overflow-y-auto border-l border-border-muted bg-surface-0 shadow-lg"
        data-testid="hook-drawer"
      >
        <header
          class="flex items-center justify-between border-b border-border-muted px-6 py-4"
        >
          <div>
            <div
              class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
            >
              EDIT HOOK
            </div>
            <h2 class="mt-1 font-ui text-lg font-semibold text-ink">
              {{ editingHook.name || editingHook.id }}
            </h2>
          </div>
          <Button variant="ghost" @click="closeDrawer">Close</Button>
        </header>

        <div class="px-6 py-4 grid gap-4">
          <label class="grid gap-1">
            <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">ID</span>
            <input
              v-model="editingHook.id"
              class="font-mono text-xs px-2 py-1 rounded-sm border border-border bg-surface-1 text-ink"
              data-testid="hook-form-id"
            />
          </label>
          <label class="grid gap-1">
            <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">Name</span>
            <input
              v-model="editingHook.name"
              class="text-sm px-2 py-1 rounded-sm border border-border bg-surface-1 text-ink"
              data-testid="hook-form-name"
            />
          </label>
          <label class="grid gap-1">
            <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">Event</span>
            <select
              v-model="editingHook.event"
              class="text-sm px-2 py-1 rounded-sm border border-border bg-surface-1 text-ink"
              data-testid="hook-form-event"
            >
              <option v-for="e in events" :key="e" :value="e">{{ e }}</option>
            </select>
          </label>
          <label class="grid gap-1">
            <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">Kind</span>
            <select
              v-model="editingHook.kind"
              class="text-sm px-2 py-1 rounded-sm border border-border bg-surface-1 text-ink"
              data-testid="hook-form-kind"
            >
              <option v-for="k in kinds" :key="k" :value="k">{{ k }}</option>
            </select>
          </label>

          <label v-if="editingHook.kind === 'builtin'" class="grid gap-1">
            <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">Builtin</span>
            <select
              v-model="editingHook.builtin"
              class="text-sm px-2 py-1 rounded-sm border border-border bg-surface-1 text-ink"
              data-testid="hook-form-builtin"
            >
              <option value="">—</option>
              <option v-for="d in builtins" :key="d.id" :value="d.id">
                {{ d.id }} — {{ d.name }}
              </option>
            </select>
          </label>

          <label v-if="editingHook.kind === 'shell'" class="grid gap-1">
            <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">Command</span>
            <textarea
              v-model="editingHook.command"
              class="text-xs font-mono px-2 py-1 rounded-sm border border-border bg-surface-1 text-ink"
              rows="3"
              data-testid="hook-form-command"
            />
          </label>

          <label v-if="editingHook.kind === 'mcp'" class="grid gap-1">
            <span class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">MCP tool</span>
            <input
              v-model="editingHook.mcpTool"
              class="text-sm px-2 py-1 rounded-sm border border-border bg-surface-1 text-ink"
              data-testid="hook-form-mcp-tool"
            />
          </label>

          <fieldset class="rounded-md border border-border-muted px-3 py-2">
            <legend class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle">Match filters (empty = any)</legend>
            <label class="block mt-2">
              <span class="font-ui text-[11px] text-ink-muted">Session IDs (comma-separated)</span>
              <input
                :value="joinList(editingHook.match.sessionIds)"
                class="mt-1 w-full text-xs font-mono px-2 py-1 rounded-sm border border-border bg-surface-1 text-ink"
                data-testid="hook-form-match-sessions"
                @input="(e) => editingHook!.match.sessionIds = splitList((e.target as HTMLInputElement).value)"
              />
            </label>
            <label class="block mt-2">
              <span class="font-ui text-[11px] text-ink-muted">Kinds (comma-separated)</span>
              <input
                :value="joinList(editingHook.match.kinds)"
                class="mt-1 w-full text-xs font-mono px-2 py-1 rounded-sm border border-border bg-surface-1 text-ink"
                data-testid="hook-form-match-kinds"
                @input="(e) => editingHook!.match.kinds = splitList((e.target as HTMLInputElement).value)"
              />
            </label>
            <label class="block mt-2">
              <span class="font-ui text-[11px] text-ink-muted">Models (comma-separated)</span>
              <input
                :value="joinList(editingHook.match.models)"
                class="mt-1 w-full text-xs font-mono px-2 py-1 rounded-sm border border-border bg-surface-1 text-ink"
                data-testid="hook-form-match-models"
                @input="(e) => editingHook!.match.models = splitList((e.target as HTMLInputElement).value)"
              />
            </label>
          </fieldset>

          <label class="flex items-center gap-2">
            <input
              type="checkbox"
              :checked="editingHook.enabled"
              data-testid="hook-form-enabled"
              @change="(e) => editingHook!.enabled = (e.target as HTMLInputElement).checked"
            />
            <span class="font-ui text-sm text-ink">Enabled</span>
          </label>

          <div class="flex justify-end gap-2 pt-2">
            <Button variant="ghost" @click="closeDrawer">Cancel</Button>
            <Button variant="accent" data-testid="hook-form-save" @click="saveHook">Save</Button>
          </div>
        </div>
      </div>
    </div>
  </SettingsShell>
</template>
