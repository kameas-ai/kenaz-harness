<script setup lang="ts">
import type { Component } from 'vue';
import { computed } from 'vue';
import { useRoute } from 'vue-router';

const props = defineProps<{
  to?: string;
  label: string;
  icon: Component;
  active?: boolean;
}>();

const route = useRoute();
const isActive = computed(
  () => props.active ?? (props.to ? route.path === props.to : false),
);
</script>

<template>
  <component
    :is="to ? 'router-link' : 'button'"
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
