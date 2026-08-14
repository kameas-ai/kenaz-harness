/**
 * Adapter-level tests (visual-graph-authoring-01PMUX01 WP02): a Graph
 * spec goes in, a node/edge view-model comes out.
 *
 * Everything the canvas renders is decided here, in a pure function, so
 * these tests cover the rendering rules without a DOM. (The plan's
 * "vue-flow + jsdom" risk called this shot: the component is thin and
 * the adapter is where the logic lives.)
 */
import { describe, expect, it } from 'vitest';

import type { NodeManifestDetail, NodeManifestSummary } from '@/lib/types';

import { buildGraphAdapter, defaultAttrsForKind } from '../graphAdapter';
import { parseGraphText } from '../graphSpec';
import { DEFAULT_PORT_IN, DEFAULT_PORT_OUT } from '../types';

import { DIAMOND_YAML, libraryGraphYAML, syntheticGraphYAML } from '../__fixtures__/graphFixtures';

const MANIFESTS: NodeManifestSummary[] = [
  { id: 'model', callable: true, category: 'compute', displayName: 'Model' },
  { id: 'transform', callable: true, category: 'compute', displayName: 'Transform' },
  { id: 'plan', callable: true, category: 'compute', displayName: 'Plan' },
  { id: 'decision', callable: true, category: 'control', displayName: 'Decision' },
  { id: 'router', callable: true, category: 'control', displayName: 'Router' },
  { id: 'loop', callable: true, category: 'control', displayName: 'Loop' },
  { id: 'session_write', callable: true, category: 'state', displayName: 'Session write' },
  { id: 'compute', callable: false, category: 'compute', displayName: 'Compute' },
];

function build(yaml: string, extra: Partial<Parameters<typeof buildGraphAdapter>[0]> = {}) {
  const parsed = parseGraphText(yaml);
  expect(parsed.error).toBeNull();
  return buildGraphAdapter({
    graph: parsed.graph,
    manifests: MANIFESTS,
    readOnly: false,
    checkEdge: async () => ({ ok: true }),
    applyOp: () => undefined,
    ...extra,
  });
}

describe('buildGraphAdapter', () => {
  it('maps every spec node to a canvas node with its category colour', () => {
    const adapter = build(DIAMOND_YAML);
    expect(adapter.nodes.map((n) => n.id)).toEqual([
      'start',
      'gate',
      'left',
      'right',
      'join',
    ]);
    const byId = Object.fromEntries(adapter.nodes.map((n) => [n.id, n]));
    expect(byId['gate'].category).toBe('control');
    expect(byId['join'].category).toBe('state');
    expect(byId['left'].category).toBe('compute');
  });

  it('falls back to the neutral category for an unknown kind', () => {
    const adapter = build(DIAMOND_YAML, { manifests: [] });
    expect(adapter.nodes.every((n) => n.category === 'other')).toBe(true);
  });

  it('labels a node by its title, falling back to its id', () => {
    const adapter = build(libraryGraphYAML('chat_default'));
    const byId = Object.fromEntries(adapter.nodes.map((n) => [n.id, n]));
    expect(byId['history_in'].label).toBe('Load conversation history');
    const noTitle = build(DIAMOND_YAML).nodes.find((n) => n.id === 'left');
    expect(noTitle?.label).toBe('left');
  });

  it('prefers explicitly declared ports over the manifest surface', () => {
    const adapter = build(libraryGraphYAML('chat_default'), {
      details: {
        model: {
          summary: { id: 'model', callable: true },
          chain: ['model'],
          attrs: [],
          ports: { inputs: [{ name: 'messages', type: 'messages' }], outputs: [] },
          provenance: [],
        } as NodeManifestDetail,
      },
    });
    const assistant = adapter.nodes.find((n) => n.id === 'assistant_turn');
    // chat_default declares four explicit outputs on assistant_turn.
    expect(assistant?.outputs.map((p) => p.name)).toEqual([
      'response',
      'assistant',
      'tool_calls',
      'finish_reason',
    ]);
    // Inputs are not declared, so the manifest surface fills them in.
    expect(assistant?.inputs.map((p) => p.name)).toEqual(['messages']);
  });

  it('synthesises the default port pair when nothing declares ports', () => {
    const adapter = build(DIAMOND_YAML);
    const left = adapter.nodes.find((n) => n.id === 'left');
    expect(left?.inputs).toEqual([DEFAULT_PORT_IN]);
    expect(left?.outputs).toEqual([DEFAULT_PORT_OUT]);
  });

  it('renders loop-body membership as a group, not as nesting', () => {
    const adapter = build(libraryGraphYAML('chat_default'));
    const byId = Object.fromEntries(adapter.nodes.map((n) => [n.id, n]));
    expect(byId['agent_loop'].isGroup).toBe(true);
    expect(byId['assistant_turn'].group).toBe('agent_loop');
    expect(byId['tool_dispatch'].group).toBe('agent_loop');
    expect(byId['route'].group).toBe('agent_loop');
    // The members are still top-level entries in the flat node list —
    // the canvas must not invent a second nesting model.
    expect(adapter.nodes).toHaveLength(11);
    expect(byId['history_in'].group).toBeUndefined();
  });

  it('ignores a body entry naming a node that does not exist', () => {
    const yaml = `spec_version: "1"
id: g
entrypoints: [l]
nodes:
  - id: l
    kind: loop
    attrs:
      max_iterations: 2
      body: [ghost, real]
  - id: real
    kind: transform
    attrs: {}
`;
    const adapter = build(yaml);
    const byId = Object.fromEntries(adapter.nodes.map((n) => [n.id, n]));
    expect(byId['real'].group).toBe('l');
    expect(adapter.nodes.map((n) => n.id)).toEqual(['l', 'real']);
  });

  it('classifies decision verdict edges as routes', () => {
    const adapter = build(DIAMOND_YAML);
    const byId = Object.fromEntries(adapter.edges.map((e) => [e.id, e]));
    expect(byId['gate:true→left:in'].kind).toBe('route');
    expect(byId['gate:false→right:in'].kind).toBe('route');
    expect(byId['start:out→gate:in'].kind).toBe('data');
  });

  it('classifies router choice edges as routes', () => {
    const yaml = `spec_version: "1"
id: g
entrypoints: [a]
nodes:
  - id: a
    kind: router
    attrs:
      mode: fused
      source_node: a
      default_choice: done
      choices:
        done:
          target: b
  - id: b
    kind: transform
    attrs: {}
edges:
  - from: { node: a, port: done }
    to: { node: b, port: in }
  - from: { node: a, port: out }
    to: { node: b, port: in2 }
`;
    const adapter = build(yaml);
    const byId = Object.fromEntries(adapter.edges.map((e) => [e.id, e]));
    expect(byId['a:done→b:in'].kind).toBe('route');
    expect(byId['a:out→b:in2'].kind).toBe('data');
  });

  it('carries authored layout onto the node view-model', () => {
    const adapter = build(`${DIAMOND_YAML}layout:
  gate: { x: 10, y: 20 }
`);
    const byId = Object.fromEntries(adapter.nodes.map((n) => [n.id, n]));
    expect(byId['gate'].layout).toEqual({ x: 10, y: 20 });
    expect(byId['left'].layout).toBeUndefined();
  });

  it('passes run statuses through for the WP05 overlay', () => {
    const adapter = build(DIAMOND_YAML, {
      statuses: {
        gate: { status: 'complete', detail: { startSeq: 4 } },
        left: { status: 'skipped' },
      },
    });
    const byId = Object.fromEntries(adapter.nodes.map((n) => [n.id, n]));
    expect(byId['gate'].status).toBe('complete');
    expect(byId['gate'].statusDetail).toEqual({ startSeq: 4 });
    expect(byId['left'].status).toBe('skipped');
    expect(byId['right'].status).toBeUndefined();
  });

  it('renders an unparseable buffer as an empty canvas, not a crash', () => {
    const adapter = buildGraphAdapter({
      graph: null,
      manifests: MANIFESTS,
      readOnly: false,
      checkEdge: async () => ({ ok: true }),
      applyOp: () => undefined,
    });
    expect(adapter.nodes).toEqual([]);
    expect(adapter.edges).toEqual([]);
  });

  it('gives a read-only adapter no mutation path at all', () => {
    let applied = 0;
    const adapter = build(DIAMOND_YAML, {
      readOnly: true,
      applyOp: () => {
        applied += 1;
      },
    });
    expect(adapter.readOnly).toBe(true);
    void adapter.onSpecOp({ type: 'delete-node', id: 'gate' });
    expect(applied).toBe(0);
  });

  it('scales to a 50-node graph', () => {
    const adapter = build(syntheticGraphYAML(50));
    expect(adapter.nodes).toHaveLength(50);
    expect(adapter.edges).toHaveLength(49);
    expect(new Set(adapter.edges.map((e) => e.id)).size).toBe(49);
  });
});

describe('defaultAttrsForKind', () => {
  it('is empty for an unknown kind', () => {
    expect(defaultAttrsForKind(undefined)).toEqual({});
  });

  it('takes per-attr defaults, then the manifest defaults map', () => {
    const detail = {
      summary: { id: 'loop', callable: true },
      chain: ['loop'],
      attrs: [
        { name: 'max_iterations', type: 'int', default: 10 },
        { name: 'condition', type: 'string', required: true },
        { name: 'note', type: 'string' },
      ],
      ports: {},
      defaults: { max_iterations: 25 },
      provenance: [],
    } as NodeManifestDetail;
    expect(defaultAttrsForKind(detail)).toEqual({
      max_iterations: 25,
      condition: '',
    });
  });

  it('seeds a required numeric attr with 0 so the editor has a row', () => {
    const detail = {
      summary: { id: 'x', callable: true },
      chain: ['x'],
      attrs: [{ name: 'n', type: 'int', required: true }],
      ports: {},
      provenance: [],
    } as NodeManifestDetail;
    expect(defaultAttrsForKind(detail)).toEqual({ n: 0 });
  });
});
