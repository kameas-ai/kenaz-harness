/**
 * CanvasAdapter — the one interface between `GraphCanvas.vue` and a spec
 * family (mission visual-graph-authoring-01PMUX01, FR-001 / WP02).
 *
 * The canvas is a *view*. It owns pixels, pointer events, and nothing
 * else: it never imports a spec type, never mutates a spec, and never
 * knows what YAML is. Everything family-specific arrives through this
 * interface — the node/edge view-model, the palette, the legality
 * check, and the op sink.
 *
 * ─────────────────────────────────────────────────────────────────────
 * PAPER CHECK against BOTH consumers (plan.md risk: "Two spec families,
 * one canvas — the adapter interface must be designed in WP02 against
 * BOTH consumers on paper, or WP06 will force a rewrite").
 *
 * Consumer A — `core/agentgraph` Graph (spec.go):
 *   Graph{ SpecVersion, ID, Entrypoints []string, Nodes []Node,
 *          Edges []Edge, Layout map[string]NodeLayout }
 *   Node{ ID, Kind, Title, Inputs []Port, Outputs []Port, Attrs }
 *   Port{ Name, Type PortType, Required }
 *   Edge{ From EndpointRef{Node,Port}, To EndpointRef{Node,Port} }
 *   Loop/Retry containment: `attrs.body: [nodeID…]` — a MEMBERSHIP list,
 *   not a nesting of the node array.
 *
 * Consumer B — `core/workflows` Workflow (types.go):
 *   Workflow{ Name, Inputs []Input, Steps []Step }
 *   Step{ Name, Kind StepKind, InputsFrom []string, …flat per-kind
 *         fields (Cmd, URL, ToolName, If/ThenStep/ElseStep, …) }
 *   No ports. No layout block. No containment.
 *
 * Every field below is justified against both, and the mismatches are
 * resolved in the interface rather than deferred to WP06:
 *
 *  1. NODE IDENTITY is a plain string. A `Graph.Node.ID` and a
 *     `Step.Name` are both unique-within-spec strings, so `CanvasNode.id`
 *     carries either with no wrapping. ✔ both
 *
 *  2. PORTS are the real asymmetry. agentgraph edges are
 *     (node, port) → (node, port) with a nominal type system; a workflow
 *     edge is a bare dependency (`inputs_from: [stepName]`). Rather than
 *     make ports optional — which would put `if (port)` branches through
 *     every renderer and every hit-test — the interface REQUIRES ports
 *     and lets the workflows adapter synthesise the single implicit pair
 *     ({name:'in'} / {name:'out'}, type `any`). `DEFAULT_PORT_IN` /
 *     `DEFAULT_PORT_OUT` below are that contract, so WP06 does not have
 *     to invent a convention. The canvas draws one handle per port; a
 *     workflow node draws exactly one on each side, which is what it
 *     should look like anyway. ✔ both
 *
 *  3. EDGE LEGALITY is `onCheckEdge`, async, adapter-owned. agentgraph
 *     answers it with the `Graph_CheckEdge` RPC (WP03 — one rule source,
 *     the validator); workflows answers it locally (a dependency edge is
 *     legal iff it introduces no cycle and the target step exists). The
 *     canvas only ever awaits `{ok, reason}` and renders `reason`
 *     verbatim, so neither family's rules leak into the component. The
 *     signature deliberately takes a `CanvasEdgeRequest` (no id) since
 *     the edge does not exist yet at check time. ✔ both
 *
 *  4. MUTATION is `onSpecOp` and the op union is intentionally about
 *     USER INTENT, not spec shape: add-node / delete-node / connect /
 *     disconnect / move-nodes. Each maps cleanly onto both families:
 *       add-node    → append Graph.Nodes entry | append Workflow.Steps
 *       delete-node → drop node + incident edges | drop step + prune
 *                     every `inputs_from` mention
 *       connect     → append Graph.Edges | append target.inputs_from
 *       disconnect  → drop Graph.Edges entry | remove from inputs_from
 *       move-nodes  → write Graph.Layout | (workflows: no-op, see 6)
 *     No op mentions ports-as-such except connect, which carries them
 *     and which workflows fills with the default pair. ✔ both
 *
 *  5. CONTAINMENT is display-only and expressed as a flat `group` field
 *     on the child plus `isGroup` on the container — NOT as a nested
 *     node tree. This is forced by agentgraph: `attrs.body` is a
 *     membership list and a node can be listed in a body while still
 *     living in the flat `nodes:` array, so a nested view-model would
 *     invent a second nesting model the spec does not have (plan risk
 *     "Loop bodies"). Workflows has no containment at all and simply
 *     leaves both fields undefined. ✔ both
 *
 *  6. LAYOUT: agentgraph has `Graph.Layout` (WP01); workflows has no
 *     such field and adding one is out of scope for this mission. The
 *     interface therefore carries `persistsLayout: boolean`. When false
 *     the canvas still lets nodes be dragged (positions live in
 *     component state) but never emits `move-nodes` on save, and always
 *     auto-lays out on load. This is the one place where the two
 *     families genuinely differ in capability, and it is one boolean
 *     rather than a workflow-shaped hole in the component. ✔ both
 *
 *  7. STATUS OVERLAY (WP05) rides on `CanvasNode.status` +
 *     `statusDetail`. agentgraph fills it from the materialization's
 *     `NodeMaterialization.Status` / `start_seq`; workflows can fill it
 *     from its own run model later. The canvas renders the enum and
 *     emits `node-status-click`; it has no overlay logic of its own.
 *     Declared now, populated in WP05. ✔ both
 *
 *  8. READ-ONLY is `readOnly: boolean` and the component uses it
 *     STRUCTURALLY (handlers are not registered, palette is not
 *     rendered) rather than as a disabled attribute — this is what
 *     WP05 needs to close the N3 hazard. ✔ both
 *
 * Nothing in this file imports from `@/lib/types`' graph shapes or from
 * a workflow type. If a future edit needs one, the adapter boundary has
 * been broken.
 */

/** Canvas coordinate. Integers once persisted (WP03 rounds on save). */
export interface CanvasPoint {
  x: number;
  y: number;
}

/**
 * Kind categories the canvas knows how to colour. These are the three
 * the node manifest catalog emits (`compute` | `control` | `state`);
 * anything else renders with the neutral `other` treatment rather than
 * failing, so a new category in the catalog degrades to grey instead of
 * breaking the canvas.
 */
export type CanvasCategory = 'compute' | 'control' | 'state' | 'other';

export const CANVAS_CATEGORIES: readonly CanvasCategory[] = [
  'compute',
  'control',
  'state',
  'other',
] as const;

/** Normalises an arbitrary catalog category string onto the known set. */
export function toCanvasCategory(raw: string | undefined | null): CanvasCategory {
  switch (raw) {
    case 'compute':
    case 'control':
    case 'state':
      return raw;
    default:
      return 'other';
  }
}

/**
 * Per-node run status for the WP05 overlay. The values are the
 * materializer's `NodeMaterialization.Status` vocabulary plus the two
 * live-run states the run-status poll can report.
 */
export type CanvasNodeStatus =
  | 'pending'
  | 'running'
  | 'complete'
  | 'skipped'
  | 'failed'
  | 'incomplete'
  | 'not_reached';

/** One input or output handle on a node. */
export interface CanvasPort {
  name: string;
  /** Adapter-defined nominal type; `any` for port-less families. */
  type: string;
  required?: boolean;
  description?: string;
}

/**
 * The single implicit port pair a family without ports (workflows)
 * synthesises. Named here so WP06 inherits the convention rather than
 * inventing one.
 */
export const DEFAULT_PORT_IN: CanvasPort = { name: 'in', type: 'any' };
export const DEFAULT_PORT_OUT: CanvasPort = { name: 'out', type: 'any' };

/** One node as the canvas sees it. Purely a view-model. */
export interface CanvasNode {
  id: string;
  /** Family-specific kind id — `NodeKind` or `StepKind`. */
  kind: string;
  /** Human label; falls back to the id when the spec has no title. */
  label: string;
  category: CanvasCategory;
  inputs: CanvasPort[];
  outputs: CanvasPort[];
  /** Container node id when this node is *displayed inside* a frame. */
  group?: string;
  /** True when this node frames a group (loop/retry body container). */
  isGroup?: boolean;
  /** Authored position; undefined means "auto-layout decides". */
  layout?: CanvasPoint;
  /** Run overlay (WP05). Undefined on an authoring canvas. */
  status?: CanvasNodeStatus;
  /** Opaque per-node overlay detail (seq numbers, counters) for WP05. */
  statusDetail?: Record<string, unknown>;
}

/** One edge as the canvas sees it. */
export interface CanvasEdge {
  /** Stable within a render; adapters derive it from the endpoints. */
  id: string;
  source: string;
  sourcePort: string;
  target: string;
  targetPort: string;
  /**
   * `route` marks an edge the runtime uses to choose a successor
   * (a decision's true/false, a router choice) so the canvas can style
   * it distinctly from a plain data edge. `dependency` is the
   * port-less workflows edge.
   */
  kind?: 'data' | 'route' | 'dependency';
}

/** An edge being drawn — no id yet, because it does not exist. */
export type CanvasEdgeRequest = Omit<CanvasEdge, 'id' | 'kind'>;

/** One palette row. */
export interface PaletteItem {
  kind: string;
  label: string;
  category: CanvasCategory;
  description?: string;
  /** Optional grouping key (agentgraph archetype; workflow family). */
  group?: string;
}

/** The answer to "may this edge be drawn?" — `reason` is shown verbatim. */
export interface EdgeCheckResult {
  ok: boolean;
  reason?: string;
}

/**
 * A user intent the canvas emits and the adapter applies to its spec.
 * See paper-check note 4 for the per-family mapping.
 */
export type SpecOp =
  | {
      type: 'add-node';
      kind: string;
      position: CanvasPoint;
      /** Adapter allocates the id when absent. */
      id?: string;
    }
  | { type: 'delete-node'; id: string }
  | { type: 'connect'; edge: CanvasEdgeRequest }
  | { type: 'disconnect'; edgeId: string }
  | {
      /**
       * Persist positions. Emitted ONLY on save (plan risk "Layout churn
       * in diffs") and only when `persistsLayout` is true; live dragging
       * stays in component state.
       */
      type: 'move-nodes';
      positions: Record<string, CanvasPoint>;
    };

/**
 * The contract `GraphCanvas.vue` consumes. Plain data + two callbacks —
 * deliberately not a class, so a consumer can build it inside a
 * `computed()` and Vue's reactivity handles re-render.
 */
export interface CanvasAdapter {
  nodes: CanvasNode[];
  edges: CanvasEdge[];
  paletteItems: PaletteItem[];
  /** Asked before an edge is committed. Rejection shows `reason`. */
  onCheckEdge: (edge: CanvasEdgeRequest) => Promise<EdgeCheckResult>;
  /** Applies a user intent to the underlying spec. */
  onSpecOp: (op: SpecOp) => void | Promise<void>;
  /** Structural read-only: no mutation handlers are registered at all. */
  readOnly: boolean;
  /** False for families with no layout block (workflows). */
  persistsLayout: boolean;
}

/** Canonical edge id from its four endpoints. Stable + collision-free. */
export function canvasEdgeId(e: CanvasEdgeRequest): string {
  return `${e.source}:${e.sourcePort}→${e.target}:${e.targetPort}`;
}
