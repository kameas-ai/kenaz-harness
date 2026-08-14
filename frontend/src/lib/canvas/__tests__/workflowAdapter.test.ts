/**
 * The workflows `CanvasAdapter` — Consumer B of the paper check
 * (visual-graph-authoring-01PMUX01 WP06, FR-006).
 *
 * Everything under test is a pure function, so the mapping and the edge
 * rules are pinned without mounting vue-flow. Two of these tests read
 * the GO SOURCE: the step-kind enum and the validator's edge rules both
 * live in Go with no RPC surfacing them, so the only way to keep the
 * TypeScript copies honest is to check them against the original.
 */
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  EDITABLE_STEP_FIELDS,
  LOSSY_KINDS,
  UNREPRESENTED_FIELDS_BY_KIND,
  WIRE_STEP_FIELDS,
  WORKFLOW_STEP_KINDS,
  applyOpToWorkflow,
  buildWorkflowAdapter,
  checkWorkflowEdge,
  droppedFieldsIn,
  edgesOf,
  freeStepName,
  lossyKindsIn,
  nodesOf,
} from '../workflowAdapter';
import { DEFAULT_PORT_IN, DEFAULT_PORT_OUT } from '../types';
import type { WorkflowsWorkflow } from '@/lib/workflowsClient';

const HERE = dirname(fileURLToPath(import.meta.url));
/** frontend/src/lib/canvas/__tests__ → repo root. */
const REPO_ROOT = resolve(HERE, '../../../../..');

function wf(steps: WorkflowsWorkflow['steps']): WorkflowsWorkflow {
  return { id: 'w', name: 'W', version: 1, steps };
}

const CHAIN = wf([
  { name: 'fetch', kind: 'http_request' },
  { name: 'parse', kind: 'transform', inputsFrom: ['fetch'] },
  { name: 'save', kind: 'write_artifact', inputsFrom: ['parse'] },
]);

// ── the mapping, both directions ─────────────────────────────────────

describe('steps ⇄ nodes', () => {
  it('maps one step to one node, name as id', () => {
    const nodes = nodesOf(CHAIN);
    expect(nodes.map((n) => n.id)).toEqual(['fetch', 'parse', 'save']);
    expect(nodes.map((n) => n.kind)).toEqual([
      'http_request',
      'transform',
      'write_artifact',
    ]);
  });

  /*
   * Paper-check note 2: workflows has no ports, so rather than making
   * `CanvasNode.inputs`/`outputs` optional — which would put an
   * `if (port)` through every renderer and hit-test — the adapter
   * synthesises the single implicit pair the interface named for it.
   */
  it('synthesises exactly the default port pair', () => {
    for (const node of nodesOf(CHAIN)) {
      expect(node.inputs).toEqual([DEFAULT_PORT_IN]);
      expect(node.outputs).toEqual([DEFAULT_PORT_OUT]);
    }
  });

  /* Paper-check note 5: workflows has no containment. */
  it('never sets the containment fields', () => {
    for (const node of nodesOf(CHAIN)) {
      expect(node.group).toBeUndefined();
      expect(node.isGroup).toBeUndefined();
    }
  });

  it('has no run overlay on an authoring canvas', () => {
    for (const node of nodesOf(CHAIN)) {
      expect(node.status).toBeUndefined();
    }
  });

  it('categorises by kind, and degrades an unknown kind to other', () => {
    const nodes = nodesOf(
      wf([
        { name: 'a', kind: 'model_turn' },
        { name: 'b', kind: 'conditional' },
        { name: 'c', kind: 'write_artifact' },
        { name: 'd', kind: 'teleport' },
      ]),
    );
    expect(nodes.map((n) => n.category)).toEqual([
      'compute',
      'control',
      'state',
      'other',
    ]);
  });
});

describe('inputs_from ⇄ edges', () => {
  it('draws one dependency edge per inputs_from entry', () => {
    expect(edgesOf(CHAIN)).toEqual([
      {
        id: 'fetch:out→parse:in',
        source: 'fetch',
        sourcePort: 'out',
        target: 'parse',
        targetPort: 'in',
        kind: 'dependency',
      },
      {
        id: 'parse:out→save:in',
        source: 'parse',
        sourcePort: 'out',
        target: 'save',
        targetPort: 'in',
        kind: 'dependency',
      },
    ]);
  });

  it('draws nothing for a dependency on a step that does not exist', () => {
    // Validate() rejects this outright, so it can only reach the canvas
    // from a hand-edited YAML — and a dangling edge is not drawable.
    expect(edgesOf(wf([{ name: 'a', kind: 'shell', inputsFrom: ['ghost'] }]))).toEqual([]);
  });

  it('collapses a duplicate dependency to one edge', () => {
    // `inputs_from: [a, a]` passes Validate and double-counts in-degree,
    // but two identical edges would collide on canvasEdgeId.
    const edges = edgesOf(
      wf([
        { name: 'a', kind: 'shell' },
        { name: 'b', kind: 'shell', inputsFrom: ['a', 'a'] },
      ]),
    );
    expect(edges).toHaveLength(1);
  });

  it('draws nothing for a self-reference', () => {
    expect(edgesOf(wf([{ name: 'a', kind: 'shell', inputsFrom: ['a'] }]))).toEqual([]);
  });
});

// ── edge legality ────────────────────────────────────────────────────

describe('checkWorkflowEdge', () => {
  const edge = (source: string, target: string) => ({
    source,
    sourcePort: 'out',
    target,
    targetPort: 'in',
  });

  it('accepts a legal new dependency', () => {
    expect(checkWorkflowEdge(CHAIN, edge('fetch', 'save'))).toEqual({ ok: true });
  });

  it('refuses a self-edge', () => {
    const v = checkWorkflowEdge(CHAIN, edge('fetch', 'fetch'));
    expect(v.ok).toBe(false);
    expect(v.reason).toContain('itself');
  });

  it('refuses an edge to or from a step that does not exist', () => {
    expect(checkWorkflowEdge(CHAIN, edge('ghost', 'save')).ok).toBe(false);
    expect(checkWorkflowEdge(CHAIN, edge('fetch', 'ghost')).ok).toBe(false);
  });

  it('refuses a duplicate dependency', () => {
    const v = checkWorkflowEdge(CHAIN, edge('fetch', 'parse'));
    expect(v.ok).toBe(false);
    expect(v.reason).toContain('already depends on');
  });

  it('refuses the immediate back-edge', () => {
    const v = checkWorkflowEdge(CHAIN, edge('parse', 'fetch'));
    expect(v.ok).toBe(false);
    expect(v.reason).toContain('cycle');
  });

  it('refuses a cycle several hops long', () => {
    // save runs after parse runs after fetch; save → fetch closes it.
    const v = checkWorkflowEdge(CHAIN, edge('save', 'fetch'));
    expect(v.ok).toBe(false);
    expect(v.reason).toContain('cycle');
  });

  it('allows a diamond, which is not a cycle', () => {
    const diamond = wf([
      { name: 'a', kind: 'shell' },
      { name: 'b', kind: 'shell', inputsFrom: ['a'] },
      { name: 'c', kind: 'shell', inputsFrom: ['a'] },
      { name: 'd', kind: 'shell', inputsFrom: ['b'] },
    ]);
    expect(checkWorkflowEdge(diamond, edge('c', 'd'))).toEqual({ ok: true });
  });

  it('has nothing to say about an absent workflow', () => {
    expect(checkWorkflowEdge(null, edge('a', 'b')).ok).toBe(false);
  });
});

// ── ops ──────────────────────────────────────────────────────────────

describe('applyOpToWorkflow', () => {
  it('never mutates its input', () => {
    const before = JSON.stringify(CHAIN);
    applyOpToWorkflow(CHAIN, { type: 'delete-node', id: 'parse' });
    applyOpToWorkflow(CHAIN, {
      type: 'connect',
      edge: { source: 'fetch', sourcePort: 'out', target: 'save', targetPort: 'in' },
    });
    expect(JSON.stringify(CHAIN)).toBe(before);
  });

  it('adds a step named for its kind, with no dependencies', () => {
    const { workflow, selected } = applyOpToWorkflow(CHAIN, {
      type: 'add-node',
      kind: 'notify',
      position: { x: 0, y: 0 },
    });
    expect(selected).toBe('notify');
    expect(workflow.steps.at(-1)).toEqual({ name: 'notify', kind: 'notify' });
  });

  it('allocates a free name rather than colliding', () => {
    expect(freeStepName(wf([{ name: 'shell', kind: 'shell' }]), 'shell')).toBe('shell_2');
    expect(
      freeStepName(
        wf([
          { name: 'shell', kind: 'shell' },
          { name: 'shell_2', kind: 'shell' },
        ]),
        'shell',
      ),
    ).toBe('shell_3');
  });

  it('deletes a step and every reference to it', () => {
    const { workflow } = applyOpToWorkflow(CHAIN, { type: 'delete-node', id: 'parse' });
    expect(workflow.steps.map((s) => s.name)).toEqual(['fetch', 'save']);
    expect(workflow.steps.find((s) => s.name === 'save')?.inputsFrom).toBeUndefined();
  });

  it('connects by appending to the target inputs_from', () => {
    const { workflow } = applyOpToWorkflow(CHAIN, {
      type: 'connect',
      edge: { source: 'fetch', sourcePort: 'out', target: 'save', targetPort: 'in' },
    });
    expect(workflow.steps.find((s) => s.name === 'save')?.inputsFrom).toEqual([
      'parse',
      'fetch',
    ]);
  });

  it('disconnects by edge id, re-deriving the endpoints from it', () => {
    const { workflow } = applyOpToWorkflow(CHAIN, {
      type: 'disconnect',
      edgeId: 'fetch:out→parse:in',
    });
    expect(workflow.steps.find((s) => s.name === 'parse')?.inputsFrom).toBeUndefined();
  });

  it('drops the key rather than leaving an empty list', () => {
    const { workflow } = applyOpToWorkflow(CHAIN, {
      type: 'disconnect',
      edgeId: 'parse:out→save:in',
    });
    expect('inputsFrom' in (workflow.steps.find((s) => s.name === 'save') ?? {})).toBe(
      false,
    );
  });

  it('sets only the fields the selected kind actually has', () => {
    const { workflow } = applyOpToWorkflow(CHAIN, {
      type: 'set-attrs',
      id: 'fetch',
      attrs: { url: 'https://x.test', cmd: 'rm -rf /', userPrompt: 'hi' },
    });
    const step = workflow.steps.find((s) => s.name === 'fetch');
    expect(step?.url).toBe('https://x.test');
    // `cmd` and `userPrompt` are not http_request fields; the wire would
    // not carry them and the engine would ignore them.
    expect(step?.cmd).toBeUndefined();
    expect(step?.userPrompt).toBeUndefined();
  });

  it('clearing a field deletes it instead of writing an empty string', () => {
    const withURL = applyOpToWorkflow(CHAIN, {
      type: 'set-attrs',
      id: 'fetch',
      attrs: { url: 'https://x.test' },
    }).workflow;
    const cleared = applyOpToWorkflow(withURL, {
      type: 'set-attrs',
      id: 'fetch',
      attrs: { url: '' },
    }).workflow;
    expect('url' in (cleared.steps.find((s) => s.name === 'fetch') ?? {})).toBe(false);
  });

  it('renames a step and rewrites every dependent', () => {
    const { workflow, selected } = applyOpToWorkflow(CHAIN, {
      type: 'set-attrs',
      id: 'fetch',
      attrs: { name: 'download' },
    });
    expect(selected).toBe('download');
    expect(workflow.steps.map((s) => s.name)).toEqual(['download', 'parse', 'save']);
    expect(workflow.steps.find((s) => s.name === 'parse')?.inputsFrom).toEqual([
      'download',
    ]);
  });

  it('refuses a rename onto a name already taken', () => {
    const { workflow } = applyOpToWorkflow(CHAIN, {
      type: 'set-attrs',
      id: 'fetch',
      attrs: { name: 'parse' },
    });
    expect(workflow).toBe(CHAIN);
  });

  it('treats an op against a step that is gone as a no-op', () => {
    expect(applyOpToWorkflow(CHAIN, { type: 'delete-node', id: 'ghost' }).workflow).toBe(
      CHAIN,
    );
    expect(
      applyOpToWorkflow(CHAIN, { type: 'set-attrs', id: 'ghost', attrs: {} }).workflow,
    ).toBe(CHAIN);
    expect(
      applyOpToWorkflow(CHAIN, { type: 'disconnect', edgeId: 'a:out→b:in' }).workflow,
    ).toBe(CHAIN);
  });
});

// ── the adapter itself ───────────────────────────────────────────────

describe('buildWorkflowAdapter', () => {
  const build = (readOnly = false, applied: unknown[] = []) =>
    buildWorkflowAdapter({
      workflow: CHAIN,
      readOnly,
      applyOp: (op) => {
        applied.push(op);
      },
    });

  /*
   * Paper-check note 6, and the one place the two families genuinely
   * differ in capability: `Workflow` has no layout block, so drags stay
   * in canvas component state and nothing is ever persisted.
   */
  it('does not persist layout', () => {
    expect(build().persistsLayout).toBe(false);
    expect(nodesOf(CHAIN).every((n) => n.layout === undefined)).toBe(true);
  });

  it('answers edge legality locally, with no RPC', async () => {
    await expect(
      build().onCheckEdge({
        source: 'save',
        sourcePort: 'out',
        target: 'fetch',
        targetPort: 'in',
      }),
    ).resolves.toMatchObject({ ok: false });
  });

  it('is structurally read-only when asked', async () => {
    const applied: unknown[] = [];
    const adapter = build(true, applied);
    await adapter.onSpecOp({ type: 'delete-node', id: 'fetch' });
    expect(applied).toEqual([]);
  });

  it('renders an empty canvas for an absent workflow', () => {
    const adapter = buildWorkflowAdapter({
      workflow: null,
      readOnly: false,
      applyOp: () => undefined,
    });
    expect(adapter.nodes).toEqual([]);
    expect(adapter.edges).toEqual([]);
  });
});

// ── keeping the TypeScript copies honest ─────────────────────────────

describe('parity with the Go source', () => {
  /*
   * `core/workflows` exposes AllStepKinds() as a bare enum — no display
   * name, no category, nothing over the bridge — so the palette table
   * has to live in TypeScript. The editor this WP replaced had the same
   * table and silently omitted `mcp_call` for the want of this test.
   */
  it('covers every StepKind the Go enum declares', () => {
    const src = readFileSync(resolve(REPO_ROOT, 'core/workflows/types.go'), 'utf8');
    const goKinds = [...src.matchAll(/StepKind\w+\s+StepKind\s*=\s*"([a-z_]+)"/g)].map(
      (m) => m[1],
    );
    expect(goKinds.length).toBeGreaterThan(0);
    const ours = new Set(WORKFLOW_STEP_KINDS.map((k) => k.kind));
    expect([...goKinds].filter((k) => !ours.has(k))).toEqual([]);
    expect([...ours].filter((k) => !goKinds.includes(k))).toEqual([]);
  });

  /*
   * A CANARY, not a parity proof — and the distinction matters enough to
   * state, because the name it used to have ("mirrors the reference
   * rules") claimed something it does not do.
   *
   * The edge rules are reimplemented in TypeScript because workflows has
   * no per-edge check RPC (agentgraph has `Graph_CheckEdge` precisely so
   * ITS rules are never forked). Nothing here executes the Go rules or
   * compares verdicts; it asserts that three named substrings still
   * appear in `schema.go` / `loader.go`. That catches the rules being
   * DELETED or RENAMED out from under the copy — the realistic drift —
   * and catches nothing about their behaviour changing while the names
   * hold. Real parity needs the RPC.
   */
  it('canary: the Go rules this copy mirrors are still named there', () => {
    const schema = readFileSync(resolve(REPO_ROOT, 'core/workflows/schema.go'), 'utf8');
    expect(schema).toContain('inputs_from unknown step');
    expect(schema).toContain('inputs_from itself');
    const loader = readFileSync(resolve(REPO_ROOT, 'core/workflows/loader.go'), 'utf8');
    expect(loader).toContain('findCyclePath');
  });

  /*
   * The lossy map is a claim about `unprojectWorkflow`, and it is the
   * claim the first cut of this WP got WRONG: it listed only kinds whose
   * REQUIRED config was missing, which implied the other five were safe.
   * They were not — `model_turn` loses profile/model/tools, `shell` loses
   * env/cwd/timeout, `http_request` loses headers/body.
   *
   * So this pins the map from BOTH sides against the two Go structs:
   * every unrepresented field must really exist on the Go Step and must
   * really be absent from the wire Step, and no Go field may go
   * unaccounted for. Widen the wire and this fails until the map shrinks
   * — which is the point.
   */
  function goStepJSONTags(path: string): string[] {
    const src = readFileSync(resolve(REPO_ROOT, path), 'utf8');
    const start = src.indexOf('type Step struct');
    expect(start).toBeGreaterThan(-1);
    const block = src.slice(start, src.indexOf('\n}', start));
    return [...block.matchAll(/json:"([A-Za-z]+)[,"]/g)].map((m) => m[1]);
  }

  it('carries exactly the fields the wire Step declares', () => {
    const wire = goStepJSONTags('core/rpc/views/workflows/api.go');
    expect([...WIRE_STEP_FIELDS].sort()).toEqual([...wire].sort());
  });

  it('accounts for every field on the Go Step, as wire-carried or dropped', () => {
    const goFields = new Set(goStepJSONTags('core/workflows/types.go'));
    expect(goFields.size).toBeGreaterThan(30);
    const accounted = new Set<string>(WIRE_STEP_FIELDS);
    for (const fields of Object.values(UNREPRESENTED_FIELDS_BY_KIND)) {
      for (const f of fields) accounted.add(f);
    }
    // A new Go field that nobody classified is a silent new data loss.
    expect([...goFields].filter((f) => !accounted.has(f)).sort()).toEqual([]);
  });

  it('never calls a wire-carried field unrepresented', () => {
    const wire = new Set(WIRE_STEP_FIELDS);
    for (const [kind, fields] of Object.entries(UNREPRESENTED_FIELDS_BY_KIND)) {
      for (const f of fields) {
        expect({ kind, field: f, onWire: wire.has(f) }).toEqual({
          kind,
          field: f,
          onWire: false,
        });
      }
    }
  });

  it('only offers editable fields the wire can carry', () => {
    const wire = new Set(WIRE_STEP_FIELDS);
    for (const fields of Object.values(EDITABLE_STEP_FIELDS)) {
      for (const f of fields) expect(wire.has(f)).toBe(true);
    }
  });

  /*
   * Every kind is lossy. That is not a list worth reading, which is why
   * the banner leads with the surviving fields instead — but the SET is
   * still pinned, so a wire widening that makes a kind whole shows up
   * here as a deliberate edit rather than passing unnoticed.
   */
  it('reports every step kind as lossy, because every one of them is', () => {
    expect(LOSSY_KINDS).toEqual([...WORKFLOW_STEP_KINDS.map((k) => k.kind)].sort());
    expect(lossyKindsIn(CHAIN)).toEqual([
      'http_request',
      'transform',
      'write_artifact',
    ]);
    expect(lossyKindsIn(wf([{ name: 'a', kind: 'shell' }]))).toEqual(['shell']);
    expect(lossyKindsIn(null)).toEqual([]);
  });

  it('names the specific fields a workflow stands to lose', () => {
    expect(droppedFieldsIn(wf([{ name: 'a', kind: 'shell' }]))).toEqual([
      'cwd',
      'env',
      'timeoutMs',
    ]);
    // The union across kinds, deduplicated: tool_call and shell share
    // env/cwd/timeoutMs and must not be reported twice.
    expect(
      droppedFieldsIn(
        wf([
          { name: 'a', kind: 'shell' },
          { name: 'b', kind: 'tool_call' },
        ]),
      ),
    ).toEqual(['cwd', 'env', 'timeoutMs', 'toolArgs', 'toolName']);
    expect(droppedFieldsIn(null)).toEqual([]);
  });
});
