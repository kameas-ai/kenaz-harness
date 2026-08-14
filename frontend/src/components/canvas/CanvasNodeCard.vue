<script setup lang="ts">
/**
 * One node box on the shared canvas
 * (visual-graph-authoring-01PMUX01 WP02).
 *
 * Renders a kind-category colour bar, the label, the kind, and one
 * vue-flow `Handle` per declared port. Ports are the hit targets edge
 * drawing uses, so the handle id IS the port name — that is what lets
 * `GraphCanvas` turn a vue-flow connect event into a spec edge without
 * a lookup table.
 *
 * The `status` prop is the WP05 run-overlay hook. It is rendered as a
 * ring + a badge here and nothing else in the canvas depends on it, so
 * WP05 lands the overlay by populating adapter data — no component
 * surgery.
 */
import { computed } from 'vue';
import { Handle, Position } from '@vue-flow/core';

import type { CanvasNodeStatus, CanvasPort } from '@/lib/canvas/types';

const props = defineProps<{
  data: {
    label: string;
    kind: string;
    category: string;
    inputs: CanvasPort[];
    outputs: CanvasPort[];
    status?: CanvasNodeStatus;
    selected?: boolean;
  };
}>();

/**
 * Category colours come from the theme tokens (the css-token gate bans
 * raw literals), so the canvas re-themes with the rest of the app.
 */
const categoryClass = computed(() => {
  switch (props.data.category) {
    case 'compute':
      return 'bg-accent';
    case 'control':
      return 'bg-signal-warn';
    case 'state':
      return 'bg-signal-ok';
    default:
      return 'bg-border-muted';
  }
});

const statusClass = computed(() => {
  switch (props.data.status) {
    case 'running':
      return 'border-accent';
    case 'complete':
      return 'border-signal-ok';
    case 'failed':
      return 'border-signal-danger';
    case 'skipped':
    case 'not_reached':
      return 'border-border-muted opacity-50';
    case 'incomplete':
      return 'border-signal-warn';
    default:
      return 'border-border-muted';
  }
});
</script>

<template>
  <div
    class="relative flex h-full w-full flex-col overflow-hidden rounded-md border bg-surface-1"
    :class="[statusClass, data.selected ? 'ring-1 ring-accent' : '']"
    :data-testid="`canvas-node-${data.kind}`"
    :data-status="data.status ?? ''"
    :data-category="data.category"
  >
    <div class="h-1 w-full" :class="categoryClass" aria-hidden="true" />
    <div class="flex min-w-0 flex-1 flex-col justify-center px-2 py-1">
      <span class="truncate font-ui text-[12px] text-ink">{{ data.label }}</span>
      <span class="truncate font-mono text-[10px] text-ink-muted">{{ data.kind }}</span>
    </div>
    <span
      v-if="data.status"
      class="absolute right-1 top-1.5 rounded-sm border border-border-muted px-1 font-ui text-[9px] uppercase tracking-[0.14em] text-ink-muted"
      data-testid="canvas-node-status"
      >{{ data.status }}</span
    >

    <Handle
      v-for="(port, i) in data.inputs"
      :key="`in-${port.name}`"
      type="target"
      :id="port.name"
      :position="Position.Top"
      :style="{ left: `${((i + 1) / (data.inputs.length + 1)) * 100}%` }"
      :data-port="port.name"
    />
    <Handle
      v-for="(port, i) in data.outputs"
      :key="`out-${port.name}`"
      type="source"
      :id="port.name"
      :position="Position.Bottom"
      :style="{ left: `${((i + 1) / (data.outputs.length + 1)) * 100}%` }"
      :data-port="port.name"
    />
  </div>
</template>
