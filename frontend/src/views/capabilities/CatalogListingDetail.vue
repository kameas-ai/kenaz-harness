<script setup lang="ts">
/**
 * CatalogListingDetail — the fleet-catalog half of a Capabilities detail
 * pane (install-framework-01DOGF0B Phase 4 WP08, folded in from the retired
 * MarketplaceView). Rendered:
 *
 *   - alone, for a catalog-only row (a bundle / agent_pack item, or an
 *     installed/ residue download) — `mode="entry"`: name, description,
 *     the disabled-with-reason posture or the "Downloaded — not active"
 *     state, plus Remove download;
 *   - under a provider plugin, for a skill / workflow row that is also a
 *     catalog listing — `mode="listing"`: just the listing facts.
 *
 * Both modes carry the listing facts (visibility, version, published date)
 * and Withdraw (Catalog_Unpublish — fleet-enforcement-truth-01PMZ505 WP11).
 * Withdraw is shown unconditionally: CatalogItemView carries no publisher
 * identity, so the server decides (owner or fleet admin) and its answer is
 * reported as-is. The surface owns the confirm dialog.
 */
import { computed } from 'vue';
import { useRouter } from 'vue-router';
import type { CatalogItemView } from '@/lib/types';
import { VISIBILITY_LABELS, catalogOnlyKind, residueText } from './catalogBrowse';

const props = defineProps<{
  entry: CatalogItemView;
  mode: 'entry' | 'listing';
  /** entry mode: the row is installed/ residue, not a browse row. */
  residue?: boolean;
  busy?: boolean;
}>();
const emit = defineEmits<{
  (e: 'withdraw', entry: CatalogItemView): void;
  (e: 'remove-download', entry: CatalogItemView): void;
}>();

const router = (() => {
  try {
    return useRouter();
  } catch {
    return null;
  }
})();

const kindInfo = computed(() => catalogOnlyKind(props.entry.kind));
const publishedDate = computed(() => {
  const at = props.entry.published_at;
  if (!at) return null;
  const d = new Date(at);
  return Number.isNaN(d.getTime()) ? at : d.toLocaleDateString();
});

function openAlternative(to: string) {
  void router?.push(to);
}
</script>

<template>
  <section class="space-y-3" :data-testid="`catalog-detail-${entry.kind}-${entry.id}`">
    <header v-if="mode === 'entry'">
      <h3 class="font-ui text-[13px] font-semibold text-ink">{{ entry.slug }}</h3>
      <p class="font-ui text-[11px] text-ink-muted">
        {{ kindInfo?.noun ?? entry.kind }} · fleet catalog
      </p>
      <p class="mt-2 font-ui text-[12px] text-ink-muted" data-testid="catalog-detail-description">
        {{ entry.description || 'No description.' }}
      </p>
    </header>

    <template v-if="mode === 'entry'">
      <p
        v-if="residue"
        class="font-ui text-[12px] text-ink-muted"
        data-testid="catalog-detail-residue"
      >
        <span class="text-ink">Downloaded — not active.</span> {{ residueText(entry.kind) }}
      </p>
      <p
        v-if="kindInfo"
        class="font-ui text-[12px] text-ink-muted"
        data-testid="catalog-detail-unsupported"
      >
        {{ kindInfo.reason }}
      </p>
      <div class="flex flex-wrap items-center gap-2">
        <button
          v-if="kindInfo?.alternative"
          type="button"
          class="rounded-sm border border-accent-hairline bg-surface-1 px-2.5 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow"
          :data-testid="`catalog-detail-alternative-${entry.kind}`"
          @click="openAlternative(kindInfo.alternative.to)"
        >
          {{ kindInfo.alternative.label }} →
        </button>
        <button
          v-if="residue"
          type="button"
          class="rounded-sm border border-border-muted px-2.5 py-1 font-ui text-[11px] text-ink-muted hover:bg-surface-2 disabled:opacity-50"
          :disabled="busy"
          :data-testid="`catalog-detail-remove-download-${entry.kind}-${entry.id}`"
          @click="emit('remove-download', entry)"
        >
          {{ busy ? 'Removing…' : 'Remove download' }}
        </button>
      </div>
    </template>

    <div
      class="space-y-1 border-t border-border-muted pt-3 font-ui text-[11px] text-ink-muted"
      :data-testid="`catalog-listing-${entry.kind}-${entry.id}`"
    >
      <div class="text-[10px] uppercase tracking-[0.14em] text-ink-subtle">Fleet catalog listing</div>
      <div>
        Visibility:
        <span class="text-ink" data-testid="catalog-listing-visibility">{{ VISIBILITY_LABELS[entry.visibility] ?? entry.visibility }}</span>
        · v{{ entry.version }}<span v-if="publishedDate"> · published {{ publishedDate }}</span>
      </div>
      <button
        type="button"
        class="mt-1 rounded-sm border border-signal-danger px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-signal-danger hover:bg-surface-2 disabled:opacity-50"
        :disabled="busy"
        :data-testid="`capability-withdraw-${entry.kind}-${entry.id}`"
        @click="emit('withdraw', entry)"
      >
        Withdraw from catalog
      </button>
    </div>
  </section>
</template>
