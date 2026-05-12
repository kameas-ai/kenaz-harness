<script setup lang="ts">
/**
 * TodoSidePanel — slide-in panel that renders the session todo list
 * maintained by kaneaz__todo_write calls.
 *
 * Mounted by the parent (ChatView / MessageBubble host) when the user
 * clicks a TodoChip. The panel is purely presentational — it receives
 * the latest todos list as a prop and does not call any RPC.
 *
 * Layout: fixed right-side overlay with a translucent backdrop.
 * Pressing Escape or clicking the backdrop closes the panel.
 *
 * Status colours follow the design-token convention (no raw literals).
 * Priority indicators use text weight rather than colour so the panel
 * remains accessible at every contrast level.
 *
 * (builtin-tools-search-and-elicitation-01KZNP3D WP06)
 */

import { computed, onBeforeUnmount, onMounted } from 'vue';
import { X, CheckSquare, Circle } from '@/shell/icons';
import type { TodoItem } from './TodoChip.vue';

const props = defineProps<{
  /** Whether the panel is visible. */
  open: boolean;
  /** The current session todo list. */
  todos: TodoItem[];
}>();

const emit = defineEmits<{
  (e: 'close'): void;
}>();

// ── Keyboard handling ────────────────────────────────────────────────────

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && props.open) {
    emit('close');
  }
}

onMounted(() => window.addEventListener('keydown', onKeydown));
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown));

// ── Derived state ────────────────────────────────────────────────────────

const groups = computed(() => {
  const pending = props.todos.filter((t) => t.status === 'pending');
  const inProgress = props.todos.filter((t) => t.status === 'in_progress');
  const done = props.todos.filter((t) => t.status === 'done');
  const cancelled = props.todos.filter((t) => t.status === 'cancelled');
  return { pending, inProgress, done, cancelled };
});

const isEmpty = computed(() => props.todos.length === 0);

function statusLabel(status: TodoItem['status']): string {
  switch (status) {
    case 'pending':
      return 'Pending';
    case 'in_progress':
      return 'In progress';
    case 'done':
      return 'Done';
    case 'cancelled':
      return 'Cancelled';
    default:
      return status;
  }
}

function priorityClass(priority: TodoItem['priority']): string {
  switch (priority) {
    case 'high':
      return 'font-semibold text-signal-warn';
    case 'low':
      return 'text-ink-dim';
    default:
      return '';
  }
}

function priorityBadge(priority: TodoItem['priority']): string | null {
  if (priority === 'high') return 'HIGH';
  if (priority === 'low') return 'LOW';
  return null;
}
</script>

<template>
  <Teleport to="body">
    <!-- Backdrop -->
    <Transition name="fade">
      <div
        v-if="open"
        class="fixed inset-0 z-40 bg-black/30"
        data-testid="todo-side-panel-backdrop"
        aria-hidden="true"
        @click="$emit('close')"
      />
    </Transition>

    <!-- Panel -->
    <Transition name="slide-right">
      <aside
        v-if="open"
        class="fixed right-0 top-0 z-50 flex h-full w-80 flex-col border-l border-border-muted bg-surface-0 shadow-xl"
        role="complementary"
        aria-label="Task list"
        data-testid="todo-side-panel"
      >
        <!-- Header -->
        <div
          class="flex items-center justify-between border-b border-border-muted px-4 py-3"
        >
          <div class="flex items-center gap-2 font-ui text-[13px] text-ink">
            <CheckSquare class="h-4 w-4 text-accent" aria-hidden="true" />
            <span>Task list</span>
            <span
              v-if="todos.length > 0"
              class="rounded-sm bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-ink-dim"
            >
              {{ todos.length }}
            </span>
          </div>
          <button
            type="button"
            class="rounded-sm p-1 text-ink-dim hover:bg-surface-2 hover:text-ink"
            aria-label="Close task list"
            data-testid="todo-side-panel-close"
            @click="$emit('close')"
          >
            <X class="h-4 w-4" />
          </button>
        </div>

        <!-- Body -->
        <div class="flex-1 overflow-y-auto p-4">
          <!-- Empty state -->
          <div
            v-if="isEmpty"
            class="flex flex-col items-center justify-center py-12 text-center"
            data-testid="todo-empty"
          >
            <CheckSquare class="mb-3 h-8 w-8 text-ink-dim" aria-hidden="true" />
            <p class="font-ui text-[12px] text-ink-muted">No tasks yet.</p>
            <p class="mt-1 font-ui text-[11px] text-ink-dim">
              The model will add tasks here via kaneaz__todo_write.
            </p>
          </div>

          <!-- In progress -->
          <section
            v-if="groups.inProgress.length > 0"
            class="mb-4"
            data-testid="todo-group-in-progress"
          >
            <h3
              class="mb-2 font-ui text-[10px] uppercase tracking-[0.16em] text-ink-subtle"
            >
              {{ statusLabel('in_progress') }}
            </h3>
            <ul class="space-y-1">
              <li
                v-for="item in groups.inProgress"
                :key="item.id"
                class="flex items-start gap-2 rounded-sm border border-border-muted bg-surface-1 px-3 py-2"
                :data-testid="`todo-item-${item.id}`"
              >
                <Circle
                  class="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-signal-warn"
                  aria-hidden="true"
                />
                <span
                  class="flex-1 font-ui text-[12px]"
                  :class="priorityClass(item.priority)"
                >
                  {{ item.content }}
                </span>
                <span
                  v-if="priorityBadge(item.priority)"
                  class="rounded-sm px-1 font-ui text-[9px] uppercase tracking-[0.14em]"
                  :class="
                    item.priority === 'high'
                      ? 'bg-signal-warn/20 text-signal-warn'
                      : 'bg-surface-2 text-ink-dim'
                  "
                >
                  {{ priorityBadge(item.priority) }}
                </span>
              </li>
            </ul>
          </section>

          <!-- Pending -->
          <section
            v-if="groups.pending.length > 0"
            class="mb-4"
            data-testid="todo-group-pending"
          >
            <h3
              class="mb-2 font-ui text-[10px] uppercase tracking-[0.16em] text-ink-subtle"
            >
              {{ statusLabel('pending') }}
            </h3>
            <ul class="space-y-1">
              <li
                v-for="item in groups.pending"
                :key="item.id"
                class="flex items-start gap-2 rounded-sm border border-border-muted bg-surface-1 px-3 py-2"
                :data-testid="`todo-item-${item.id}`"
              >
                <Circle
                  class="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-ink-dim"
                  aria-hidden="true"
                />
                <span
                  class="flex-1 font-ui text-[12px] text-ink"
                  :class="priorityClass(item.priority)"
                >
                  {{ item.content }}
                </span>
                <span
                  v-if="priorityBadge(item.priority)"
                  class="rounded-sm px-1 font-ui text-[9px] uppercase tracking-[0.14em]"
                  :class="
                    item.priority === 'high'
                      ? 'bg-signal-warn/20 text-signal-warn'
                      : 'bg-surface-2 text-ink-dim'
                  "
                >
                  {{ priorityBadge(item.priority) }}
                </span>
              </li>
            </ul>
          </section>

          <!-- Done -->
          <section
            v-if="groups.done.length > 0"
            class="mb-4"
            data-testid="todo-group-done"
          >
            <h3
              class="mb-2 font-ui text-[10px] uppercase tracking-[0.16em] text-ink-subtle"
            >
              {{ statusLabel('done') }}
            </h3>
            <ul class="space-y-1">
              <li
                v-for="item in groups.done"
                :key="item.id"
                class="flex items-start gap-2 rounded-sm border border-border-muted bg-surface-1 px-3 py-2 opacity-60"
                :data-testid="`todo-item-${item.id}`"
              >
                <CheckSquare
                  class="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-signal-ok"
                  aria-hidden="true"
                />
                <span class="flex-1 font-ui text-[12px] text-ink-muted line-through">
                  {{ item.content }}
                </span>
              </li>
            </ul>
          </section>

          <!-- Cancelled -->
          <section
            v-if="groups.cancelled.length > 0"
            data-testid="todo-group-cancelled"
          >
            <h3
              class="mb-2 font-ui text-[10px] uppercase tracking-[0.16em] text-ink-subtle"
            >
              {{ statusLabel('cancelled') }}
            </h3>
            <ul class="space-y-1">
              <li
                v-for="item in groups.cancelled"
                :key="item.id"
                class="flex items-start gap-2 rounded-sm border border-border-muted bg-surface-1 px-3 py-2 opacity-40"
                :data-testid="`todo-item-${item.id}`"
              >
                <X
                  class="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-ink-dim"
                  aria-hidden="true"
                />
                <span class="flex-1 font-ui text-[12px] text-ink-dim line-through">
                  {{ item.content }}
                </span>
              </li>
            </ul>
          </section>
        </div>
      </aside>
    </Transition>
  </Teleport>
</template>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 150ms ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}

.slide-right-enter-active,
.slide-right-leave-active {
  transition: transform 200ms ease;
}
.slide-right-enter-from,
.slide-right-leave-to {
  transform: translateX(100%);
}
</style>
