<script setup lang="ts">
/**
 * LongSessionNudge — dismissible inline banner that appears when a session
 * crosses a turn-count or token-count threshold (v0.5.6 memory-trust-signals).
 *
 * Rendered above the ChatInput, below the message list. Shows three actions:
 *   [Branch from here]        — emit 'branch'  → parent triggers branch flow
 *   [+ New session]           — emit 'newSession' → parent creates a fresh session
 *   [Dismiss for this session] — emit 'dismiss' → parent sets per-session flag
 *
 * This component is purely presentational. State management (threshold
 * detection, dismiss persistence) lives in useLongSessionNudge.ts.
 */

const emit = defineEmits<{
  /**
   * Fires when the user clicks "Branch from here".
   * Parent should open the CreateBranchModal or invoke Branches_Create directly.
   */
  branch: [];
  /**
   * Fires when the user clicks "+ New session".
   * Parent should invoke Sessions_Create and route to the new session.
   */
  newSession: [];
  /**
   * Fires when the user clicks "Dismiss for this session".
   * Parent should set a session-local flag so the banner doesn't re-appear.
   */
  dismiss: [];
}>();

function handleBranch() {
  emit('branch');
}

function handleNewSession() {
  emit('newSession');
}

function handleDismiss() {
  emit('dismiss');
}
</script>

<template>
  <div
    class="flex items-start gap-3 border-t border-b border-accent-hairline bg-surface-1 px-4 py-3"
    role="note"
    aria-label="Long session nudge"
    data-testid="long-session-nudge"
  >
    <!-- Icon -->
    <span class="mt-0.5 flex-shrink-0 text-[15px]" aria-hidden="true">💡</span>

    <!-- Content + actions -->
    <div class="flex min-w-0 flex-1 flex-col gap-2">
      <div>
        <p class="font-ui text-[13px] font-medium text-ink">
          This conversation is getting long.
        </p>
        <p class="font-ui text-[12px] text-ink-muted">
          Memory will carry forward what matters across new sessions.
        </p>
      </div>

      <div class="flex flex-wrap items-center gap-2">
        <button
          type="button"
          class="rounded-sm border border-accent-hairline px-2.5 py-1 font-ui text-[12px] text-accent hover:bg-accent-glow transition-fast"
          data-testid="long-session-nudge-branch"
          @click="handleBranch"
        >
          Branch from here
        </button>
        <button
          type="button"
          class="rounded-sm border border-accent px-2.5 py-1 font-ui text-[12px] text-accent hover:bg-accent-glow transition-fast"
          data-testid="long-session-nudge-new-session"
          @click="handleNewSession"
        >
          + New session
        </button>
        <button
          type="button"
          class="font-ui text-[12px] text-ink-muted hover:text-ink ml-auto transition-fast"
          data-testid="long-session-nudge-dismiss"
          @click="handleDismiss"
        >
          Dismiss for this session
        </button>
      </div>
    </div>
  </div>
</template>
