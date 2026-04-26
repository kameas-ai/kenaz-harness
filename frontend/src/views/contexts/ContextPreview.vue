<script setup lang="ts">
/**
 * ContextPreview — renders the selected file's content.
 *
 * v1 uses a plain `whitespace-pre-wrap` block — same approach as
 * MessageBubble. v-html / a markdown HTML pipeline is gated on a
 * sanitiser policy that hasn't been ratified; until then the textual
 * source is rendered as-is, which is the safe default and still
 * readable for the markdown-flavoured files users drop in.
 *
 * The in-place editor lands in WP05 (Edit / Save / Cancel); for WP01
 * the preview is read-only.
 */
defineProps<{
  path: string | null;
  content: string;
  loading?: boolean;
  error?: string | null;
}>();
</script>

<template>
  <section
    class="h-full flex flex-col"
    :aria-label="path ? `Preview of ${path}` : 'Context preview'"
  >
    <header
      v-if="path"
      class="px-4 py-2 border-b border-border-muted bg-surface-1 flex items-baseline gap-2"
    >
      <span class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle">
        Preview
      </span>
      <span class="font-mono text-[12px] text-ink truncate">{{ path }}</span>
    </header>
    <div class="flex-1 overflow-auto">
      <div
        v-if="loading"
        class="px-6 py-4 font-ui text-sm text-ink-muted"
        data-testid="context-preview-loading"
      >
        Loading…
      </div>
      <div
        v-else-if="error"
        class="px-6 py-4 font-ui text-sm text-signal-danger"
        role="alert"
        data-testid="context-preview-error"
      >
        {{ error }}
      </div>
      <div
        v-else-if="!path"
        class="px-6 py-4 font-ui text-sm text-ink-subtle"
        data-testid="context-preview-empty"
      >
        Select a file to preview.
      </div>
      <pre
        v-else
        class="px-6 py-4 font-mono text-[12px] leading-relaxed text-ink whitespace-pre-wrap"
        data-testid="context-preview-content"
      >{{ content }}</pre>
    </div>
  </section>
</template>
