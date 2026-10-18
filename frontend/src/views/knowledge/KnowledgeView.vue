<script setup lang="ts">
/**
 * KnowledgeView — /knowledge/:section(curated|learned)
 * (knowledge-home-01DOGF0E WP02, spec FR-1..FR-3).
 *
 * One home for everything that shapes what the model knows, in two sections
 * that keep their different mechanics visible (the deterministic-vs-
 * embeddings axis 01NKNOW01 already drew when Corpora merged into Contexts):
 *
 *   - Curated — the context library (ContextsView): files you write,
 *     attached at conversation start or read on demand.
 *   - Learned — long-term memory (MemoryView): chunks captured from your
 *     conversations, retrieved by similarity every turn.
 *
 * This is an IA merge, not a data merge (owner ruling 2026-10-04): the two
 * stores, their RPCs and their views are unchanged. Each view is mounted
 * `embedded`, which drops its own page header; this view owns the header,
 * the section switcher and the one-line explainer.
 */
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import SectionSwitcher from '@/components/ui/SectionSwitcher.vue';
import type { SectionSwitcherItem } from '@/components/ui/sectionSwitcherTypes';
import ContextsView from '@/views/contexts/ContextsView.vue';
import MemoryView from '@/views/memory/MemoryView.vue';

type KnowledgeSection = 'curated' | 'learned';

const route = useRoute();
const section = computed<KnowledgeSection>(() =>
  route.params.section === 'learned' ? 'learned' : 'curated',
);

const SECTIONS: readonly SectionSwitcherItem[] = [
  { id: 'curated', label: 'Curated', hint: 'Context files you write and attach' },
  { id: 'learned', label: 'Learned', hint: 'Memory captured from your conversations' },
];

/** FR-3 — each section states its mechanic in one line. */
const EXPLAINERS: Readonly<Record<KnowledgeSection, string>> = {
  curated: 'Files you write; attached at conversation start or read on demand.',
  learned: 'Captured from your conversations; retrieved by similarity each turn.',
};
</script>

<template>
  <div class="h-full flex flex-col" data-testid="knowledge-view">
    <CanvasHead
      number="07"
      section="KNOWLEDGE"
      title="Knowledge"
      subtitle="What the model knows beyond the conversation: the context you curate, and the memory it learns."
    />

    <div class="px-6 pt-3 pb-2 flex flex-wrap items-center gap-4 border-b border-border-muted">
      <SectionSwitcher
        :sections="SECTIONS"
        :active="section"
        base-path="/knowledge"
        nav-label="Knowledge sections"
        test-id-prefix="knowledge"
      />
      <p class="font-ui text-[12px] text-ink-muted" data-testid="knowledge-explainer">
        {{ EXPLAINERS[section] }}
      </p>
    </div>

    <div v-if="section === 'curated'" class="flex-1 min-h-0" data-testid="knowledge-section-curated">
      <ContextsView embedded />
    </div>
    <div v-else class="flex-1 min-h-0 overflow-y-auto" data-testid="knowledge-section-learned">
      <MemoryView embedded />
    </div>
  </div>
</template>
