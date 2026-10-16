<script setup lang="ts">
/**
 * CloudMLExclusions — the org's exclusions, read-only, for CloudMLPanel
 * (WP05). Paths and command prefixes are enforced on this device; legacy
 * notes are the org's old free text and are shown only (never matched), as
 * one sentence that says so plainly.
 *
 * Legal (kameas-ai-11, 2026-10-16): while the ML notice is shown, this
 * renders INSIDE the notice card, after the notice text and before any
 * action button, so the full notice plus exclusions is visible before
 * "Turn on Offload" / "I have read this". Otherwise the panel renders it
 * on its own.
 */
import { computed } from 'vue';

const props = defineProps<{
  paths: string[];
  commands: string[];
  legacyNotes: string[];
}>();

/**
 * Legal (kameas-ai-11, 2026-10-16): the legacy free-text notes are one
 * candid sentence, mirrored from Fleet's exclusions template 2 (kenaz-fleet
 * PR #289, c9b08ca2, service/ml_notice_text.go mlExclusionsLegacyLeadV2):
 * this lead, the notes joined with "; ", and a final ".". Shown only when
 * legacy notes exist. Pinned against Fleet's shared fixture
 * (__tests__/fixtures/ml_notice_text.json). If this sentence changes, bump
 * localExclusionsTemplate in core/rpc/views/settings/ml.go in lockstep.
 */
const LEGACY_NOTES_LEAD =
  "Your organization also listed these exclusions in its own words. Fleet can't apply them " +
  'automatically, so matching activity may still be uploaded: ';
const legacySentence = computed(() => LEGACY_NOTES_LEAD + props.legacyNotes.join('; ') + '.');
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
  <p v-if="legacyNotes.length" class="text-[12px]" data-testid="cloud-ml-legacy-notes">{{ legacySentence }}</p>
</template>
