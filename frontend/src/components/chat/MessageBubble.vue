<script setup lang="ts">
/**
 * MessageBubble — single-message render primitive.
 *
 * Visual register (FR-001b, plan §5.2):
 *   - User bubble  : right-aligned, surface-2 background, sans body.
 *   - Assistant    : left-aligned, surface-1 background, sans body. While
 *                    streaming a brass hairline glows along the leading
 *                    edge — brass strictly reserved for live state per
 *                    FR-011 / privacy CI invariant #4.
 *   - System       : centred, italic, ink-muted.
 *   - Tool         : monospace EventStreamRow-style line — namespaced.
 *
 * Markdown rendering is the deliberately-narrow inline subset (no HTML,
 * no images): the textual content flows through a `whitespace-pre-wrap`
 * block via StreamingText. Future missions extend with a vetted markdown
 * pipeline once the policy mission ratifies a sanitiser.
 *
 * Pin button (WP06 T005): a 📌 button near the role label opens a
 * three-option menu (session / project / global). Triggers:
 *   - Left click on 📌 → menu.
 *   - Right click on the bubble body → menu.
 *   - Long-press (≥ 350 ms pointerdown) on 📌 → menu.
 * Pressing Enter on the freshly-opened menu pins to session — the
 * legacy default — so muscle memory keeps working.
 */

import { computed, ref } from 'vue';
import StreamingText from './StreamingText.vue';
import PinMenu from './PinMenu.vue';
import type { MemoryScopeKind, MessageRole, ToolCall } from '@/lib/types';

const props = defineProps<{
  role: MessageRole;
  content: string;
  streaming?: boolean;
  toolCalls?: readonly ToolCall[];
  /**
   * When true, renders a 📌 button on hover that opens the scope menu.
   * The parent toggles this off when long-term memory is disabled in
   * settings so the button never appears unsolicited.
   */
  rememberable?: boolean;
  /**
   * Project id of the session this message belongs to. Empty / undefined
   * means the session is loose; the "Pin to project" row in the menu is
   * disabled with a tooltip.
   */
  projectId?: string;
}>();

const emit = defineEmits<{
  /**
   * User picked a scope from the pin menu — caller persists the chunk
   * via RPC. Defaults to `'session'` when invoked from legacy paths.
   */
  (e: 'remember', scope: MemoryScopeKind): void;
}>();

const menuOpen = ref(false);
const flashConfirm = ref(false);
const longPressTimer = ref<ReturnType<typeof setTimeout> | null>(null);

const LONG_PRESS_MS = 350;

function openMenu() {
  menuOpen.value = true;
}

function closeMenu() {
  menuOpen.value = false;
}

function onPinClick(event: Event) {
  event.stopPropagation();
  // Cancel any pending long-press so we don't double-fire when the user
  // taps and releases inside the long-press window.
  if (longPressTimer.value !== null) {
    clearTimeout(longPressTimer.value);
    longPressTimer.value = null;
  }
  if (menuOpen.value) {
    closeMenu();
  } else {
    openMenu();
  }
}

function onPinPointerDown(event: PointerEvent) {
  // Long-press → open menu (matches click behaviour but explicit, so
  // touch / pen surfaces still work). The click handler also opens the
  // menu, so on a quick tap we just no-op the timer.
  if (longPressTimer.value !== null) {
    clearTimeout(longPressTimer.value);
  }
  const button = event.currentTarget;
  longPressTimer.value = setTimeout(() => {
    longPressTimer.value = null;
    openMenu();
  }, LONG_PRESS_MS);
  // Cancel on pointerup / leave so a quick click does not also fire
  // the long-press path.
  const cancel = () => {
    if (longPressTimer.value !== null) {
      clearTimeout(longPressTimer.value);
      longPressTimer.value = null;
    }
    if (button instanceof HTMLElement) {
      button.removeEventListener('pointerup', cancel);
      button.removeEventListener('pointerleave', cancel);
      button.removeEventListener('pointercancel', cancel);
    }
  };
  if (button instanceof HTMLElement) {
    button.addEventListener('pointerup', cancel);
    button.addEventListener('pointerleave', cancel);
    button.addEventListener('pointercancel', cancel);
  }
}

function onBodyContextMenu(event: MouseEvent) {
  if (!props.rememberable) return;
  event.preventDefault();
  openMenu();
}

function onPick(scope: MemoryScopeKind) {
  closeMenu();
  emit('remember', scope);
  flashConfirm.value = true;
  setTimeout(() => {
    flashConfirm.value = false;
  }, 1200);
}

const hasProject = computed(() => (props.projectId ?? '').length > 0);

const isUser = computed(() => props.role === 'user');
const isAssistant = computed(() => props.role === 'assistant');
const isSystem = computed(() => props.role === 'system');
const isTool = computed(() => props.role === 'tool');

const wrapperClass = computed(() => {
  if (isUser.value) return 'flex justify-end';
  if (isSystem.value) return 'flex justify-center';
  if (isTool.value) return 'flex justify-start';
  return 'flex justify-start';
});

const bubbleClass = computed(() => {
  const base = 'max-w-[78ch] px-4 py-3 rounded-md font-ui text-sm leading-relaxed';
  if (isUser.value) {
    return `${base} bg-surface-3 text-ink border border-border-muted`;
  }
  if (isAssistant.value) {
    const live = props.streaming
      ? 'border-accent shadow-[0_0_0_1px_var(--accent-glow)]'
      : 'border-border-muted';
    return `${base} bg-surface-1 text-ink border ${live}`;
  }
  if (isSystem.value) {
    return `${base} bg-transparent text-ink-muted italic`;
  }
  // tool — monospace EventStreamRow-style
  return 'font-mono text-[12px] text-ink-muted px-3 py-1';
});

const roleLabel = computed(() => props.role.toUpperCase());
</script>

<template>
  <article
    :class="wrapperClass + ' group relative'"
    :role="isTool ? 'log' : 'article'"
    :aria-label="`${roleLabel} message`"
    @contextmenu="onBodyContextMenu"
  >
    <div :class="bubbleClass">
      <header
        v-if="!isTool"
        class="font-ui text-[10px] uppercase tracking-[0.18em] mb-1 flex items-center"
        :class="isAssistant && streaming ? 'text-accent' : 'text-ink-subtle'"
      >
        <span>{{ roleLabel }}</span>
        <span v-if="isAssistant && streaming" class="ml-2">live</span>
        <span
          v-if="flashConfirm"
          class="ml-auto mr-2 text-accent"
          aria-live="polite"
          data-testid="remember-confirm"
        >
          pinned
        </span>
        <div v-if="rememberable" :class="['relative', flashConfirm ? '' : 'ml-auto']">
          <button
            type="button"
            class="opacity-0 group-hover:opacity-100 focus:opacity-100 transition-fast text-[12px] px-1.5 py-0.5 rounded-sm border border-border-muted text-ink-dim hover:text-accent hover:bg-surface-2"
            :aria-label="'Remember this message'"
            :aria-haspopup="'menu'"
            :aria-expanded="menuOpen"
            data-testid="remember-message"
            @click="onPinClick"
            @pointerdown="onPinPointerDown"
          >
            📌
          </button>
          <PinMenu
            :open="menuOpen"
            :has-project="hasProject"
            @pick="onPick"
            @close="closeMenu"
          />
        </div>
      </header>

      <!-- tool-call rows: namespaced monospace line each -->
      <ul v-if="toolCalls && toolCalls.length > 0" class="space-y-1 mb-2">
        <li
          v-for="tc in toolCalls"
          :key="tc.id"
          class="font-mono text-[12px] grid items-baseline gap-3"
          style="grid-template-columns: 14ch 1fr auto"
        >
          <span class="uppercase tracking-[0.12em] text-[11px] text-accent">
            tool · {{ tc.name }}
          </span>
          <span class="text-ink truncate">{{ tc.argsSummary }}</span>
          <span v-if="tc.latency" class="text-ink-subtle text-right">
            {{ tc.latency }}
          </span>
        </li>
      </ul>

      <StreamingText
        v-if="!isTool"
        :text="content"
        :streaming="streaming === true"
      />
      <span v-else class="font-mono text-[12px] text-ink-muted">
        {{ content }}
      </span>
    </div>
  </article>
</template>
