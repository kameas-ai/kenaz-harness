<script setup lang="ts">
/**
 * HooksPanel — list of configured lifecycle hooks with per-row enable /
 * disable toggle, edit, and delete actions.
 *
 * Empty state offers an "Install starter memory hooks" CTA.
 *
 * hooks-event-surface-expansion-01KZNP3A WP07c.
 */
import { computed } from 'vue';
import Button from '@/components/ui/Button.vue';
import type { Hook, BuiltinDescriptor } from '@/lib/types';

const props = defineProps<{
  hooks: readonly Hook[];
  builtins: readonly BuiltinDescriptor[];
  loading: boolean;
  error: string | null;
  flagEnabled: boolean;
}>();

const emit = defineEmits<{
  (e: 'add'): void;
  (e: 'edit', hook: Hook): void;
  (e: 'remove', id: string): void;
  (e: 'toggle', hook: Hook): void;
  (e: 'installStarter'): void;
}>();

const hasStarterHooks = computed(() =>
  props.hooks.some(
    (h) => h.id === 'starter:memory.retrieve' || h.id === 'starter:memory.persist',
  ),
);

function matchSummary(h: Hook): string {
  const parts: string[] = [];
  if (h.match.sessionIds?.length) parts.push(`${h.match.sessionIds.length} session(s)`);
  if (h.match.kinds?.length) parts.push(h.match.kinds.join(','));
  if (h.match.models?.length) parts.push(h.match.models.join(','));
  return parts.length === 0 ? 'any' : parts.join(' · ');
}

function handleRemove(id: string): void {
  if (!window.confirm(`Remove hook ${id}?`)) return;
  emit('remove', id);
}
</script>

<template>
  <div data-testid="hooks-panel">
    <!-- Flag disabled -->
    <div
      v-if="!flagEnabled"
      class="rounded-md border border-border-muted bg-surface-1 px-4 py-6 text-center"
      data-testid="hooks-flag-disabled"
    >
      <p class="font-ui text-sm text-ink-muted">
        Hooks v2 feature flag is disabled. Enable
        <code class="font-mono text-xs">hooks_v2_enabled</code> to manage lifecycle hooks.
      </p>
    </div>

    <template v-else>
      <!-- Error banner -->
      <div
        v-if="error"
        class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
        data-testid="hooks-panel-error"
      >
        {{ error }}
      </div>

      <div class="flex items-center justify-between mb-3">
        <p class="font-ui text-[12px] text-ink-muted">
          Configurable side-effects on chat-pipeline and lifecycle events.
        </p>
        <Button
          variant="accent"
          data-testid="hooks-add-btn"
          @click="emit('add')"
        >
          Add hook
        </Button>
      </div>

      <!-- Loading -->
      <div
        v-if="loading"
        class="font-ui text-[12px] text-ink-muted"
        role="status"
        data-testid="hooks-loading"
      >
        Loading hooks…
      </div>

      <!-- Empty state -->
      <div
        v-else-if="hooks.length === 0"
        class="rounded-md border border-border-muted bg-surface-1 px-4 py-6 text-center"
        data-testid="hooks-empty"
      >
        <p class="font-ui text-sm text-ink">
          No hooks configured yet.
        </p>
        <p class="font-ui text-[12px] text-ink-muted mt-1 mb-4">
          Install the starter memory hooks to get long-term memory across sessions,
          or add a custom shell or builtin hook.
        </p>
        <div class="flex gap-2 justify-center">
          <Button
            variant="accent"
            data-testid="hooks-install-starter-btn"
            @click="emit('installStarter')"
          >
            Install starter memory hooks
          </Button>
          <Button
            variant="ghost"
            data-testid="hooks-add-first-btn"
            @click="emit('add')"
          >
            Add custom hook
          </Button>
        </div>
      </div>

      <!-- Hook list -->
      <div v-else>
        <!-- Starter hooks CTA when some exist but not the starter pair -->
        <div v-if="!hasStarterHooks" class="mb-3">
          <Button
            variant="ghost"
            data-testid="hooks-install-starter-inline"
            @click="emit('installStarter')"
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
              @click="emit('edit', h)"
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
              <td class="px-3 py-2" @click.stop>
                <input
                  type="checkbox"
                  :checked="h.enabled"
                  :data-testid="`hook-toggle-${h.id}`"
                  @change="emit('toggle', h)"
                />
              </td>
              <td class="px-3 py-2 text-right flex gap-1 items-center justify-end" @click.stop>
                <button
                  type="button"
                  class="px-2 py-1 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:text-ink hover:bg-surface-3"
                  :data-testid="`hook-edit-${h.id}`"
                  @click="emit('edit', h)"
                >
                  Edit
                </button>
                <button
                  type="button"
                  class="px-2 py-1 rounded-sm border border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:text-signal-danger hover:bg-surface-3"
                  :data-testid="`hook-remove-${h.id}`"
                  @click="handleRemove(h.id)"
                >
                  Remove
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </div>
</template>
