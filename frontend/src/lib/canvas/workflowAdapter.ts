/**
 * The workflows `CanvasAdapter` — Consumer B of the paper check in
 * `types.ts` (visual-graph-authoring-01PMUX01 WP06, FR-006).
 *
 * `core/workflows` is a separate DAG runtime with its own engine, Run
 * model and scheduler. This mission converges its EDITING SURFACE onto
 * the shared canvas and nothing else: the runtime convergence is the
 * spec's explicit non-goal and a successor mission.
 *
 * Like `graphAdapter.ts` this is a pure function of (workflow,
 * callbacks) — every rule below is testable without mounting a
 * component. The op appliers are pure too: they return a NEW workflow
 * rather than mutating, so the editor holds one object and there is no
 * second model to drift.
 *
 * ── WHERE THIS FOLLOWS THE PAPER CHECK, POINT BY POINT ───────────────
 *
 *  2. PORTS. Workflows has none — a dependency is a bare step name in
 *     `inputs_from`. So every node synthesises the single implicit pair
 *     `DEFAULT_PORT_IN` / `DEFAULT_PORT_OUT`, exactly the convention
 *     `types.ts` named so WP06 would not invent one. One handle on each
 *     side, which is what a dependency graph should look like anyway.
 *
 *  3. EDGE LEGALITY is answered LOCALLY, not by an RPC. agentgraph has
 *     `Graph_CheckEdge` because its rules (port types, decision routing)
 *     live in the Go validator and must not be forked. Workflows has no
 *     equivalent RPC and — deliberately — none is added here: see
 *     `checkWorkflowEdge` for which Go rule each check corresponds to
 *     and why the set is closed.
 *
 *  4. MUTATION maps as the paper check said it would:
 *       add-node    → append Workflow.steps
 *       delete-node → drop the step AND prune every `inputs_from` mention
 *       connect     → append to target.inputs_from
 *       disconnect  → remove from target.inputs_from
 *       set-attrs   → the step's flat per-kind fields (incl. rename)
 *       move-nodes  → never emitted; see 6
 *
 *  5. CONTAINMENT: workflows has none. `group` / `isGroup` stay unset.
 *
 *  6. LAYOUT: `Workflow` has no layout block and adding one is out of
 *     scope, so `persistsLayout` is FALSE. Nodes stay draggable — the
 *     positions live in canvas component state — but nothing is ever
 *     written back and every load auto-lays out.
 *
 *  7. STATUS: workflows runs report per-step status through
 *     `workflowRunsStore`, on a different surface. Nothing here fills
 *     `status`; the field stays undefined on an authoring canvas.
 */
import type { WorkflowsStep, WorkflowsWorkflow } from '@/lib/workflowsClient';

import {
  DEFAULT_PORT_IN,
  DEFAULT_PORT_OUT,
  canvasEdgeId,
  type CanvasAdapter,
  type CanvasCategory,
  type CanvasEdge,
  type CanvasEdgeRequest,
  type CanvasNode,
  type EdgeCheckResult,
  type SpecOp,
} from './types';

// ── the step-kind catalog ─────────────────────────────────────────────

/**
 * Kind metadata for the palette.
 *
 * agentgraph gets this from the Go manifest catalog over an RPC.
 * Workflows has NO equivalent: `core/workflows` exposes `AllStepKinds()`
 * as a bare enum with no display name, description or category, and
 * nothing surfaces it over the bridge. So the table lives here, and the
 * enum-parity test in `workflowAdapter.test.ts` reads the Go source to
 * pin that it stays complete — the pre-canvas palette silently omitted
 * `mcp_call` for exactly the want of such a test.
 *
 * The categories reuse the canvas's three (`compute` / `control` /
 * `state`) rather than inventing a workflow-only vocabulary: they mean
 * the same things here — do work, choose what runs next, touch stored
 * state — and a fourth category would only be a colour.
 */
interface StepKindSpec {
  kind: string;
  label: string;
  category: CanvasCategory;
  description: string;
}

export const WORKFLOW_STEP_KINDS: readonly StepKindSpec[] = [
  {
    kind: 'aggregate',
    label: 'Aggregate',
    category: 'compute',
    description: 'Combine the outputs of several upstream steps.',
  },
  {
    kind: 'conditional',
    label: 'Conditional',
    category: 'control',
    description: 'Branch to one step or another on a condition.',
  },
  {
    kind: 'http_request',
    label: 'HTTP request',
    category: 'compute',
    description: 'Call an HTTP endpoint.',
  },
  {
    kind: 'mcp_call',
    label: 'MCP call',
    category: 'compute',
    description: 'Invoke a tool on a connected MCP server.',
  },
  {
    kind: 'model_turn',
    label: 'Model turn',
    category: 'compute',
    description: 'Run one model turn, optionally with tools.',
  },
  {
    kind: 'notify',
    label: 'Notify',
    category: 'compute',
    description: 'Send a notification to one or more surfaces.',
  },
  {
    kind: 'read_artifact',
    label: 'Read artifact',
    category: 'state',
    description: 'Read a stored artifact by id.',
  },
  {
    kind: 'shell',
    label: 'Shell',
    category: 'compute',
    description: 'Run a shell command.',
  },
  {
    kind: 'tool_call',
    label: 'Tool call',
    category: 'compute',
    description: 'Invoke a built-in tool.',
  },
  {
    kind: 'transform',
    label: 'Transform',
    category: 'compute',
    description: 'Render a template over the values so far.',
  },
  {
    kind: 'wait_until',
    label: 'Wait until',
    category: 'control',
    description: 'Pause until a time, a duration, or a condition.',
  },
  {
    kind: 'web_fetch',
    label: 'Web fetch',
    category: 'compute',
    description: 'Fetch a URL.',
  },
  {
    kind: 'web_scrape',
    label: 'Web scrape',
    category: 'compute',
    description: 'Fetch a URL and extract structured data from it.',
  },
  {
    kind: 'write_artifact',
    label: 'Write artifact',
    category: 'state',
    description: 'Store content as an artifact.',
  },
] as const;

const KIND_SPECS = new Map(WORKFLOW_STEP_KINDS.map((k) => [k.kind, k]));

/**
 * EVERY field the wire `Step` carries
 * (`core/rpc/views/workflows/api.go`). This is the whole vocabulary a
 * structured save can reconstruct a workflow from — `unprojectWorkflow`
 * copies these and nothing else, so anything absent here is destroyed
 * by any save that goes through `Workflows_Save({workflow})`.
 */
export const WIRE_STEP_FIELDS: readonly string[] = [
  'name',
  'kind',
  'inputsFrom',
  'userPrompt',
  'cmd',
  'args',
  'method',
  'url',
  'mode',
] as const;

/**
 * Per-kind fields the wire carries, and therefore the only ones the
 * attribute panel may offer. A control for a field outside this set
 * would take input and discard it.
 */
export const EDITABLE_STEP_FIELDS: Readonly<Record<string, readonly string[]>> = {
  model_turn: ['userPrompt'],
  shell: ['cmd', 'args'],
  http_request: ['method', 'url'],
  web_fetch: ['url'],
  web_scrape: ['url', 'mode'],
};

/**
 * Per-kind fields the Go `workflows.Step` HAS and the wire does NOT
 * (`core/workflows/types.go`, json tags). Every entry is a field a
 * structured save silently drops.
 *
 * The first cut of this list held only kinds whose REQUIRED config was
 * missing, which made it look like the other five kinds were safe. They
 * are not: a `model_turn` loses its tools, profile and model; a `shell`
 * loses env, cwd and timeout; an `http_request` loses headers and body.
 * The correct statement is not "these kinds are lossy" — it is "these
 * NINE fields survive and nothing else does", which is why the banner
 * now leads with the surviving set.
 *
 * The lossiness predates this mission (`unprojectWorkflow` has always
 * copied a handful of fields) and the pre-canvas editor inherited it in
 * silence. Widening the wire to the full ~45-field `Step` is the real
 * fix and is out of scope here; making the loss visible is not.
 * `workflowAdapter.test.ts` pins this map against both the Go struct and
 * the wire struct, so widening the wire forces a conscious shrink.
 */
export const UNREPRESENTED_FIELDS_BY_KIND: Readonly<
  Record<string, readonly string[]>
> = {
  aggregate: ['strategy', 'separator'],
  conditional: ['if', 'thenStep', 'elseStep'],
  http_request: ['headers', 'body'],
  mcp_call: ['server', 'toolName', 'toolArgs'],
  model_turn: ['allowTools', 'tools', 'maxToolIterations', 'profile', 'model'],
  notify: ['notifyTitle', 'notifyBody', 'surface'],
  read_artifact: ['artifactIdRef'],
  shell: ['env', 'cwd', 'timeoutMs'],
  tool_call: ['toolName', 'toolArgs', 'env', 'cwd', 'timeoutMs'],
  transform: ['template'],
  wait_until: ['until', 'duration', 'condition'],
  web_fetch: ['userAgent', 'minIntervalMs'],
  web_scrape: ['extractors', 'extractWithModel', 'extractPrompt'],
  write_artifact: ['title', 'content', 'contentRef', 'mimeType'],
};

/**
 * Kinds a structured save cannot round-trip. Derived rather than
 * hand-listed so it cannot drift from the map above — which is exactly
 * how the first cut came to omit five kinds that were lossy all along.
 */
export const LOSSY_KINDS: readonly string[] = Object.keys(
  UNREPRESENTED_FIELDS_BY_KIND,
)
  .filter((k) => UNREPRESENTED_FIELDS_BY_KIND[k].length > 0)
  .sort();

/** Kinds in this workflow whose config a structured save would drop. */
export function lossyKindsIn(wf: WorkflowsWorkflow | null): string[] {
  if (!wf) return [];
  const present = new Set<string>();
  for (const s of wf.steps) {
    if (LOSSY_KINDS.includes(s.kind)) present.add(s.kind);
  }
  return [...present].sort();
}

/**
 * The specific fields this workflow's steps stand to lose. Naming the
 * fields beats naming the kinds: with every kind lossy, a kind list is
 * noise, while "this workflow's `model_turn` will lose `profile`" is
 * something the author can act on.
 */
export function droppedFieldsIn(wf: WorkflowsWorkflow | null): string[] {
  if (!wf) return [];
  const out = new Set<string>();
  for (const s of wf.steps) {
    for (const f of UNREPRESENTED_FIELDS_BY_KIND[s.kind] ?? []) out.add(f);
  }
  return [...out].sort();
}

// ── dependency graph helpers ──────────────────────────────────────────

/**
 * Deduplicated, self-reference-free, existing-step-only dependencies.
 *
 * `core/workflows/schema.go` rejects a self-reference and an unknown
 * reference outright, and `topoSort` counts a duplicate twice. None of
 * those may reach the canvas as a drawable edge, so they are filtered at
 * the point the view-model is built rather than guarded at every reader.
 */
function depsOf(step: WorkflowsStep, known: Set<string>): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const dep of step.inputsFrom ?? []) {
    if (!dep || dep === step.name || !known.has(dep) || seen.has(dep)) continue;
    seen.add(dep);
    out.push(dep);
  }
  return out;
}

/** step name → the steps that depend on it (i.e. run after it). */
function successorMap(wf: WorkflowsWorkflow): Map<string, string[]> {
  const known = new Set(wf.steps.map((s) => s.name));
  const out = new Map<string, string[]>();
  for (const step of wf.steps) {
    for (const dep of depsOf(step, known)) {
      const list = out.get(dep);
      if (list) list.push(step.name);
      else out.set(dep, [step.name]);
    }
  }
  return out;
}

/** True when `to` can already be reached from `from` along dependencies. */
function reaches(wf: WorkflowsWorkflow, from: string, to: string): boolean {
  const succ = successorMap(wf);
  const seen = new Set<string>([from]);
  const stack = [from];
  while (stack.length > 0) {
    const cur = stack.pop() as string;
    if (cur === to && cur !== from) return true;
    for (const next of succ.get(cur) ?? []) {
      if (seen.has(next)) continue;
      if (next === to) return true;
      seen.add(next);
      stack.push(next);
    }
  }
  return false;
}

/**
 * May this dependency edge be drawn?
 *
 * Every rule here corresponds to one the Go validator already enforces,
 * and the set is CLOSED at those — the canvas must not invent a rule
 * `Validate` does not have (it would refuse a workflow the engine runs
 * fine) nor miss one it does (it would accept an edge the save then
 * rejects):
 *
 *   - unknown step        → schema.go's `ErrUnknownReference`
 *   - self-reference      → schema.go's `ErrWorkflowCycle` on `ref == st.Name`
 *   - cycle               → loader.go's `topoSort` / `findCyclePath`
 *   - duplicate dependency → NOT a Go rule (`inputs_from: [a, a]` passes
 *     Validate and merely double-counts in-degree), but it is not a
 *     drawable second edge either: `canvasEdgeId` is derived from the
 *     endpoints, so a duplicate would collide with the edge already on
 *     the canvas. Refused as a no-op with a plain reason.
 *
 * Reimplementing them in TypeScript is a genuine second rule source and
 * the reason `Graph_CheckEdge` exists on the other family. It is
 * accepted here because workflows exposes no per-edge validation RPC and
 * adding one is a Go change this WP does not justify — the parity test in
 * `workflowAdapter.test.ts` reads `schema.go` + `loader.go` to pin that
 * the rule set has not drifted.
 */
export function checkWorkflowEdge(
  wf: WorkflowsWorkflow | null,
  edge: CanvasEdgeRequest,
): EdgeCheckResult {
  if (!wf) return { ok: false, reason: 'There is no workflow to connect.' };
  const byName = new Map(wf.steps.map((s) => [s.name, s]));
  if (!byName.has(edge.source)) {
    return { ok: false, reason: `No step named "${edge.source}".` };
  }
  const target = byName.get(edge.target);
  if (!target) {
    return { ok: false, reason: `No step named "${edge.target}".` };
  }
  if (edge.source === edge.target) {
    return { ok: false, reason: 'A step cannot depend on itself.' };
  }
  const known = new Set(wf.steps.map((s) => s.name));
  if (depsOf(target, known).includes(edge.source)) {
    return {
      ok: false,
      reason: `"${edge.target}" already depends on "${edge.source}".`,
    };
  }
  // The new edge says source-runs-before-target. That is a cycle exactly
  // when target already runs before source.
  if (reaches(wf, edge.target, edge.source)) {
    return {
      ok: false,
      reason: `That would make a cycle: "${edge.source}" already runs after "${edge.target}".`,
    };
  }
  return { ok: true };
}

// ── op appliers (pure) ────────────────────────────────────────────────

/** Result of applying one op: the new workflow, and any new step name. */
export interface WorkflowOpResult {
  workflow: WorkflowsWorkflow;
  /** Set by add-node and by a rename, so the caller can re-select. */
  selected?: string;
}

/**
 * A free step name for a freshly dropped kind.
 *
 * Named from the KIND rather than a global counter (`step_1`, `step_2`)
 * because a workflow step name is a reference target — it appears in
 * every dependent's `inputs_from` and in `${...}` interpolations — so a
 * name that says what the step is survives being read later.
 */
export function freeStepName(wf: WorkflowsWorkflow, kind: string): string {
  const taken = new Set(wf.steps.map((s) => s.name));
  if (!taken.has(kind)) return kind;
  for (let i = 2; ; i += 1) {
    const candidate = `${kind}_${i}`;
    if (!taken.has(candidate)) return candidate;
  }
}

function withoutDep(step: WorkflowsStep, dep: string): WorkflowsStep {
  const deps = (step.inputsFrom ?? []).filter((d) => d !== dep);
  const next: WorkflowsStep = { ...step };
  if (deps.length > 0) next.inputsFrom = deps;
  else delete next.inputsFrom;
  return next;
}

/**
 * Applies one canvas op to a workflow, returning a new one.
 *
 * Unknown or inapplicable ops return the input unchanged rather than
 * throwing: the canvas emits user intents, and an intent that no longer
 * makes sense (deleting a step a re-load already removed) is a no-op,
 * not an error.
 */
export function applyOpToWorkflow(
  wf: WorkflowsWorkflow,
  op: SpecOp,
): WorkflowOpResult {
  switch (op.type) {
    case 'add-node': {
      const name = op.id && op.id.trim() !== '' ? op.id : freeStepName(wf, op.kind);
      if (wf.steps.some((s) => s.name === name)) return { workflow: wf };
      const step: WorkflowsStep = { name, kind: op.kind };
      return {
        workflow: { ...wf, steps: [...wf.steps, step] },
        selected: name,
      };
    }

    case 'delete-node': {
      if (!wf.steps.some((s) => s.name === op.id)) return { workflow: wf };
      // Drop the step AND prune every mention of it — an `inputs_from`
      // naming a step that no longer exists is `ErrUnknownReference`, so
      // leaving the references behind would make the workflow unsavable.
      const steps = wf.steps
        .filter((s) => s.name !== op.id)
        .map((s) => withoutDep(s, op.id));
      return { workflow: { ...wf, steps } };
    }

    case 'connect': {
      const { edge } = op;
      const steps = wf.steps.map((s) => {
        if (s.name !== edge.target) return s;
        const deps = s.inputsFrom ?? [];
        if (deps.includes(edge.source)) return s;
        return { ...s, inputsFrom: [...deps, edge.source] };
      });
      return { workflow: { ...wf, steps } };
    }

    case 'disconnect': {
      // The canvas identifies an edge by `canvasEdgeId`, which is derived
      // from its four endpoints — so the id IS the endpoints, and the
      // adapter re-derives them rather than keeping a lookup table that
      // could go stale between renders.
      const found = edgesOf(wf).find((e) => e.id === op.edgeId);
      if (!found) return { workflow: wf };
      const steps = wf.steps.map((s) =>
        s.name === found.target ? withoutDep(s, found.source) : s,
      );
      return { workflow: { ...wf, steps } };
    }

    case 'set-attrs': {
      const current = wf.steps.find((s) => s.name === op.id);
      if (!current) return { workflow: wf };
      const rename = typeof op.attrs.name === 'string' ? op.attrs.name.trim() : '';
      // A rename is a reference rewrite. The step name is what every
      // dependent's `inputs_from` points at, so renaming without
      // rewriting them would silently break the DAG — which is what the
      // pre-canvas editor did, harmlessly only because it had no edges.
      const renaming = rename !== '' && rename !== op.id;
      if (renaming && wf.steps.some((s) => s.name === rename)) {
        return { workflow: wf };
      }
      const steps = wf.steps.map((s) => {
        const deps = (s.inputsFrom ?? []).map((d) =>
          renaming && d === op.id ? rename : d,
        );
        const base: WorkflowsStep =
          deps.length > 0 ? { ...s, inputsFrom: deps } : stripDeps(s);
        if (s.name !== op.id) return base;
        return applyStepFields(base, op.attrs, renaming ? rename : s.name);
      });
      return {
        workflow: { ...wf, steps },
        ...(renaming ? { selected: rename } : {}),
      };
    }

    // move-nodes never arrives: `persistsLayout` is false, so the canvas
    // keeps drag positions in component state and emits nothing.
    default:
      return { workflow: wf };
  }
}

function stripDeps(step: WorkflowsStep): WorkflowsStep {
  if (!step.inputsFrom) return { ...step };
  const next = { ...step };
  delete next.inputsFrom;
  return next;
}

/**
 * Writes the per-kind fields from a set-attrs op onto a step.
 *
 * Only `EDITABLE_STEP_FIELDS` are honoured, and an empty value DELETES
 * the key rather than writing `""` — the Go `omitempty` tags mean an
 * empty string and an absent field serialise identically, so writing one
 * would make a save that changed nothing look like a change.
 */
/** The scalar per-kind fields, as a key of `WorkflowsStep`. */
type ScalarStepField = 'userPrompt' | 'cmd' | 'method' | 'url' | 'mode';

const SCALAR_FIELDS: readonly ScalarStepField[] = [
  'userPrompt',
  'cmd',
  'method',
  'url',
  'mode',
];

function applyStepFields(
  step: WorkflowsStep,
  attrs: Record<string, unknown>,
  name: string,
): WorkflowsStep {
  const next: WorkflowsStep = { ...step, name };
  const allowed = new Set(EDITABLE_STEP_FIELDS[step.kind] ?? []);
  for (const field of SCALAR_FIELDS) {
    if (!allowed.has(field) || !(field in attrs)) continue;
    const raw = attrs[field];
    const value = typeof raw === 'string' ? raw : raw === undefined ? '' : String(raw);
    if (value === '') delete next[field];
    else next[field] = value;
  }
  if (allowed.has('args') && 'args' in attrs) {
    const raw = attrs.args;
    const list = Array.isArray(raw)
      ? raw.map((v) => String(v)).filter((v) => v !== '')
      : [];
    if (list.length > 0) next.args = list;
    else delete next.args;
  }
  return next;
}

// ── view-model ────────────────────────────────────────────────────────

/** The canvas edges a workflow's `inputs_from` lists describe. */
export function edgesOf(wf: WorkflowsWorkflow | null): CanvasEdge[] {
  if (!wf) return [];
  const known = new Set(wf.steps.map((s) => s.name));
  const out: CanvasEdge[] = [];
  for (const step of wf.steps) {
    for (const dep of depsOf(step, known)) {
      const req: CanvasEdgeRequest = {
        source: dep,
        sourcePort: DEFAULT_PORT_OUT.name,
        target: step.name,
        targetPort: DEFAULT_PORT_IN.name,
      };
      out.push({ ...req, id: canvasEdgeId(req), kind: 'dependency' });
    }
  }
  return out;
}

/** The canvas nodes a workflow's steps describe. */
export function nodesOf(wf: WorkflowsWorkflow | null): CanvasNode[] {
  if (!wf) return [];
  return wf.steps.map((step) => ({
    id: step.name,
    kind: step.kind,
    label: step.name,
    category: KIND_SPECS.get(step.kind)?.category ?? 'other',
    // Paper-check note 2: a port-less family synthesises the single
    // implicit pair rather than making ports optional on the interface.
    inputs: [DEFAULT_PORT_IN],
    outputs: [DEFAULT_PORT_OUT],
  }));
}

export interface WorkflowAdapterInput {
  /** Null before a workflow is loaded — the canvas renders empty. */
  workflow: WorkflowsWorkflow | null;
  readOnly: boolean;
  applyOp: (op: SpecOp) => void | Promise<void>;
}

export function buildWorkflowAdapter(input: WorkflowAdapterInput): CanvasAdapter {
  const wf = input.workflow;
  return {
    nodes: nodesOf(wf),
    edges: edgesOf(wf),
    onCheckEdge: async (edge) => checkWorkflowEdge(wf, edge),
    // Structural read-only, the same idiom `graphAdapter` uses: the
    // mutation path is REPLACED, not guarded.
    onSpecOp: input.readOnly ? () => undefined : input.applyOp,
    readOnly: input.readOnly,
    // Paper-check note 6: `Workflow` has no layout block, so drags stay
    // in component state and every load auto-lays out.
    persistsLayout: false,
  };
}
