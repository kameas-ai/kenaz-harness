/**
 * The run overlay at the adapter level
 * (visual-graph-authoring-01PMUX01 WP05, FR-005).
 *
 * Everything here is a pure function of a parsed materialized graph, so
 * the Airflow-style statuses are pinned without mounting vue-flow: what
 * the canvas draws is `CanvasNode.status`, and this is the code that
 * decides it.
 */
import { describe, it, expect } from 'vitest';

import {
  buildGraphAdapter,
  materializationStatuses,
  toCanvasStatus,
  SPEC_PROVENANCE_LIBRARY_FALLBACK,
} from '../graphAdapter';
import { parseGraphText } from '../graphSpec';
import {
  MATERIALIZED_DEGRADED_YAML,
  MATERIALIZED_RUN_YAML,
  DIAMOND_YAML,
} from '../__fixtures__/graphFixtures';

function parse(yaml: string) {
  const { graph, error } = parseGraphText(yaml);
  expect(error).toBeNull();
  if (!graph) throw new Error('fixture did not parse');
  return graph;
}

describe('materialization parse', () => {
  it('reads the materialized block off every projected node', () => {
    const graph = parse(MATERIALIZED_RUN_YAML);
    const turn = graph.nodes.find((n) => n.id === 'assistant_turn@2');
    expect(turn?.materialized).toEqual({
      sourceNode: 'assistant_turn',
      instance: 2,
      iteration: 2,
      status: 'incomplete',
      startSeq: 10,
    });
  });

  it('leaves materialized undefined on an authored graph', () => {
    const graph = parse(DIAMOND_YAML);
    expect(graph.nodes.every((n) => n.materialized === undefined)).toBe(true);
    expect(graph.specProvenance).toBeUndefined();
  });

  it('reads spec_provenance only when the projection is degraded', () => {
    expect(parse(MATERIALIZED_RUN_YAML).specProvenance).toBeUndefined();
    expect(parse(MATERIALIZED_DEGRADED_YAML).specProvenance).toBe(
      SPEC_PROVENANCE_LIBRARY_FALLBACK,
    );
  });
});

describe('toCanvasStatus', () => {
  it('translates the whole materializer vocabulary', () => {
    expect(toCanvasStatus('completed')).toBe('complete');
    expect(toCanvasStatus('error')).toBe('failed');
    expect(toCanvasStatus('skipped')).toBe('skipped');
    expect(toCanvasStatus('not_reached')).toBe('not_reached');
    expect(toCanvasStatus('incomplete')).toBe('incomplete');
  });

  it('has no opinion about a status it does not know', () => {
    expect(toCanvasStatus(undefined)).toBeUndefined();
    expect(toCanvasStatus('')).toBeUndefined();
    expect(toCanvasStatus('teleported')).toBeUndefined();
  });

  // The live translation is the whole reason a live run needs no second
  // RPC: a fire with no matching complete is "crashed" on a finished run
  // and "executing" on a live one, and nothing else distinguishes them.
  it('reads an unfinished fire as running while the run is live', () => {
    expect(toCanvasStatus('incomplete', true)).toBe('running');
    expect(toCanvasStatus('not_reached', true)).toBe('pending');
  });

  it('does not re-colour a finished fire when live', () => {
    expect(toCanvasStatus('completed', true)).toBe('complete');
    expect(toCanvasStatus('error', true)).toBe('failed');
    expect(toCanvasStatus('skipped', true)).toBe('skipped');
  });
});

describe('materializationStatuses', () => {
  it('overlays every status the materializer emits', () => {
    const statuses = materializationStatuses(parse(MATERIALIZED_RUN_YAML));
    expect(Object.fromEntries(
      Object.entries(statuses).map(([id, o]) => [id, o.status]),
    )).toEqual({
      'history_in@1': 'complete',
      'assistant_turn@1': 'complete',
      'assistant_turn@2': 'incomplete',
      'tool_leg@1': 'failed',
      'skipped_leg@1': 'skipped',
      never_ran: 'not_reached',
    });
  });

  it('carries the seq join key so a click can find the trace rows', () => {
    const statuses = materializationStatuses(parse(MATERIALIZED_RUN_YAML));
    expect(statuses['assistant_turn@1'].detail).toEqual({
      startSeq: 4,
      endSeq: 9,
      sourceNode: 'assistant_turn',
      instance: 1,
      iteration: 1,
    });
    // A node the run never reached has no span to join to.
    expect(statuses.never_ran.detail).toEqual({ sourceNode: 'never_ran' });
  });

  it('is empty for an authored graph and for a null parse', () => {
    expect(materializationStatuses(parse(DIAMOND_YAML))).toEqual({});
    expect(materializationStatuses(null)).toEqual({});
  });
});

describe('the overlay on the adapter', () => {
  const adapterFor = (yaml: string, live = false) => {
    const graph = parse(yaml);
    return buildGraphAdapter({
      graph,
      manifests: [],
      readOnly: true,
      checkEdge: async () => ({ ok: false }),
      applyOp: () => undefined,
      statuses: materializationStatuses(graph, { live }),
    });
  };

  it('puts the status and its detail on the canvas node', () => {
    const nodes = adapterFor(MATERIALIZED_RUN_YAML).nodes;
    const byID = new Map(nodes.map((n) => [n.id, n]));
    expect(byID.get('tool_leg@1')?.status).toBe('failed');
    expect(byID.get('skipped_leg@1')?.status).toBe('skipped');
    expect(byID.get('assistant_turn@1')?.statusDetail?.startSeq).toBe(4);
  });

  // The iteration instances are what make an unrolled loop legible: the
  // same authored node appears twice, as two nodes with two statuses.
  it('keeps per-iteration instances apart', () => {
    const nodes = adapterFor(MATERIALIZED_RUN_YAML).nodes;
    const first = nodes.find((n) => n.id === 'assistant_turn@1');
    const second = nodes.find((n) => n.id === 'assistant_turn@2');
    expect(first?.status).toBe('complete');
    expect(second?.status).toBe('incomplete');
    expect(first?.statusDetail?.sourceNode).toBe(second?.statusDetail?.sourceNode);
  });

  it('shows the in-flight fire as running on a live run', () => {
    const nodes = adapterFor(MATERIALIZED_RUN_YAML, true).nodes;
    expect(nodes.find((n) => n.id === 'assistant_turn@2')?.status).toBe('running');
    // The already-recorded statuses are untouched by liveness.
    expect(nodes.find((n) => n.id === 'tool_leg@1')?.status).toBe('failed');
  });

  /*
   * Structural read-only (spec FR-005, closing the WP12-review N3
   * hazard): a materialized adapter must not merely REFUSE mutation, it
   * must have no mutation path. `onSpecOp` is replaced, not guarded, so
   * there is nothing to forget to check.
   */
  it('has no mutation path at all', async () => {
    const applied: unknown[] = [];
    const graph = parse(MATERIALIZED_RUN_YAML);
    const adapter = buildGraphAdapter({
      graph,
      manifests: [],
      readOnly: true,
      checkEdge: async () => ({ ok: false }),
      applyOp: (op) => {
        applied.push(op);
      },
      statuses: materializationStatuses(graph),
    });
    expect(adapter.readOnly).toBe(true);
    await adapter.onSpecOp({ type: 'delete-node', id: 'assistant_turn@1' });
    await adapter.onSpecOp({
      type: 'add-node',
      kind: 'transform',
      position: { x: 0, y: 0 },
    });
    await adapter.onSpecOp({
      type: 'set-attrs',
      id: 'assistant_turn@1',
      attrs: { hacked: true },
    });
    expect(applied).toEqual([]);
  });
});
