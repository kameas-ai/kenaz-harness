<script setup lang="ts">
import { useShellStatus } from '@/lib/useHarnessAPI';

/**
 * StatusPill — Model · Tier · Build footer triple (FR-001f).
 * Mirrors Kenaz's bottom-left key-value triple. Driven by the cross-
 * cutting ShellStatus polled via useShellStatus (WP12 KenazClient-style
 * 5 s polling hook).
 */
const status = useShellStatus();
</script>

<template>
  <div
    class="flex items-center gap-4 px-3 py-1 text-[11px] font-ui text-ink-muted"
    role="status"
    aria-label="Harness status"
  >
    <div class="flex items-center gap-2">
      <span class="text-ink-dim">Active provider</span>
      <span class="font-mono text-ink">{{ status.value.activeProvider }}</span>
    </div>
    <div class="text-ink-dim">·</div>
    <div class="flex items-center gap-2">
      <span class="text-ink-dim">Trust tier</span>
      <span class="font-mono text-ink">{{ status.value.trustTier }}</span>
    </div>
    <div class="text-ink-dim">·</div>
    <div class="flex items-center gap-2">
      <span class="text-ink-dim">Harness build</span>
      <span class="font-mono text-ink">{{ status.value.harnessBuild }}</span>
    </div>
  </div>
</template>
