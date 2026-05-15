<script setup lang="ts">
/**
 * TraceView — renders inline trace annotations for a message bubble.
 *
 * Currently surfaces one category of annotations:
 *   adapter.fallback_attempted — shows which provider/model the fallback
 *   runner hopped to for this turn, with the trigger that fired.
 *
 * Props:
 *   - actualProvider: the provider that ultimately served this turn
 *     (from Message.actualProvider). Empty when no fallback occurred.
 *   - actualModel: the model that served this turn
 *     (from Message.actualModel). Empty when no fallback occurred.
 *
 * Rendered below the message content in MessageBubble or similar parent.
 *
 * model-fallback-routing-01NDFSEX04 WP05.
 */
defineProps<{
  /**
   * Provider that ultimately served this turn. When non-empty a fallback
   * hop occurred; the pill shows the rerouted provider.
   */
  actualProvider?: string;
  /**
   * Model that ultimately served this turn (may be empty when the provider
   * uses its default model).
   */
  actualModel?: string;
}>();
</script>

<template>
  <div
    v-if="actualProvider"
    class="mt-1 inline-flex items-center gap-1 rounded bg-surface-0 border border-border-muted px-1.5 py-0.5 font-ui text-[10px] text-ink-muted"
    role="note"
    aria-label="Fallback provider used"
    data-testid="trace-view-fallback"
  >
    <svg
      class="h-3 w-3 text-signal-warning flex-shrink-0"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      stroke-width="1.5"
    >
      <path d="M2 8h12M10 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round" />
    </svg>
    <span>via {{ actualProvider }}<template v-if="actualModel"> / {{ actualModel }}</template></span>
  </div>
</template>
