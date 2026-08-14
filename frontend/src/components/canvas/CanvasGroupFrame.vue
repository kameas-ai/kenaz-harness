<script setup lang="ts">
/**
 * The frame drawn around a loop / retry body
 * (visual-graph-authoring-01PMUX01 WP02).
 *
 * MEMBERSHIP DISPLAY ONLY. `attrs.body` is a list of node ids, not a
 * nesting of the spec's flat `nodes:` array, and the canvas must not
 * invent a second nesting model (plan.md risk "Loop bodies"). So this
 * frame is a *decoration sized to its members* — dragging a node out of
 * it changes nothing, and body editing stays in the attribute panel.
 * The frame still carries handles, because the container node is what
 * the outer graph's edges actually connect to (exactly as the kernel
 * sees it: body nodes are hidden and the container stands for them).
 */
import { Handle, Position } from '@vue-flow/core';

import type { CanvasPort } from '@/lib/canvas/types';

defineProps<{
  data: {
    label: string;
    kind: string;
    inputs: CanvasPort[];
    outputs: CanvasPort[];
    selected?: boolean;
  };
}>();
</script>

<template>
  <div
    class="h-full w-full rounded-md border border-dashed border-border-muted bg-surface-2/40"
    :class="data.selected ? 'ring-1 ring-accent' : ''"
    :data-testid="`canvas-group-${data.kind}`"
  >
    <div
      class="flex items-center gap-1 border-b border-dashed border-border-muted px-2 py-1"
    >
      <span class="truncate font-ui text-[11px] uppercase tracking-[0.14em] text-ink-muted">
        {{ data.label }}
      </span>
      <span class="ml-auto font-mono text-[10px] text-ink-muted">{{ data.kind }}</span>
    </div>

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
