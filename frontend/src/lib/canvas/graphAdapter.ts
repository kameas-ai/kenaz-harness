/**
 * The agentgraph `CanvasAdapter` (visual-graph-authoring-01PMUX01
 * WP02/WP03) — one of the two adapters the paper check in `types.ts`
 * was designed against. It is a pure function of (parsed spec, manifest
 * catalog, callbacks) so every rule in it is testable without mounting
 * a component or touching a DOM.
 */
import type {
  NodeManifestDetail,
  NodeManifestSummary,
} from '@/lib/types';

import type { ParsedGraph, SpecMaterialization, SpecNode } from './graphSpec';
import {
  DEFAULT_PORT_IN,
  DEFAULT_PORT_OUT,
  canvasEdgeId,
  toCanvasCategory,
  type CanvasAdapter,
  type CanvasEdge,
  type CanvasEdgeRequest,
  type CanvasNode,
  type CanvasNodeStatus,
  type CanvasPort,
  type EdgeCheckResult,
  type PaletteItem,
  type SpecOp,
} from './types';

/** Kinds whose `attrs.body` lists the nodes displayed inside them. */
const BODY_CONTAINER_KINDS = new Set(['loop', 'retry']);

export interface NodeStatusOverlay {
  status: CanvasNodeStatus;
  detail?: Record<string, unknown>;
}

export interface GraphAdapterInput {
  /** Null when the buffer does not parse — the caller keeps last-good. */
  graph: ParsedGraph | null;
  /** Catalog rows (palette source + per-kind category). */
  manifests: NodeManifestSummary[];
  /** Resolved manifests by kind id; supplies ports + attr defaults. */
  details?: Record<string, NodeManifestDetail | undefined>;
  readOnly: boolean;
  checkEdge: (edge: CanvasEdgeRequest) => Promise<EdgeCheckResult>;
  applyOp: (op: SpecOp) => void | Promise<void>;
  /** Run overlay, keyed by node id. Populated in WP05; empty until then. */
  statuses?: Record<string, NodeStatusOverlay | undefined>;
}

function portsFrom(
  declared: SpecNode['inputs'],
  manifestPorts: Array<{ name: string; type: string }> | undefined,
  fallback: CanvasPort,
): CanvasPort[] {
  // Explicit ports in the spec always win — same precedence the Go
  // validator's `portSurface` applies (explicit, else kind defaults).
  if (declared && declared.length > 0) {
    return declared.map((p) => ({
      name: p.name,
      type: p.type || 'any',
      ...(p.required ? { required: true } : {}),
    }));
  }
  if (manifestPorts && manifestPorts.length > 0) {
    return manifestPorts.map((p) => ({ name: p.name, type: p.type || 'any' }));
  }
  return [fallback];
}

/**
 * Manifest-derived default attrs for a freshly dropped kind (WP03).
 * `defaults` is the resolved manifest's own map; the per-attr `default`
 * fills any gap, and required attrs with no default land as empty
 * strings so the attribute editor has a row to render rather than a
 * missing key.
 */
export function defaultAttrsForKind(
  detail: NodeManifestDetail | undefined,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  if (!detail) return out;
  for (const attr of detail.attrs ?? []) {
    if (attr.default !== undefined) {
      out[attr.name] = attr.default;
    } else if (attr.required) {
      out[attr.name] = attr.type === 'int' || attr.type === 'float' ? 0 : '';
    }
  }
  for (const [k, v] of Object.entries(detail.defaults ?? {})) {
    out[k] = v;
  }
  return out;
}

// ── run overlay (WP05) ────────────────────────────────────────────────

/**
 * `Graph.SpecProvenance` value marking a projection that fell back to
 * the library file because the resolved spec the run executed had been
 * evicted (core/agentgraph/spec.go). The topology shown may differ from
 * the one that ran, which is why it is badged rather than served silently.
 */
export const SPEC_PROVENANCE_LIBRARY_FALLBACK = 'library_fallback';

/**
 * Maps the materializer's status vocabulary onto the canvas enum.
 *
 * The two vocabularies deliberately do not match one-for-one: the
 * materializer describes a RECORD ("completed", "error"), the canvas
 * describes a CELL on an Airflow-style graph ("complete", "failed"), and
 * `running` exists only on the canvas because a recorded fire is never
 * running by the time it is a record.
 *
 * `live` is the whole bridge between the two. A node whose fire started
 * and whose log has no matching complete or error materializes as
 * `incomplete` — which on a finished run means "the run was cancelled or
 * crashed mid-node", and on a run that is STILL GOING means "this node is
 * executing right now". Same record, and the only thing that distinguishes
 * them is whether the run has ended, so that is the one bit passed in.
 */
export function toCanvasStatus(
  raw: string | undefined,
  live = false,
): CanvasNodeStatus | undefined {
  switch (raw) {
    case 'completed':
      return 'complete';
    case 'error':
      return 'failed';
    case 'skipped':
      return 'skipped';
    case 'not_reached':
      return live ? 'pending' : 'not_reached';
    case 'incomplete':
      return live ? 'running' : 'incomplete';
    default:
      return undefined;
  }
}

/** The overlay detail the canvas hands back on a status click. */
function statusDetail(m: SpecMaterialization): Record<string, unknown> {
  const detail: Record<string, unknown> = {};
  if (m.startSeq !== undefined) detail.startSeq = m.startSeq;
  if (m.endSeq !== undefined) detail.endSeq = m.endSeq;
  if (m.sourceNode) detail.sourceNode = m.sourceNode;
  if (m.instance !== undefined) detail.instance = m.instance;
  if (m.iteration !== undefined) detail.iteration = m.iteration;
  if (m.container) detail.container = m.container;
  return detail;
}

/**
 * Per-node run overlay read out of a materialized projection.
 *
 * This is a pure function of the SAME parse the canvas renders from —
 * there is no second fetch and no second model of the run. The statuses
 * ride inside the projected graph's own `materialized:` blocks, so
 * "what the canvas draws" and "what the overlay says" cannot disagree.
 */
export function materializationStatuses(
  graph: ParsedGraph | null,
  opts: { live?: boolean } = {},
): Record<string, NodeStatusOverlay> {
  const out: Record<string, NodeStatusOverlay> = {};
  if (!graph) return out;
  for (const n of graph.nodes) {
    const m = n.materialized;
    if (!m) continue;
    const status = toCanvasStatus(m.status, opts.live === true);
    if (!status) continue;
    const overlay: NodeStatusOverlay = { status };
    const detail = statusDetail(m);
    if (Object.keys(detail).length > 0) overlay.detail = detail;
    out[n.id] = overlay;
  }
  return out;
}

/** Node ids listed in some container's `attrs.body`, → container id. */
function containerMembership(graph: ParsedGraph): Map<string, string> {
  const out = new Map<string, string>();
  const known = new Set(graph.nodes.map((n) => n.id));
  for (const n of graph.nodes) {
    if (!BODY_CONTAINER_KINDS.has(n.kind)) continue;
    const body = n.attrs?.body;
    if (!Array.isArray(body)) continue;
    for (const raw of body) {
      const id = typeof raw === 'string' ? raw : '';
      // A body entry naming a node that does not exist is a validator
      // error, not a canvas crash — render membership only for real
      // nodes and let the validation pane say the rest.
      if (id && known.has(id) && id !== n.id && !out.has(id)) out.set(id, n.id);
    }
  }
  return out;
}

/**
 * Ports a runtime uses to CHOOSE a successor rather than to pass data.
 * Styling only — the canvas draws these differently; nothing depends on
 * the classification being exhaustive.
 */
function isRoutePort(node: SpecNode | undefined, port: string): boolean {
  if (!node) return false;
  if (node.kind === 'decision') return port === 'true' || port === 'false';
  if (node.kind === 'router') {
    const choices = node.attrs?.choices;
    if (choices && typeof choices === 'object' && !Array.isArray(choices)) {
      return Object.prototype.hasOwnProperty.call(choices, port);
    }
  }
  return false;
}

export function buildGraphAdapter(input: GraphAdapterInput): CanvasAdapter {
  const graph = input.graph;
  const byKind = new Map<string, NodeManifestSummary>();
  for (const m of input.manifests) byKind.set(m.id, m);

  const nodes: CanvasNode[] = [];
  const edges: CanvasEdge[] = [];

  if (graph) {
    const membership = containerMembership(graph);
    const containerIds = new Set(membership.values());
    const specByID = new Map(graph.nodes.map((n) => [n.id, n]));

    for (const n of graph.nodes) {
      const summary = byKind.get(n.kind);
      const detail = input.details?.[n.kind];
      const overlay = input.statuses?.[n.id];
      const node: CanvasNode = {
        id: n.id,
        kind: n.kind,
        label: n.title || n.id,
        category: toCanvasCategory(summary?.category),
        inputs: portsFrom(n.inputs, detail?.ports?.inputs, DEFAULT_PORT_IN),
        outputs: portsFrom(n.outputs, detail?.ports?.outputs, DEFAULT_PORT_OUT),
      };
      const group = membership.get(n.id);
      if (group) node.group = group;
      if (containerIds.has(n.id)) node.isGroup = true;
      const authored = graph.layout[n.id];
      if (authored) node.layout = { x: authored.x, y: authored.y };
      if (overlay) {
        node.status = overlay.status;
        if (overlay.detail) node.statusDetail = overlay.detail;
      }
      nodes.push(node);
    }

    for (const e of graph.edges) {
      const req: CanvasEdgeRequest = {
        source: e.from.node,
        sourcePort: e.from.port,
        target: e.to.node,
        targetPort: e.to.port,
      };
      edges.push({
        ...req,
        id: canvasEdgeId(req),
        kind: isRoutePort(specByID.get(e.from.node), e.from.port) ? 'route' : 'data',
      });
    }
  }

  const paletteItems: PaletteItem[] = input.manifests
    .filter((m) => m.callable)
    .map((m) => {
      const item: PaletteItem = {
        kind: m.id,
        label: m.displayName || m.id,
        category: toCanvasCategory(m.category),
      };
      if (m.description) item.description = m.description;
      const group = m.archetype || m.extends;
      if (group) item.group = group;
      return item;
    })
    .sort((a, b) => a.kind.localeCompare(b.kind));

  return {
    nodes,
    edges,
    paletteItems,
    onCheckEdge: input.checkEdge,
    // Structural read-only (paper-check note 8): a read-only adapter has
    // no mutation path at all, rather than a handler that checks a flag.
    onSpecOp: input.readOnly ? () => undefined : input.applyOp,
    readOnly: input.readOnly,
    persistsLayout: true,
  };
}
