<script setup lang="ts">
/**
 * NotAvailableInServedMode — empty-state panel shown in served mode
 * when a view depends on RPCs that are not ported to the HTTP transport.
 *
 * (served-mode-honesty-WZQR1ZJE WP02 / FR-002 / FR-006)
 *
 * Usage: render this component when the view detects a served-mode
 * limitation (e.g. catches ServedUnsupportedError or calls useServedMode()).
 *
 * # Copy rule
 *
 * The body text must stay ACTIONABLE FROM INSIDE A WORKBENCH. This
 * harness is the default app in a Kenaz workbench VM; its user often has
 * no desktop harness at all, so "run the desktop app" is not advice, it
 * is a dead end. Say what this specific view needs and what does work
 * here instead. Pass `reason` to be specific; the default only states the
 * general truth.
 */

const props = withDefaults(
  defineProps<{
    /**
     * Short name of the unsupported feature shown in the heading.
     * Defaults to "This feature".
     */
    feature?: string;
    /**
     * One sentence explaining what this view needs that the in-workbench
     * build does not expose yet. Keep it specific — a reader should learn
     * something they did not already know from the heading.
     */
    reason?: string;
  }>(),
  {
    feature: 'This feature',
    reason:
      'It depends on parts of the harness API that the in-workbench build does not expose yet.',
  },
);
</script>

<template>
  <div
    class="flex flex-col items-center justify-center h-full py-16 px-6 gap-4 text-center"
    data-testid="not-available-in-served-mode"
    role="status"
  >
    <div
      class="w-10 h-10 rounded-full bg-surface-2 flex items-center justify-center text-ink-muted"
      aria-hidden="true"
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="1.5"
        stroke-linecap="round"
        stroke-linejoin="round"
        class="w-5 h-5"
      >
        <!-- cloud with slash -->
        <path d="M20 16.2A4.5 4.5 0 0 0 17.5 8h-1.8A7 7 0 1 0 4 15" />
        <line x1="4" y1="4" x2="20" y2="20" />
      </svg>
    </div>
    <div class="flex flex-col gap-1">
      <span class="font-ui text-sm font-medium text-ink">
        {{ props.feature }} is not available in served mode
      </span>
      <span class="font-ui text-xs text-ink-muted max-w-xs">
        {{ props.reason }}
      </span>
      <span class="font-ui text-xs text-ink-muted max-w-xs">
        Sessions work here: create a conversation, send a message, watch it
        stream, and stop it.
      </span>
    </div>
  </div>
</template>
