<script setup lang="ts">
/**
 * GraphCanvas — the ONE drag-and-drop DAG canvas
 * (visual-graph-authoring-01PMUX01 FR-001, WP02).
 *
 * It renders whatever a `CanvasAdapter` hands it and knows nothing
 * about YAML, the agentgraph spec, or the workflow step model. See
 * `@/lib/canvas/types.ts` for the interface and the paper check that
 * designed it against both consumers.
 *
 * WP02 is READ-ONLY ON PURPOSE: this ships the render (auto-layout,
 * category colours, loop-body frames, status hooks) mounted alongside
 * the existing YAML textarea, changing no behaviour. WP03 adds the
 * authoring handlers on top of exactly this component.
 *
 * Positions live in component state, seeded from the adapter's authored
 * layout (which wins) or from dagre. They are only ever written back to
 * the spec on an explicit save — see plan.md's "Layout churn in diffs"
 * risk.
 */
import { computed, ref, watch } from 'vue';
import { useVueFlow, VueFlow, type Edge, type Node } from '@vue-flow/core';
import { Background } from '@vue-flow/background';
import { ControlButton, Controls } from '@vue-flow/controls';

import { autoLayout, NODE_HEIGHT, NODE_WIDTH } from '@/lib/canvas/layout';
import type { CanvasAdapter, CanvasPoint } from '@/lib/canvas/types';

import CanvasGroupFrame from './CanvasGroupFrame.vue';
import CanvasNodeCard from './CanvasNodeCard.vue';

import '@vue-flow/core/dist/style.css';
import '@vue-flow/core/dist/theme-default.css';
import '@vue-flow/controls/dist/style.css';

const props = withDefaults(
  defineProps<{
    adapter: CanvasAdapter;
    selectedNodeId?: string;
    /** Test seam: skip the fit-view animation frame under happy-dom. */
    fitViewOnInit?: boolean;
    /**
     * A caveat about what is being drawn, badged over the canvas
     * (WP05: the degraded-projection warning). Empty renders nothing.
     */
    notice?: string;
  }>(),
  { selectedNodeId: '', fitViewOnInit: true, notice: '' },
);

const emit = defineEmits<{
  (e: 'select-node', id: string): void;
  /**
   * A node carrying a run overlay was clicked (WP05). `detail` is the
   * adapter's own `statusDetail` — for agentgraph that is the
   * materialization's `startSeq`, which is the join key back into the
   * trace. The canvas has no idea what a trace is; it forwards.
   */
  (
    e: 'node-status-click',
    payload: { id: string; status: string; detail?: Record<string, unknown> },
  ): void;
}>();

/**
 * Live positions. Keyed by node id and deliberately NOT derived: once a
 * node has a position it keeps it across re-parses of the YAML buffer,
 * so typing in the textarea does not make the canvas jump.
 */
const positions = ref<Record<string, CanvasPoint>>({});
const sizes = ref<Record<string, { width: number; height: number }>>({});

/**
 * Nodes the USER has dragged since the buffer was last loaded or saved.
 *
 * This set is the whole reason `movedNodeIds` is not simply "position
 * differs from the authored layout". A layout-free graph has no
 * authored position for ANY node, so that formulation reports every
 * node as moved and the first save of a graph nobody touched injects a
 * full auto-layout block — which pins the graph forever (it never
 * reflows again when nodes are added) and breaks spec §4's promise that
 * authored graphs without layout stay byte-identical. Auto-layout is a
 * rendering decision; only a drag is an authoring one.
 */
const draggedIds = ref<Set<string>>(new Set());

/**
 * Ids the user dragged AND whose current position differs from what the
 * spec's layout block says. Dragging a node back onto its authored
 * coordinates leaves nothing to persist.
 */
function movedNodeIds(): string[] {
  const out: string[] = [];
  for (const n of props.adapter.nodes) {
    if (!draggedIds.value.has(n.id)) continue;
    const live = positions.value[n.id];
    if (!live) continue;
    const authored = n.layout;
    if (
      !authored ||
      Math.round(authored.x) !== Math.round(live.x) ||
      Math.round(authored.y) !== Math.round(live.y)
    ) {
      out.push(n.id);
    }
  }
  return out;
}

/**
 * The layout to persist on save: integer coordinates for the nodes the
 * user placed deliberately, and nothing else.
 *
 * Undragged nodes are left OUT of the block on purpose rather than
 * written at their current auto-layout coordinates — an absent entry
 * means "auto-layout decides", which is what keeps the diff to the
 * nodes the author actually moved and lets the rest reflow around them.
 *
 * Families whose spec has no layout block declare
 * `adapter.persistsLayout: false` and always get an empty result. Until
 * the 2026-08-14 unwired sweep that flag was set by both adapters and
 * READ BY NOTHING: workflowAdapter.ts's "move-nodes never arrives"
 * comment described an invariant the canvas did not enforce, and held
 * only because WorkflowGraphEditor happens not to hold a canvas ref.
 */
function pendingLayout(): Record<string, CanvasPoint> {
  const out: Record<string, CanvasPoint> = {};
  if (!props.adapter.persistsLayout) return out;
  for (const id of movedNodeIds()) {
    const p = positions.value[id];
    if (!p) continue;
    out[id] = { x: Math.round(p.x), y: Math.round(p.y) };
  }
  return out;
}

/**
 * Recomputes layout for nodes the canvas has never placed. Nodes the
 * user already dragged keep their live position; nodes carrying an
 * authored layout adopt it the first time they appear.
 */
function relayout(force = false): void {
  const result = autoLayout(props.adapter.nodes, props.adapter.edges);
  const next: Record<string, CanvasPoint> = {};
  const nextSizes: Record<string, { width: number; height: number }> = {};
  for (const n of props.adapter.nodes) {
    const box = result.boxes[n.id];
    nextSizes[n.id] = {
      width: box?.width ?? NODE_WIDTH,
      height: box?.height ?? NODE_HEIGHT,
    };
    const keep = !force && positions.value[n.id];
    next[n.id] = keep
      ? positions.value[n.id]
      : (result.positions[n.id] ?? { x: 0, y: 0 });
  }
  positions.value = next;
  sizes.value = nextSizes;
  if (force) {
    // A forced relayout means the authored layout changed underneath
    // the canvas — a fresh load, a hand edit to the `layout:` block, or
    // the save that just persisted these very drags. Either way the
    // buffer is now the authority and no drag is outstanding.
    draggedIds.value = new Set();
  }
}

watch(
  () => props.adapter.nodes.map((n) => n.id).join('\u0000'),
  () => relayout(),
  { immediate: true },
);
// A change to authored layout (someone hand-edited the `layout:` block)
// re-seeds every position — the YAML is the source of truth.
watch(
  () =>
    props.adapter.nodes
      .map((n) => `${n.id}:${n.layout?.x ?? ''},${n.layout?.y ?? ''}`)
      .join('\u0000'),
  () => relayout(true),
);

const flowNodes = computed<Node[]>(() =>
  props.adapter.nodes.map((n) => ({
    id: n.id,
    type: n.isGroup ? 'kenazGroup' : 'kenazNode',
    position: positions.value[n.id] ?? { x: 0, y: 0 },
    draggable: !props.adapter.readOnly,
    selectable: true,
    connectable: !props.adapter.readOnly,
    // Group frames sit behind their members.
    zIndex: n.isGroup ? 0 : 1,
    style: {
      width: `${sizes.value[n.id]?.width ?? NODE_WIDTH}px`,
      height: `${sizes.value[n.id]?.height ?? NODE_HEIGHT}px`,
    },
    data: {
      label: n.label,
      kind: n.kind,
      category: n.category,
      inputs: n.inputs,
      outputs: n.outputs,
      status: n.status,
      selected: n.id === props.selectedNodeId,
    },
  })),
);

const flowEdges = computed<Edge[]>(() =>
  props.adapter.edges.map((e) => ({
    id: e.id,
    source: e.source,
    sourceHandle: e.sourcePort,
    target: e.target,
    targetHandle: e.targetPort,
    label: `${e.sourcePort} → ${e.targetPort}`,
    animated: e.kind === 'route',
    class: e.kind === 'route' ? 'kenaz-edge-route' : 'kenaz-edge-data',
  })),
);

/**
 * The three viewport controls are re-declared rather than left to
 * `<Controls>`' defaults: the shipped buttons are icon-only with no
 * accessible name, which axe flags (`button-name`) and which the
 * editor's a11y test enforces. Overriding the slots keeps the panel's
 * positioning + styling and gives every button a label.
 */
const { zoomIn, zoomOut, fitView, screenToFlowCoordinate } = useVueFlow();

function onNodeClick(payload: { node: { id: string } }): void {
  selectedEdgeId.value = '';
  emit('select-node', payload.node.id);
  const overlaid = props.adapter.nodes.find((n) => n.id === payload.node.id);
  if (overlaid?.status) {
    emit('node-status-click', {
      id: overlaid.id,
      status: overlaid.status,
      ...(overlaid.statusDetail ? { detail: overlaid.statusDetail } : {}),
    });
  }
}

function onPaneClick(): void {
  selectedEdgeId.value = '';
  emit('select-node', '');
}

// ── authoring (WP03) ──────────────────────────────────────────────────
//
// Every handler below is registered ONLY when the adapter is writable.
// That is deliberate and structural: a read-only canvas has no mutation
// path to guard, which is what closes the materialize-review N3 hazard
// in WP05 rather than papering over it with a disabled attribute.

const selectedEdgeId = ref('');
/** The validator's message for the last refused connection. */
const rejection = ref('');

const writable = computed(() => !props.adapter.readOnly);

/** MIME the NodePalette sets on dragstart. */
const KIND_MIME = 'application/x-kenaz-node-kind';

function onDragOver(ev: DragEvent): void {
  if (!writable.value || !ev.dataTransfer) return;
  if (!ev.dataTransfer.types.includes(KIND_MIME)) return;
  ev.preventDefault();
  ev.dataTransfer.dropEffect = 'copy';
}

function onDrop(ev: DragEvent): void {
  if (!writable.value || !ev.dataTransfer) return;
  const kind =
    ev.dataTransfer.getData(KIND_MIME) || ev.dataTransfer.getData('text/plain');
  if (!kind) return;
  ev.preventDefault();
  rejection.value = '';
  void props.adapter.onSpecOp({
    type: 'add-node',
    kind,
    position: dropPosition(ev),
  });
}

/**
 * Screen → flow coordinates. vue-flow's helper needs a laid-out
 * viewport; under a zero-size test container it returns the input
 * unchanged, which is harmless (the node lands at 0,0 and the author
 * drags it). Rounded because layout persists as integers.
 */
function dropPosition(ev: DragEvent): CanvasPoint {
  try {
    const p = screenToFlowCoordinate({ x: ev.clientX, y: ev.clientY });
    return { x: Math.round(p.x), y: Math.round(p.y) };
  } catch {
    return { x: 0, y: 0 };
  }
}

/**
 * Edge drawing. The verdict comes from the adapter — for agentgraph
 * that is the `Graph_CheckEdge` RPC, i.e. the same Go functions the
 * save path runs. A refusal shows the validator's message and draws
 * nothing.
 */
async function onConnect(conn: {
  source: string;
  target: string;
  sourceHandle?: string | null;
  targetHandle?: string | null;
}): Promise<void> {
  if (!writable.value) return;
  const edge = {
    source: conn.source,
    sourcePort: conn.sourceHandle ?? '',
    target: conn.target,
    targetPort: conn.targetHandle ?? '',
  };
  const verdict = await props.adapter.onCheckEdge(edge);
  if (!verdict.ok) {
    rejection.value = verdict.reason || 'This connection is not allowed.';
    return;
  }
  rejection.value = '';
  void props.adapter.onSpecOp({ type: 'connect', edge });
}

function onEdgeClick(payload: { edge: { id: string } }): void {
  selectedEdgeId.value = payload.edge.id;
  emit('select-node', '');
}

/**
 * Drag-to-move updates COMPONENT STATE only. The spec's `layout:` block
 * is written on save, from `pendingLayout()` — persisting on every drag
 * would make a YAML diff out of every twitch of the mouse (plan.md,
 * "Layout churn in diffs").
 */
function onNodeDragStop(payload: {
  nodes?: Array<{ id: string; position: CanvasPoint }>;
  node?: { id: string; position: CanvasPoint };
}): void {
  if (!writable.value) return;
  const moved = payload.nodes ?? (payload.node ? [payload.node] : []);
  if (moved.length === 0) return;
  const next = { ...positions.value };
  const dragged = new Set(draggedIds.value);
  for (const n of moved) {
    next[n.id] = { x: Math.round(n.position.x), y: Math.round(n.position.y) };
    dragged.add(n.id);
  }
  positions.value = next;
  draggedIds.value = dragged;
}

/** Deletes whatever is selected: the node, else the edge. */
function deleteSelection(): void {
  if (!writable.value) return;
  rejection.value = '';
  if (props.selectedNodeId) {
    void props.adapter.onSpecOp({ type: 'delete-node', id: props.selectedNodeId });
    emit('select-node', '');
    return;
  }
  if (selectedEdgeId.value) {
    void props.adapter.onSpecOp({ type: 'disconnect', edgeId: selectedEdgeId.value });
    selectedEdgeId.value = '';
  }
}

function onKeydown(ev: KeyboardEvent): void {
  if (ev.key !== 'Delete' && ev.key !== 'Backspace') return;
  if (!props.selectedNodeId && !selectedEdgeId.value) return;
  ev.preventDefault();
  deleteSelection();
}

const canDelete = computed(
  () => writable.value && (!!props.selectedNodeId || !!selectedEdgeId.value),
);

defineExpose({
  relayout,
  pendingLayout,
  positions,
  movedNodeIds,
  // Test seams: the pointer gestures these sit behind cannot be
  // simulated faithfully under happy-dom (zero-size viewport), so the
  // handlers are reachable directly and the DnD flows are tested at
  // this level. See the header comment on the component test.
  onDrop,
  onConnect,
  onNodeDragStop,
  deleteSelection,
  selectedEdgeId,
  rejection,
});
</script>

<template>
  <div
    class="relative h-[480px] w-full overflow-hidden rounded-md border border-border-muted bg-surface-0"
    data-testid="graph-canvas"
    :data-readonly="adapter.readOnly ? 'true' : 'false'"
    :data-node-count="adapter.nodes.length"
    :tabindex="writable ? 0 : undefined"
    @dragover="writable ? onDragOver($event) : undefined"
    @drop="writable ? onDrop($event) : undefined"
    @keydown="writable ? onKeydown($event) : undefined"
  >
    <div
      v-if="adapter.nodes.length === 0"
      class="absolute inset-0 z-10 flex items-center justify-center font-ui text-[12px] text-ink-muted"
      data-testid="canvas-empty"
    >
      {{ writable ? 'Drag a kind from the palette to start.' : 'No nodes to draw yet.' }}
    </div>

    <div
      v-if="notice"
      class="absolute inset-x-2 top-2 z-20 rounded-sm border border-signal-warn bg-surface-1 px-2 py-1 font-ui text-[11px] text-signal-warn"
      data-testid="canvas-notice"
      role="status"
    >
      {{ notice }}
    </div>

    <div
      v-if="writable"
      class="absolute right-2 top-2 z-20 flex items-center gap-2"
    >
      <button
        type="button"
        :disabled="!canDelete"
        class="rounded-sm border border-border-muted bg-surface-1 px-2 py-0.5 font-ui text-[11px] uppercase tracking-[0.14em] text-ink-muted hover:bg-surface-2 disabled:opacity-40"
        data-testid="canvas-delete"
        @click="deleteSelection"
      >
        Delete
      </button>
    </div>

    <div
      v-if="rejection"
      class="absolute inset-x-2 bottom-2 z-20 rounded-sm border border-signal-danger bg-surface-1 px-2 py-1 font-ui text-[11px] text-signal-danger"
      data-testid="canvas-edge-rejected"
      role="alert"
    >
      {{ rejection }}
    </div>

    <VueFlow
      :nodes="flowNodes"
      :edges="flowEdges"
      :nodes-draggable="!adapter.readOnly"
      :nodes-connectable="!adapter.readOnly"
      :elements-selectable="true"
      :fit-view-on-init="fitViewOnInit"
      :min-zoom="0.2"
      :max-zoom="2"
      :delete-key-code="null"
      @node-click="onNodeClick"
      @pane-click="onPaneClick"
      @edge-click="writable ? onEdgeClick($event) : undefined"
      @connect="writable ? onConnect($event) : undefined"
      @node-drag-stop="writable ? onNodeDragStop($event) : undefined"
    >
      <template #node-kenazNode="nodeProps">
        <CanvasNodeCard v-bind="nodeProps" />
      </template>
      <template #node-kenazGroup="nodeProps">
        <CanvasGroupFrame v-bind="nodeProps" />
      </template>
      <Background />
      <Controls :show-interactive="false">
        <template #control-zoom-in>
          <ControlButton
            aria-label="Zoom in"
            title="Zoom in"
            data-testid="canvas-zoom-in"
            @click="zoomIn()"
            >+</ControlButton
          >
        </template>
        <template #control-zoom-out>
          <ControlButton
            aria-label="Zoom out"
            title="Zoom out"
            data-testid="canvas-zoom-out"
            @click="zoomOut()"
            >−</ControlButton
          >
        </template>
        <template #control-fit-view>
          <ControlButton
            aria-label="Fit graph to view"
            title="Fit graph to view"
            data-testid="canvas-fit-view"
            @click="fitView()"
            >⤢</ControlButton
          >
        </template>
      </Controls>
    </VueFlow>
  </div>
</template>
