<script setup lang="ts">
/**
 * NotFoundView — renders a user-friendly 404 not-found page for any
 * unmatched route. Added in WP08 (FR-011 — unknown routes render not-found).
 */
import { useRoute, useRouter } from 'vue-router';

const route = useRoute();
const router = useRouter();
</script>

<template>
  <div class="flex h-full flex-col items-center justify-center gap-4 px-8 py-12 font-ui">
    <div class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle">404</div>
    <h1 class="text-lg font-semibold text-ink">Page not found</h1>
    <p class="max-w-xs text-center text-sm text-ink-muted">
      The path <code class="font-mono text-xs text-ink">{{ route.fullPath }}</code> doesn't
      exist. It may have been moved or removed.
    </p>
    <button
      type="button"
      class="mt-2 rounded-sm border border-border-muted bg-surface-2 px-3 py-1.5 text-sm text-ink hover:bg-surface-3"
      @click="router.push('/')"
    >
      Go to sessions
    </button>
  </div>
</template>
