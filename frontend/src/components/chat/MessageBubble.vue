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
import { Archive, Layers } from 'lucide-vue-next';
import MarkdownBlock from './MarkdownBlock.vue';
import PinMenu from './PinMenu.vue';
import ImageBlock from './ImageBlock.vue';
import DocumentChip from './DocumentChip.vue';
import ArtifactChip from './ArtifactChip.vue';
import TokenMeterChip from './TokenMeterChip.vue';
import type {
  Artifact,
  ContentBlock,
  MemoryScopeKind,
  MessageRole,
  ToolCall,
} from '@/lib/types';

const props = defineProps<{
  role: MessageRole;
  content: string;
  streaming?: boolean;
  /**
   * Stream-failure marker (long-turn-resilience WP00). When set, the
   * bubble renders a muted "Connection lost — message may be
   * incomplete." sub-line beneath the body. No Resume button yet —
   * that lands in WP03.
   */
  streamingError?: string;
  toolCalls?: readonly ToolCall[];
  /**
   * Polymorphic content blocks for multimodal messages
   * (multimodal-io WP02/WP04). When non-empty, MessageBubble renders
   * each block via the matching component (StreamingText / ImageBlock
   * / DocumentChip). Empty / undefined falls back to the legacy
   * `content` text path so pre-WP02 messages keep rendering.
   */
  contentBlocks?: readonly ContentBlock[];
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
  /**
   * Stable id for this message. Required for the artifact-save and
   * per-message artifact-chip flows. Optional for legacy callers — when
   * absent the right-click "Save as artifact" affordance is suppressed
   * and the chip row never renders.
   */
  messageId?: string;
  /**
   * When true, MessageBubble exposes a "Save as artifact" affordance
   * (right-click context menu when `rememberable` is false; explicit
   * button otherwise). The parent owns the rpc call via the
   * `save-artifact` event.
   */
  saveable?: boolean;
  /**
   * Artifacts already captured from THIS message (filtered upstream
   * by the session-level useArtifacts composable so we don't fetch
   * per-bubble). Empty / undefined renders no chip row.
   */
  artifacts?: readonly Artifact[];
  /**
   * Compaction bookkeeping (compaction-strategy-ui WP07). When the
   * row is the synthetic summary, summaryFoldedCount is the number of
   * archived rows that fold into it (computed by the parent from
   * `messages.filter(m => m.compactedIntoId === this.messageId).length`).
   * When the row is an archived original, archivedFromSummaryId points
   * back at the summary that absorbed it so the chip can scroll-to.
   */
  isSummary?: boolean;
  summaryFoldedCount?: number;
  isArchived?: boolean;
  archivedFromSummaryId?: string;
  /**
   * Long-turn-resilience-01KR3PRS WP03 — partial-output bubble state.
   * When streamingFailedAt is non-empty, the bubble carries a
   * "Connection lost" sub-line; when streamingRecoverable is also true
   * a "Resume" button surfaces and emits `resume`. The parent wires
   * the resume click to client.sessions.resumeMessage.
   *
   * streamingFailureKind is a hint the parent can use to tailor copy
   * ("transient" → "Network blip"; "auth" → "Re-authenticate";
   * everything else falls back to the generic "Connection lost").
   */
  streamingFailedAt?: string;
  streamingFailureKind?: string;
  streamingRecoverable?: boolean;
  /**
   * Frontend-only marker: set by useSession's stream-closed handler
   * when the close payload carried reason=backend-error AND the
   * session_messages row hasn't been persisted server-side yet (WP00
   * surface). Used to render the partial-output footer even before the
   * server-side row materializes via ListMessages refresh.
   */
  streamingError?: string;

  // ── per-message-token-meter-01KR3PQR ────────────────────────────────

  /**
   * When true, shows the TokenMeterChip next to the role label on
   * completed assistant bubbles. Driven by the global
   * settings.showPerMessageTokenMeter toggle; the parent passes the
   * flag so the bubble doesn't need to subscribe to settings itself.
   * No chip is ever shown while streaming — wait for the turn to land.
   */
  showTokenMeter?: boolean;
  /**
   * Per-message prompt token count (from session_messages.prompt_tokens).
   * Absent on rows that pre-date the telemetry migration or on non-assistant turns.
   */
  promptTokens?: number;
  /** Per-message completion token count (from session_messages.completion_tokens). */
  completionTokens?: number;
  /** Per-message cost in USD (from session_messages.cost_usd). */
  costUsd?: number;
  /** Cost source: "provider" | "derived" | "mixed" | "unknown". */
  messageCostSource?: string;
}>();

const emit = defineEmits<{
  /**
   * User picked a scope from the pin menu — caller persists the chunk
   * via RPC. Defaults to `'session'` when invoked from legacy paths.
   */
  (e: 'remember', scope: MemoryScopeKind): void;
  /**
   * User asked to save the message (or a slice of it) as an artifact.
   * The parent prompts for a title and calls
   * `client.sessions.saveAsArtifact`.
   */
  (e: 'save-artifact', messageId: string): void;
  /**
   * User clicked an artifact chip — parent owns the preview modal
   * lifecycle (it fetches bytes via `client.artifacts.get`).
   */
  (e: 'open-artifact', artifact: Artifact): void;
  /**
   * User clicked the "→ summary" chip on an archived row. The parent
   * scrolls / jumps to the summary in the scrollback
   * (compaction-strategy-ui WP07).
   */
  (e: 'jump-to-summary', summaryId: string): void;
  /**
   * User clicked the "Resume" button on a partial assistant bubble
   * (long-turn-resilience-01KR3PRS WP03). The parent wires the click
   * to client.sessions.resumeMessage(sessionId, messageId).
   */
  (e: 'resume', messageId: string): void;
  /**
   * User clicked "Branch from this turn" (branching-ux-polish-01KQ8TD7 WP05).
   * The parent calls client.branches.createExplicit and navigates to the child.
   */
  (e: 'branch-from-turn', messageId: string): void;
}>();

// Feature flag (branching-ux-polish-01KQ8TD7 WP05) — consistent with LeftRail.
const BRANCHING_POLISH_ENABLED: boolean = (() => {
  try {
    const v = localStorage.getItem('harness.feature.branchingPolish');
    if (v === 'off' || v === 'false') return false;
  } catch { /* ignore */ }
  return true;
})();

function onBranchFromTurn() {
  if (!props.messageId) return;
  emit('branch-from-turn', props.messageId);
}

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
  if (props.rememberable) {
    event.preventDefault();
    openMenu();
    return;
  }
  if (props.saveable && props.messageId) {
    event.preventDefault();
    saveArtifactMenuOpen.value = true;
  }
}

const saveArtifactMenuOpen = ref(false);

function closeSaveArtifactMenu() {
  saveArtifactMenuOpen.value = false;
}

function onSaveArtifact() {
  closeSaveArtifactMenu();
  if (!props.messageId) return;
  emit('save-artifact', props.messageId);
}

const copyFlash = ref(false);

async function onCopyMessage() {
  const text = props.content ?? '';
  let ok = false;
  try {
    await navigator.clipboard.writeText(text);
    ok = true;
  } catch {
    // Wails fallback — same path as the per-code-block copy in
    // MarkdownBlock. Wails' webview rejects navigator.clipboard in
    // some contexts; the runtime binding always works.
    try {
      const setter = window.runtime?.ClipboardSetText;
      if (setter) {
        ok = await setter(text);
      }
    } catch {
      ok = false;
    }
  }
  if (ok) {
    copyFlash.value = true;
    setTimeout(() => {
      copyFlash.value = false;
    }, 1200);
  }
}

function onArtifactChipOpen(artifact: Artifact) {
  emit('open-artifact', artifact);
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
  // Archived rows render at half-strength with a left-border tag —
  // the surface still shows them in full when "Show full history" is
  // on (compaction-strategy-ui WP07 plan §2.8). The opacity tweak
  // pulls from the global ink ramp rather than hard-coding a color.
  const archived = props.isArchived
    ? ' opacity-60 border-l-2 border-l-accent-hairline'
    : '';
  if (isUser.value) {
    return `${base} bg-surface-3 text-ink border border-border-muted${archived}`;
  }
  if (isAssistant.value) {
    const live = props.streaming
      ? 'border-accent shadow-[0_0_0_1px_var(--accent-glow)]'
      : 'border-border-muted';
    return `${base} bg-surface-1 text-ink border ${live}${archived}`;
  }
  if (isSystem.value) {
    return `${base} bg-transparent text-ink-muted italic${archived}`;
  }
  // tool — monospace EventStreamRow-style
  return `font-mono text-[12px] text-ink-muted px-3 py-1${archived}`;
});

function onJumpToSummary() {
  if (!props.archivedFromSummaryId) return;
  emit('jump-to-summary', props.archivedFromSummaryId);
}

const roleLabel = computed(() => props.role.toUpperCase());

const blocks = computed<readonly ContentBlock[]>(() => props.contentBlocks ?? []);
const hasBlocks = computed(() => blocks.value.length > 0);

function isLastBlock(idx: number): boolean {
  return idx === blocks.value.length - 1;
}

// Long-turn-resilience-01KR3PRS WP03 — partial-output bubble logic.
//
// isPartialOutputBubble: true on assistant rows whose stream dropped
// before the kernel could land a clean assistant turn. The flag rides
// streamingFailedAt OR the frontend-only streamingError marker (set by
// useSession's stream-closed handler when the close payload carried
// reason=backend-error). Both surfaces converge so the WP00 fallback
// keeps working on rows that haven't been persisted server-side yet.
const isPartialOutputBubble = computed(() => {
  if (!isAssistant.value) return false;
  return Boolean(props.streamingFailedAt) || Boolean(props.streamingError);
});

// partialOutputCopy: tailored failure copy by classification kind.
// Recoverable rows get the "preserved" framing; non-recoverable rows
// (tool_use already ran) get the "re-issue your request" framing.
const partialOutputCopy = computed(() => {
  if (!props.streamingRecoverable) {
    return 'Connection lost mid-tool-call — please re-send your request.';
  }
  switch (props.streamingFailureKind) {
    case 'auth':
      return 'Connection lost — partial reply preserved (auth issue).';
    case 'transient':
      return 'Connection lost — partial reply preserved.';
    default:
      return 'Connection lost — partial reply preserved.';
  }
});

function onResumeClick() {
  if (!props.messageId) return;
  emit('resume', props.messageId);
}
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
        <!-- Token-meter chip (per-message-token-meter-01KR3PQR).
             Show only on completed (non-streaming) assistant turns when the
             toggle is on and the row carries telemetry data. -->
        <TokenMeterChip
          v-if="isAssistant && !streaming && showTokenMeter"
          :prompt-tokens="promptTokens"
          :completion-tokens="completionTokens"
          :cost-usd="costUsd"
          :cost-source="messageCostSource"
          class="ml-2 normal-case tracking-normal"
        />
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
        <button
          type="button"
          class="opacity-0 group-hover:opacity-100 focus:opacity-100 transition-fast text-[12px] px-1.5 py-0.5 rounded-sm border border-border-muted text-ink-dim hover:text-accent hover:bg-surface-2 ml-1"
          :class="rememberable || (saveable && messageId) ? '' : 'ml-auto'"
          aria-label="Copy message text"
          :data-testid="'copy-message-button'"
          @click="onCopyMessage"
        >
          {{ copyFlash ? 'copied' : 'copy' }}
        </button>
        <div
          v-if="saveable && messageId"
          :class="['relative', rememberable ? '' : flashConfirm ? '' : '']"
        >
          <button
            type="button"
            class="opacity-0 group-hover:opacity-100 focus:opacity-100 transition-fast text-[12px] px-1.5 py-0.5 rounded-sm border border-border-muted text-ink-dim hover:text-accent hover:bg-surface-2 ml-1"
            aria-label="Save as artifact"
            data-testid="save-artifact-button"
            @click="onSaveArtifact"
          >
            ⎘
          </button>
          <div
            v-if="saveArtifactMenuOpen"
            class="absolute right-0 top-full z-30 mt-1 w-52 rounded-sm border border-border-muted bg-surface-1 shadow-lg"
            role="menu"
            data-testid="save-artifact-menu"
          >
            <button
              type="button"
              role="menuitem"
              class="block w-full px-3 py-1.5 text-left font-ui text-[12px] text-ink hover:bg-surface-2"
              data-testid="save-artifact-menu-save"
              @click="onSaveArtifact"
            >
              Save as artifact
            </button>
          </div>
        </div>
        <!-- "Branch from this turn" button (branching-ux-polish-01KQ8TD7 WP05).
             Visible on assistant messages with a stable messageId when
             HARNESS_BRANCHING_POLISH is on. Disabled while streaming. -->
        <button
          v-if="BRANCHING_POLISH_ENABLED && isAssistant && messageId"
          type="button"
          class="opacity-0 group-hover:opacity-100 focus:opacity-100 transition-fast text-[12px] px-1.5 py-0.5 rounded-sm border border-border-muted text-ink-dim hover:text-accent hover:bg-surface-2 ml-1 disabled:opacity-40 disabled:cursor-not-allowed"
          :disabled="!!streaming"
          :title="streaming ? 'Wait for the response to finish' : 'Branch from this turn'"
          :aria-label="'Branch from this turn'"
          data-testid="branch-from-turn-button"
          @click="onBranchFromTurn"
        >
          ⎇
        </button>
      </header>

      <!-- Compaction summary indicator (WP07): rendered on the
           synthetic summary row regardless of toggle state. -->
      <div
        v-if="isSummary"
        class="mb-2 flex items-center gap-1.5 font-ui text-[11px] text-ink-muted"
        data-testid="message-summary-indicator"
      >
        <Layers class="h-3.5 w-3.5 text-accent" aria-hidden="true" />
        <span>
          Summary of {{ summaryFoldedCount ?? 0 }}
          {{ (summaryFoldedCount ?? 0) === 1 ? 'turn' : 'turns' }}
        </span>
      </div>

      <!-- Archived row tag + jump-to-summary chip (WP07). -->
      <div
        v-if="isArchived"
        class="mb-2 flex items-center gap-1.5 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
        data-testid="message-archived-tag"
      >
        <Archive class="h-3 w-3" aria-hidden="true" />
        <span>archived</span>
        <button
          v-if="archivedFromSummaryId"
          type="button"
          class="ml-2 normal-case tracking-normal text-[11px] text-accent hover:underline"
          data-testid="message-archived-jump"
          :data-summary-id="archivedFromSummaryId"
          @click="onJumpToSummary"
        >
          → summary
        </button>
      </div>

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

      <template v-if="!isTool">
        <template v-if="hasBlocks">
          <template v-for="(block, i) in blocks" :key="i">
            <MarkdownBlock
              v-if="block.type === 'text'"
              :source="block.text ?? ''"
              :streaming="streaming === true && isLastBlock(i)"
              :session-id="undefined"
              :message-id="messageId"
            />
            <ImageBlock
              v-else-if="block.type === 'image' && block.source"
              :source="block.source"
            />
            <DocumentChip
              v-else-if="block.type === 'document' && block.source"
              :source="block.source"
            />
          </template>
        </template>
        <MarkdownBlock
          v-else
          :source="content"
          :streaming="streaming === true"
          :session-id="undefined"
          :message-id="messageId"
        />
      </template>
      <span v-else class="font-mono text-[12px] text-ink-muted">
        {{ content }}
      </span>

      <!-- Stream-failure sub-line (long-turn-resilience WP00). Surfaced
           when the SSE stream closed with a non-completed reason and
           the partial bubble was committed anyway. WP03 will replace
           this with a Resume button. -->
      <div
        v-if="streamingError"
        class="mt-2 font-ui text-[11px] text-ink-muted italic"
        data-testid="message-streaming-error"
      >
        Connection lost — message may be incomplete.
      </div>

      <div
        v-if="artifacts && artifacts.length > 0"
        class="mt-2 flex flex-wrap gap-1.5"
        data-testid="message-artifacts-row"
      >
        <ArtifactChip
          v-for="a in artifacts"
          :key="a.id"
          :artifact="a"
          @open="onArtifactChipOpen"
        />
      </div>

      <!--
        Long-turn-resilience-01KR3PRS WP03 — partial-output footer.
        Renders for assistant rows whose stream dropped before the
        kernel could land a clean assistant turn. When recoverable, a
        Resume button surfaces; otherwise a static caption tells the
        user the turn cannot be resumed (tool_use already ran) and to
        re-issue the request.
      -->
      <div
        v-if="isPartialOutputBubble"
        class="mt-2 flex items-center gap-2 font-ui text-[11px] text-ink-subtle"
        data-testid="message-partial-output-footer"
      >
        <span :data-failure-kind="streamingFailureKind || 'unknown'">
          {{ partialOutputCopy }}
        </span>
        <button
          v-if="streamingRecoverable && messageId"
          type="button"
          class="ml-1 px-1.5 py-0.5 rounded border border-accent/60 text-accent hover:bg-accent/10 normal-case tracking-normal"
          data-testid="message-partial-resume-button"
          :data-message-id="messageId"
          @click="onResumeClick"
        >
          Resume
        </button>
      </div>
    </div>
  </article>
</template>
