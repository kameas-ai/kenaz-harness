<script setup lang="ts">
/**
 * LibraryView — /library/:view(captured|authored)
 * (artifacts-as-units-01DOGF0C WP06, spec FR-7).
 *
 * One home for the two kinds of thing the harness keeps for you, which since
 * migration units/1104 live in one store (core/units):
 *
 *   - Captured — artifacts: code blocks, tool outputs, model images and pins
 *     the harness captured, with source / scope / MIME filters, preview,
 *     promote, delete and provenance (the former ArtifactsView).
 *   - Authored — documents you or the assistant wrote: editor, sanitized
 *     preview, version conflicts, knowledge-site build (the former
 *     DocumentsView).
 *
 * Shared chrome owned here: the view switcher and one title search that
 * filters whichever view is showing. Each view keeps its own kind-specific
 * controls (artifact scopes span the whole store; documents are one
 * session's visible set, so their scope semantics differ).
 *
 * Integration point (nav-ia-sweep-01DOGF0F / knowledge-home-01DOGF0E): this
 * ships its own two-tab switcher because E's generic section switcher is not
 * on main yet. When it lands, swap the <nav data-testid="library-switcher">
 * block for it; the routes and the two embedded views do not change.
 */
import { computed, ref } from 'vue';
import { useRoute } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import ArtifactsView from '@/views/artifacts/ArtifactsView.vue';
import DocumentsView from '@/views/documents/DocumentsView.vue';

type LibraryTab = 'captured' | 'authored';

const route = useRoute();
const view = computed<LibraryTab>(() =>
  route.params.view === 'authored' ? 'authored' : 'captured',
);
const query = ref('');

const TABS: readonly { id: LibraryTab; label: string; hint: string }[] = [
  { id: 'captured', label: 'Captured', hint: 'Artifacts the harness captured' },
  { id: 'authored', label: 'Authored', hint: 'Documents you or the assistant wrote' },
];
</script>

<template>
  <div class="h-full flex flex-col" data-testid="library-view">
    <CanvasHead
      number="09"
      section="LIBRARY"
      title="Library"
      subtitle="Everything kept for you: what the harness captured from your sessions, and the documents you write."
    />

    <div class="px-6 pt-3 pb-2 flex flex-wrap items-center gap-4 border-b border-border-muted">
      <nav class="flex items-center gap-1" aria-label="Library views" data-testid="library-switcher">
        <router-link
          v-for="t in TABS"
          :key="t.id"
          :to="{ path: `/library/${t.id}`, query: route.query }"
          class="rounded-sm border px-3 py-1 font-ui text-[12px]"
          :class="
            view === t.id
              ? 'border-accent bg-surface-2 text-accent'
              : 'border-border-muted bg-surface-1 text-ink-muted hover:bg-surface-2 hover:text-ink'
          "
          :aria-current="view === t.id ? 'page' : undefined"
          :title="t.hint"
          :data-testid="`library-tab-${t.id}`"
        >
          {{ t.label }}
        </router-link>
      </nav>
      <label class="flex items-center gap-2">
        <span class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle">Search</span>
        <input
          v-model="query"
          type="search"
          placeholder="Filter by title"
          aria-label="Filter the library by title"
          class="rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink w-64"
          data-testid="library-search"
        />
      </label>
    </div>

    <div class="flex-1 min-h-0">
      <ArtifactsView v-if="view === 'captured'" embedded :query="query" />
      <DocumentsView v-else embedded :query="query" />
    </div>
  </div>
</template>
