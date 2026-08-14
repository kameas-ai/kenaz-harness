/**
 * The agentgraph Graph spec, as the canvas edits it
 * (visual-graph-authoring-01PMUX01 WP03/WP04).
 *
 * ONE BUFFER (spec §4, WP04). The YAML text in the editor's textarea is
 * the only state. There is no second in-memory graph model that can
 * drift from it:
 *
 *   textarea text ──parse──▶ Document ──toJS──▶ ParsedGraph ──▶ canvas
 *          ▲                                                       │
 *          └────────── stringify ◀── applyOpToDoc ◀── SpecOp ──────┘
 *
 * Canvas ops are applied to the **parsed Document** and the Document is
 * re-serialised into the same textarea the user types in. A hand edit
 * re-parses and re-renders. There is exactly one dirty flag and one
 * save path because there is exactly one buffer.
 *
 * WHY THE DOCUMENT API AND NOT `YAML.parse` + `YAML.stringify`. A
 * full parse/stringify round-trip drops comments and re-orders nothing
 * but reflows everything — `chat_default.yaml` is 200 lines of which
 * ~120 are load-bearing prose, and a canvas that erased them on the
 * first node drag would make the YAML pane worse, not better.
 * `parseDocument` keeps the CST, so a surgical `addIn` / `deleteIn`
 * touches only the lines it must and every untouched byte survives
 * (pinned by `graphSpec.test.ts`'s comment-preservation test).
 */
import { Document, isMap, isSeq, parseDocument, type YAMLMap, type YAMLSeq } from 'yaml';

import type { CanvasPoint, SpecOp } from './types';

// ── parsed view ───────────────────────────────────────────────────────

export interface SpecPort {
  name: string;
  type: string;
  required?: boolean;
}

/**
 * The `materialized:` block a projected run carries on every node
 * (core/agentgraph/spec.go `NodeMaterialization`, WP05).
 *
 * Only the fields the canvas overlay needs are read. The block also
 * carries the redacted tool/arg/error detail, which stays where it is:
 * the overlay renders a status and a seq, and the trace rows — which
 * `startSeq` joins to — hold everything else.
 *
 * An authored graph never has this block (`omitempty` on both wires),
 * so `materialized` being undefined is the normal case, not a gap.
 */
export interface SpecMaterialization {
  sourceNode?: string;
  instance?: number;
  iteration?: number;
  container?: string;
  /** completed | error | skipped | not_reached | incomplete. */
  status?: string;
  /** EventLog sequence numbers bounding the fire; the trace join key. */
  startSeq?: number;
  endSeq?: number;
}

export interface SpecNode {
  id: string;
  kind: string;
  title?: string;
  attrs: Record<string, unknown>;
  inputs?: SpecPort[];
  outputs?: SpecPort[];
  /** Set only on a materialized run's projection (WP05). */
  materialized?: SpecMaterialization;
}

export interface SpecEndpoint {
  node: string;
  port: string;
}

export interface SpecEdge {
  from: SpecEndpoint;
  to: SpecEndpoint;
}

export interface ParsedGraph {
  id: string;
  name?: string;
  entrypoints: string[];
  nodes: SpecNode[];
  edges: SpecEdge[];
  layout: Record<string, CanvasPoint>;
  /**
   * `spec_provenance` on a materialized run — `library_fallback` when
   * the resolved spec that executed was evicted and the projection fell
   * back to the library file. Absent on everything else.
   */
  specProvenance?: string;
}

export interface ParseResult {
  /** Null when the text does not parse as a graph. */
  graph: ParsedGraph | null;
  /** Null when the text does not parse as YAML at all. */
  doc: Document | null;
  /** Human-readable parse failure, or null. */
  error: string | null;
}

function asRecord(v: unknown): Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : {};
}

function asString(v: unknown): string {
  return typeof v === 'string' ? v : v === undefined || v === null ? '' : String(v);
}

function readPorts(v: unknown): SpecPort[] | undefined {
  if (!Array.isArray(v)) return undefined;
  const out: SpecPort[] = [];
  for (const raw of v) {
    const p = asRecord(raw);
    const name = asString(p.name);
    if (!name) continue;
    const port: SpecPort = { name, type: asString(p.type) || 'any' };
    if (p.required === true) port.required = true;
    out.push(port);
  }
  return out.length > 0 ? out : undefined;
}

function asInt(v: unknown): number | undefined {
  const n = typeof v === 'number' ? v : Number(v);
  return Number.isFinite(n) ? n : undefined;
}

/**
 * Reads a node's `materialized:` block. Absent, non-mapping, or empty
 * blocks all yield undefined — an authored graph must be indistinguishable
 * from one whose projection carried nothing.
 */
function readMaterialization(v: unknown): SpecMaterialization | undefined {
  if (v === null || typeof v !== 'object' || Array.isArray(v)) return undefined;
  const m = v as Record<string, unknown>;
  const out: SpecMaterialization = {};
  const sourceNode = asString(m.source_node);
  if (sourceNode) out.sourceNode = sourceNode;
  const container = asString(m.container);
  if (container) out.container = container;
  const status = asString(m.status);
  if (status) out.status = status;
  const instance = asInt(m.instance);
  if (instance !== undefined) out.instance = instance;
  const iteration = asInt(m.iteration);
  if (iteration !== undefined) out.iteration = iteration;
  const startSeq = asInt(m.start_seq);
  if (startSeq !== undefined) out.startSeq = startSeq;
  const endSeq = asInt(m.end_seq);
  if (endSeq !== undefined) out.endSeq = endSeq;
  return Object.keys(out).length > 0 ? out : undefined;
}

function readEndpoint(v: unknown): SpecEndpoint | null {
  const e = asRecord(v);
  const node = asString(e.node);
  if (!node) return null;
  return { node, port: asString(e.port) };
}

/** Turns a plain JS object (from `doc.toJS()`) into the parsed view. */
export function graphFromPlain(plain: unknown): ParsedGraph | null {
  if (plain === null || typeof plain !== 'object' || Array.isArray(plain)) return null;
  const g = plain as Record<string, unknown>;

  const nodes: SpecNode[] = [];
  if (Array.isArray(g.nodes)) {
    for (const raw of g.nodes) {
      const n = asRecord(raw);
      const id = asString(n.id);
      if (!id) continue;
      const node: SpecNode = {
        id,
        kind: asString(n.kind),
        attrs: asRecord(n.attrs),
      };
      const title = asString(n.title);
      if (title) node.title = title;
      const inputs = readPorts(n.inputs);
      if (inputs) node.inputs = inputs;
      const outputs = readPorts(n.outputs);
      if (outputs) node.outputs = outputs;
      const materialized = readMaterialization(n.materialized);
      if (materialized) node.materialized = materialized;
      nodes.push(node);
    }
  }

  const edges: SpecEdge[] = [];
  if (Array.isArray(g.edges)) {
    for (const raw of g.edges) {
      const e = asRecord(raw);
      const from = readEndpoint(e.from);
      const to = readEndpoint(e.to);
      if (!from || !to) continue;
      edges.push({ from, to });
    }
  }

  const layout: Record<string, CanvasPoint> = {};
  for (const [id, raw] of Object.entries(asRecord(g.layout))) {
    const p = asRecord(raw);
    const x = Number(p.x);
    const y = Number(p.y);
    if (Number.isFinite(x) && Number.isFinite(y)) layout[id] = { x, y };
  }

  const entrypoints = Array.isArray(g.entrypoints)
    ? g.entrypoints.map(asString).filter(Boolean)
    : [];

  const out: ParsedGraph = {
    id: asString(g.id),
    entrypoints,
    nodes,
    edges,
    layout,
  };
  const name = asString(g.name);
  if (name) out.name = name;
  const provenance = asString(g.spec_provenance);
  if (provenance) out.specProvenance = provenance;
  return out;
}

/** Parses editor text into a Document + the canvas's view of it. */
export function parseGraphText(text: string): ParseResult {
  if (text.trim() === '') {
    return { graph: null, doc: null, error: 'yaml is empty' };
  }
  let doc: Document;
  try {
    doc = parseDocument(text);
  } catch (err) {
    return { graph: null, doc: null, error: err instanceof Error ? err.message : String(err) };
  }
  if (doc.errors.length > 0) {
    return { graph: null, doc: null, error: doc.errors[0].message };
  }
  let plain: unknown;
  try {
    plain = doc.toJS();
  } catch (err) {
    return { graph: null, doc, error: err instanceof Error ? err.message : String(err) };
  }
  const graph = graphFromPlain(plain);
  if (!graph) {
    return { graph: null, doc, error: 'yaml does not describe a graph mapping' };
  }
  return { graph, doc, error: null };
}

/**
 * Serialises a Document back into editor text.
 *
 * `lineWidth: 0` disables re-wrapping, and it is a diffability setting
 * rather than a cosmetic one: `chat_default.yaml` carries multi-line
 * prose in `description:` and long `system_prompt:` scalars, and the
 * library's default 80-column wrap would reflow all of it the first
 * time the author dragged a node.
 *
 * Flow-collection padding is left at the library default (`{ a: b }`,
 * `[ a ]`) because that is what the shipped library YAML already uses —
 * `graphSpecOps.test.ts` pins that both bundled graphs survive a
 * parse→serialize round trip byte-for-byte.
 */
export function serializeDoc(doc: Document): string {
  return doc.toString({ lineWidth: 0 });
}

// ── op application (the canvas path) ──────────────────────────────────

export interface ApplyOpContext {
  /** Manifest-derived default attrs for a newly dropped kind. */
  attrsFor?: (kind: string) => Record<string, unknown>;
  /** Existing ids, so a generated id never collides. */
  existingIds?: string[];
}

/**
 * Allocates `<kind>_<n>` — the same scheme the pre-canvas
 * `appendStubNode` used, so ids the editor produces do not change shape
 * just because the drop target moved from the textarea to the canvas.
 */
export function allocateNodeId(kind: string, existing: Iterable<string>): string {
  const taken = new Set(existing);
  let i = 1;
  let candidate = `${kind}_${i}`;
  while (taken.has(candidate)) {
    i += 1;
    candidate = `${kind}_${i}`;
  }
  return candidate;
}

function ensureSeq(doc: Document, key: string): YAMLSeq {
  const existing = doc.getIn([key]);
  if (isSeq(existing)) return existing;
  const seq = doc.createNode([]) as YAMLSeq;
  doc.setIn([key], seq);
  return seq;
}

function ensureMap(doc: Document, key: string): YAMLMap {
  const existing = doc.getIn([key]);
  if (isMap(existing)) return existing;
  const map = doc.createNode({}) as YAMLMap;
  doc.setIn([key], map);
  return map;
}

function nodeIdAt(seq: YAMLSeq, i: number): string {
  const item = seq.get(i);
  if (!isMap(item)) return '';
  return asString(item.get('id'));
}

function edgeEndpoints(item: unknown): { from: SpecEndpoint; to: SpecEndpoint } | null {
  if (!isMap(item)) return null;
  const raw = item.toJSON() as Record<string, unknown>;
  const from = readEndpoint(raw.from);
  const to = readEndpoint(raw.to);
  if (!from || !to) return null;
  return { from, to };
}

/** Canonical edge id — must match `canvasEdgeId` in types.ts. */
function edgeKey(e: { from: SpecEndpoint; to: SpecEndpoint }): string {
  return `${e.from.node}:${e.from.port}→${e.to.node}:${e.to.port}`;
}

/**
 * Applies one canvas op to the Document, in place.
 *
 * Returns the node id an `add-node` allocated (so the caller can select
 * it), or the empty string for every other op.
 */
export function applyOpToDoc(doc: Document, op: SpecOp, ctx: ApplyOpContext = {}): string {
  switch (op.type) {
    case 'add-node': {
      const nodes = ensureSeq(doc, 'nodes');
      const existing = new Set<string>(ctx.existingIds ?? []);
      for (let i = 0; i < nodes.items.length; i += 1) {
        const id = nodeIdAt(nodes, i);
        if (id) existing.add(id);
      }
      const id = op.id && !existing.has(op.id) ? op.id : allocateNodeId(op.kind, existing);
      const attrs = ctx.attrsFor ? ctx.attrsFor(op.kind) : {};
      nodes.add(doc.createNode({ id, kind: op.kind, attrs }));
      setLayout(doc, id, op.position);
      // A graph with no entrypoint at all cannot validate; the first
      // node dropped onto an empty canvas becomes it. Later drops never
      // touch `entrypoints` — that is an authoring decision, not a
      // side-effect of placing a box.
      const entry = doc.getIn(['entrypoints']);
      const hasEntry = isSeq(entry) && entry.items.length > 0;
      if (!hasEntry && nodes.items.length === 1) {
        doc.setIn(['entrypoints'], doc.createNode([id]));
      }
      return id;
    }

    case 'delete-node': {
      const nodes = doc.getIn(['nodes']);
      if (isSeq(nodes)) {
        for (let i = nodes.items.length - 1; i >= 0; i -= 1) {
          if (nodeIdAt(nodes, i) === op.id) nodes.delete(i);
        }
        if (nodes.items.length === 0) doc.deleteIn(['nodes']);
      }
      const edges = doc.getIn(['edges']);
      if (isSeq(edges)) {
        for (let i = edges.items.length - 1; i >= 0; i -= 1) {
          const ep = edgeEndpoints(edges.get(i));
          if (!ep) continue;
          if (ep.from.node === op.id || ep.to.node === op.id) edges.delete(i);
        }
        if (edges.items.length === 0) doc.deleteIn(['edges']);
      }
      const entry = doc.getIn(['entrypoints']);
      if (isSeq(entry)) {
        for (let i = entry.items.length - 1; i >= 0; i -= 1) {
          if (asString(entry.toJSON()[i]) === op.id) entry.delete(i);
        }
      }
      deleteLayout(doc, op.id);
      return '';
    }

    case 'set-attrs': {
      const nodes = doc.getIn(['nodes']);
      if (!isSeq(nodes)) return '';
      for (let i = 0; i < nodes.items.length; i += 1) {
        if (nodeIdAt(nodes, i) !== op.id) continue;
        if (Object.keys(op.attrs).length === 0) {
          doc.deleteIn(['nodes', i, 'attrs']);
        } else {
          doc.setIn(['nodes', i, 'attrs'], doc.createNode(op.attrs));
        }
        break;
      }
      return '';
    }

    case 'connect': {
      const edges = ensureSeq(doc, 'edges');
      const key = edgeKey({
        from: { node: op.edge.source, port: op.edge.sourcePort },
        to: { node: op.edge.target, port: op.edge.targetPort },
      });
      for (const item of edges.items) {
        const ep = edgeEndpoints(item);
        if (ep && edgeKey(ep) === key) return '';
      }
      edges.add(
        doc.createNode({
          from: { node: op.edge.source, port: op.edge.sourcePort },
          to: { node: op.edge.target, port: op.edge.targetPort },
        }),
      );
      return '';
    }

    case 'disconnect': {
      const edges = doc.getIn(['edges']);
      if (isSeq(edges)) {
        for (let i = edges.items.length - 1; i >= 0; i -= 1) {
          const ep = edgeEndpoints(edges.get(i));
          if (ep && edgeKey(ep) === op.edgeId) edges.delete(i);
        }
        if (edges.items.length === 0) doc.deleteIn(['edges']);
      }
      return '';
    }

    case 'move-nodes': {
      for (const id of Object.keys(op.positions).sort()) {
        setLayout(doc, id, op.positions[id]);
      }
      return '';
    }

    default:
      return '';
  }
}

function setLayout(doc: Document, id: string, p: CanvasPoint): void {
  // Integers only (plan risk "Layout churn in diffs"): a sub-pixel drag
  // must not produce a YAML diff.
  const x = Math.round(p.x);
  const y = Math.round(p.y);
  ensureMap(doc, 'layout');
  doc.setIn(['layout', id], doc.createNode({ x, y }));
}

function deleteLayout(doc: Document, id: string): void {
  const layout = doc.getIn(['layout']);
  if (!isMap(layout)) return;
  doc.deleteIn(['layout', id]);
  if (layout.items.length === 0) doc.deleteIn(['layout']);
}

/**
 * Prunes layout entries whose node no longer exists. `Graph.Layout`
 * tolerates stale keys by design (spec.go: "a renamed node must not
 * brick its graph"), so this is only ever called on an explicit save —
 * never on a parse, which would rewrite the buffer under the cursor.
 */
export function pruneStaleLayout(doc: Document): void {
  const layout = doc.getIn(['layout']);
  if (!isMap(layout)) return;
  const nodes = doc.getIn(['nodes']);
  const live = new Set<string>();
  if (isSeq(nodes)) {
    for (let i = 0; i < nodes.items.length; i += 1) {
      const id = nodeIdAt(nodes, i);
      if (id) live.add(id);
    }
  }
  for (const key of Object.keys((layout.toJSON() ?? {}) as Record<string, unknown>)) {
    if (!live.has(key)) doc.deleteIn(['layout', key]);
  }
  if (layout.items.length === 0) doc.deleteIn(['layout']);
}
