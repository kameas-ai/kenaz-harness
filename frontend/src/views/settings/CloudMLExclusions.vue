<script setup lang="ts">
/**
 * CloudMLExclusions — the org's exclusions, read-only, for CloudMLPanel
 * (WP05). Paths and command prefixes are enforced on this device; legacy
 * notes are the org's old free text and are shown only (never matched).
 *
 * Legal (kameas-ai-11, 2026-10-16): while the ML notice is shown, this
 * renders INSIDE the notice card, after the notice text and before any
 * action button, so the full notice plus exclusions is visible before
 * "Turn on Offload" / "I have read this". Otherwise the panel renders it
 * on its own.
 */
defineProps<{
  paths: string[];
  commands: string[];
  legacyNotes: string[];
}>();
</script>

<template>
  <div v-if="paths.length || commands.length" class="space-y-1 text-[12px]" data-testid="cloud-ml-exclusions">
    <p>Your organization excludes:</p>
    <ul class="list-disc pl-5">
      <li v-for="p in paths" :key="'p:' + p" data-testid="cloud-ml-exclusion-path">
        files matching <code>{{ p }}</code>
      </li>
      <li v-for="c in commands" :key="'c:' + c" data-testid="cloud-ml-exclusion-command">
        commands starting with <code>{{ c }}</code>
      </li>
    </ul>
    <p class="text-ink-muted">
      These are checked on this device. Matching files and commands are never sent; only that a
      tool ran, its outcome and timing.
    </p>
  </div>
  <div v-if="legacyNotes.length" class="space-y-1 text-[12px] text-ink-muted" data-testid="cloud-ml-legacy-notes">
    <p>Earlier notes from your organization (for information; not applied as patterns):</p>
    <ul class="list-disc pl-5">
      <li v-for="(n, i) in legacyNotes" :key="i" data-testid="cloud-ml-legacy-note">{{ n }}</li>
    </ul>
  </div>
</template>
