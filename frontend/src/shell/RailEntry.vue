<script setup lang="ts">
import type { Component } from 'vue';
import { computed } from 'vue';
import { useRoute } from 'vue-router';
import { railPathMatches } from './railMatch';
import { DESKTOP_ONLY_LABEL } from '@/lib/servedCapabilities';

const props = withDefaults(
  defineProps<{
    to?: string;
    label: string;
    icon: Component;
    /** Explicit override. Left undefined, the route decides. */
    active?: boolean;
    /**
     * nav-ia-sweep-01DOGF0F WP02 (FR-2): path prefix(es) that keep this
     * entry highlighted across nested routes — the same idea as
     * SettingsTabs' Tab `matchPrefix`. Without it the entry is active only
     * on an exact path match. Segment-bounded (see railMatch.ts).
     */
    matchPrefix?: string | readonly string[];
    /**
     * Audit findings HR-4 / HR-5: when set, the surface does not work in
     * this build (lib/servedCapabilities.ts). The entry is muted, marked
     * "Desktop only", does not navigate, and the value is its tooltip —
     * the user learns it is unavailable in the nav, not by clicking in.
     */
    unavailable?: string | null;
  }>(),
  {
    to: undefined,
    // Vue casts an absent Boolean prop to `false` unless a default exists,
    // which would make `active ?? …` below always pick the override and
    // never consult the route. An explicit undefined default keeps
    // "absent" distinguishable from "false".
    active: undefined,
    matchPrefix: undefined,
    unavailable: null,
  },
);

// useRoute() returns undefined outside a router context (vitest unit
// mounts); degrade to "not active" rather than throwing.
const route = useRoute() as ReturnType<typeof useRoute> | undefined;
const isActive = computed(() => {
  if (props.active !== undefined) return props.active;
  const path = route?.path ?? '';
  if (props.matchPrefix !== undefined) return railPathMatches(path, props.matchPrefix);
  return props.to ? path === props.to : false;
});
</script>

<template>
  <span
    v-if="unavailable"
    class="flex items-center gap-2 px-3 py-2 rounded-sm w-full text-left text-sm font-ui text-ink-subtle opacity-60 cursor-not-allowed"
    role="link"
    aria-disabled="true"
    :aria-label="`${label} (${DESKTOP_ONLY_LABEL})`"
    :title="`${DESKTOP_ONLY_LABEL}. ${unavailable}`"
    data-rail-unavailable="true"
  >
    <component :is="icon" :size="14" />
    <span class="truncate hidden two-col:inline">{{ label }}</span>
    <span
      class="ml-auto hidden two-col:inline shrink-0 font-ui text-[9px] uppercase tracking-[0.14em]"
    >{{ DESKTOP_ONLY_LABEL }}</span>
  </span>
  <component
    :is="to ? 'router-link' : 'button'"
    v-else
    :to="to"
    type="button"
    class="flex items-center gap-2 px-3 py-2 rounded-sm w-full text-left text-sm font-ui transition-fast ease-kenaz"
    :class="
      isActive
        ? 'text-ink bg-surface-2 ring-1 ring-accent-hairline'
        : 'text-ink-muted hover:text-ink hover:bg-surface-2'
    "
    :aria-current="isActive ? 'page' : undefined"
    :aria-label="label"
  >
    <component :is="icon" :size="14" :class="isActive ? 'text-accent' : ''" />
    <span class="truncate hidden two-col:inline">{{ label }}</span>
  </component>
</template>
