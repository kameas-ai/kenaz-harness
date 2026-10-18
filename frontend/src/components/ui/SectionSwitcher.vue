<script setup lang="ts">
/**
 * SectionSwitcher — a route-linked row of section tabs for a merged home
 * (knowledge-home-01DOGF0E WP02, spec FR-1).
 *
 * Each section is a path segment under `basePath` (`/knowledge/curated`,
 * `/knowledge/learned`), so the active section survives reload, deep links
 * and a persisted lastRoute. The current query string is carried across a
 * switch, matching the Library switcher.
 *
 * Markup and classes deliberately match the inline switcher
 * artifacts-as-units-01DOGF0C shipped in LibraryView.vue
 * (`<nav data-testid="library-switcher">`) so the two merged homes look and
 * behave the same. Swapping LibraryView onto this component is a recorded
 * follow-up (docs/missions/knowledge-home.md D2): pass
 * `test-id-prefix="library"` and its existing test ids survive unchanged.
 */
import { useRoute } from 'vue-router';
import type { SectionSwitcherItem } from './sectionSwitcherTypes';

const props = defineProps<{
  sections: readonly SectionSwitcherItem[];
  /** The active section id. */
  active: string;
  /** Route prefix, e.g. "/knowledge" — no trailing slash. */
  basePath: string;
  /** Accessible name for the nav landmark. */
  navLabel: string;
  /** Prefix for data-testid: `<prefix>-switcher`, `<prefix>-tab-<id>`. */
  testIdPrefix: string;
}>();

const route = useRoute();
</script>

<template>
  <nav
    class="flex items-center gap-1"
    :aria-label="props.navLabel"
    :data-testid="`${props.testIdPrefix}-switcher`"
  >
    <router-link
      v-for="s in props.sections"
      :key="s.id"
      :to="{ path: `${props.basePath}/${s.id}`, query: route.query }"
      class="rounded-sm border px-3 py-1 font-ui text-[12px]"
      :class="
        props.active === s.id
          ? 'border-accent bg-surface-2 text-accent'
          : 'border-border-muted bg-surface-1 text-ink-muted hover:bg-surface-2 hover:text-ink'
      "
      :aria-current="props.active === s.id ? 'page' : undefined"
      :title="s.hint"
      :data-testid="`${props.testIdPrefix}-tab-${s.id}`"
    >
      {{ s.label }}
    </router-link>
  </nav>
</template>
