/**
 * Spec-op mechanics (visual-graph-authoring-01PMUX01 WP03).
 *
 * Each op is applied to the parsed Document and read back, so what is
 * asserted is the BUFFER — the same text the textarea shows and the
 * save path sends — not an intermediate model.
 */
import { describe, expect, it } from 'vitest';

import {
  allocateNodeId,
  applyOpToDoc,
  parseGraphText,
  pruneStaleLayout,
  serializeDoc,
} from '../graphSpec';

import { DIAMOND_YAML, libraryGraphYAML } from '../__fixtures__/graphFixtures';

function apply(yaml: string, ...ops: Parameters<typeof applyOpToDoc>[1][]): string {
  const parsed = parseGraphText(yaml);
  expect(parsed.doc).not.toBeNull();
  for (const op of ops) applyOpToDoc(parsed.doc!, op);
  return serializeDoc(parsed.doc!);
}

/**
 * A pure parse→serialize round trip. "This op changed nothing" is
 * asserted against THIS, not against the original text: the YAML
 * library normalises a handful of author-chosen whitespace choices
 * (extra alignment spaces after a key's colon) that no CST-preserving
 * serializer can reproduce, and that normalisation is not the op's
 * doing. `round-trip fidelity` below measures the normalisation itself.
 */
function roundTrip(yaml: string): string {
  const parsed = parseGraphText(yaml);
  return serializeDoc(parsed.doc!);
}

function reparse(yaml: string) {
  const parsed = parseGraphText(yaml);
  expect(parsed.error).toBeNull();
  return parsed.graph!;
}

describe('add-node', () => {
  it('appends a node with the manifest-derived default attrs', () => {
    const parsed = parseGraphText(DIAMOND_YAML);
    applyOpToDoc(
      parsed.doc!,
      { type: 'add-node', kind: 'model', position: { x: 40, y: 90 } },
      { attrsFor: () => ({ model: 'default', max_tokens: 4096 }) },
    );
    const g = reparse(serializeDoc(parsed.doc!));
    const added = g.nodes.find((n) => n.id === 'model_1');
    expect(added).toBeDefined();
    expect(added?.kind).toBe('model');
    expect(added?.attrs).toEqual({ model: 'default', max_tokens: 4096 });
  });

  it('records the drop position in the layout block, integer-rounded', () => {
    const out = apply(DIAMOND_YAML, {
      type: 'add-node',
      kind: 'transform',
      position: { x: 40.7, y: 90.2 },
    });
    expect(reparse(out).layout['transform_1']).toEqual({ x: 41, y: 90 });
  });

  it('allocates a non-colliding id', () => {
    const out = apply(
      DIAMOND_YAML,
      { type: 'add-node', kind: 'transform', position: { x: 0, y: 0 } },
      { type: 'add-node', kind: 'transform', position: { x: 0, y: 0 } },
    );
    const ids = reparse(out).nodes.map((n) => n.id);
    expect(ids).toContain('transform_1');
    expect(ids).toContain('transform_2');
    expect(new Set(ids).size).toBe(ids.length);
  });

  it('honours a caller-supplied id when it is free', () => {
    const out = apply(DIAMOND_YAML, {
      type: 'add-node',
      kind: 'transform',
      id: 'my_step',
      position: { x: 0, y: 0 },
    });
    expect(reparse(out).nodes.map((n) => n.id)).toContain('my_step');
  });

  it('makes the first node of an empty graph the entrypoint', () => {
    const out = apply('spec_version: "1"\nid: blank\n', {
      type: 'add-node',
      kind: 'plan',
      position: { x: 0, y: 0 },
    });
    const g = reparse(out);
    expect(g.entrypoints).toEqual(['plan_1']);
  });

  it('never touches entrypoints once one exists', () => {
    const out = apply(DIAMOND_YAML, {
      type: 'add-node',
      kind: 'transform',
      position: { x: 0, y: 0 },
    });
    expect(reparse(out).entrypoints).toEqual(['start']);
  });
});

describe('delete-node', () => {
  it('removes the node, its edges, its layout entry and its entrypoint', () => {
    const withLayout = apply(DIAMOND_YAML, {
      type: 'move-nodes',
      positions: { start: { x: 1, y: 2 }, gate: { x: 3, y: 4 } },
    });
    const out = apply(withLayout, { type: 'delete-node', id: 'start' });
    const g = reparse(out);
    expect(g.nodes.map((n) => n.id)).not.toContain('start');
    expect(g.edges.some((e) => e.from.node === 'start' || e.to.node === 'start')).toBe(false);
    expect(g.layout['start']).toBeUndefined();
    expect(g.layout['gate']).toEqual({ x: 3, y: 4 });
    expect(g.entrypoints).toEqual([]);
  });

  it('drops the empty edges block rather than leaving `edges: []`', () => {
    const yaml = `spec_version: "1"
id: two
entrypoints: [a]
nodes:
  - id: a
    kind: transform
    attrs: {}
  - id: b
    kind: transform
    attrs: {}
edges:
  - from: { node: a, port: out }
    to: { node: b, port: in }
`;
    const out = apply(yaml, { type: 'delete-node', id: 'b' });
    expect(out).not.toMatch(/edges:/);
  });

  it('is a no-op for an id that is not there', () => {
    const out = apply(DIAMOND_YAML, { type: 'delete-node', id: 'ghost' });
    expect(out).toBe(roundTrip(DIAMOND_YAML));
  });
});

describe('connect / disconnect', () => {
  it('appends the edge in the spec’s from/to shape', () => {
    const out = apply(DIAMOND_YAML, {
      type: 'connect',
      edge: { source: 'start', sourcePort: 'out', target: 'join', targetPort: 'extra' },
    });
    const g = reparse(out);
    expect(g.edges).toContainEqual({
      from: { node: 'start', port: 'out' },
      to: { node: 'join', port: 'extra' },
    });
  });

  it('refuses to duplicate an edge that already exists', () => {
    const out = apply(DIAMOND_YAML, {
      type: 'connect',
      edge: { source: 'start', sourcePort: 'out', target: 'gate', targetPort: 'in' },
    });
    expect(out).toBe(roundTrip(DIAMOND_YAML));
  });

  it('creates the edges block when the graph has none', () => {
    const yaml = `spec_version: "1"
id: two
entrypoints: [a]
nodes:
  - id: a
    kind: transform
    attrs: {}
  - id: b
    kind: transform
    attrs: {}
`;
    const out = apply(yaml, {
      type: 'connect',
      edge: { source: 'a', sourcePort: 'out', target: 'b', targetPort: 'in' },
    });
    expect(reparse(out).edges).toHaveLength(1);
  });

  it('disconnect removes exactly the named edge', () => {
    const out = apply(DIAMOND_YAML, {
      type: 'disconnect',
      edgeId: 'gate:true→left:in',
    });
    const g = reparse(out);
    expect(g.edges).toHaveLength(4);
    expect(
      g.edges.some((e) => e.from.port === 'true' && e.to.node === 'left'),
    ).toBe(false);
  });

  it('disconnect on an unknown edge id changes nothing', () => {
    expect(apply(DIAMOND_YAML, { type: 'disconnect', edgeId: 'a:b→c:d' })).toBe(
      roundTrip(DIAMOND_YAML),
    );
  });
});

describe('move-nodes', () => {
  it('writes integer coordinates', () => {
    const out = apply(DIAMOND_YAML, {
      type: 'move-nodes',
      positions: { gate: { x: 10.4, y: -3.6 } },
    });
    expect(reparse(out).layout['gate']).toEqual({ x: 10, y: -4 });
  });

  it('is stable — re-applying the same positions changes no byte', () => {
    const once = apply(DIAMOND_YAML, {
      type: 'move-nodes',
      positions: { gate: { x: 10, y: 20 }, left: { x: 30, y: 40 } },
    });
    const twice = apply(once, {
      type: 'move-nodes',
      positions: { gate: { x: 10, y: 20 }, left: { x: 30, y: 40 } },
    });
    expect(twice).toBe(once);
  });
});

describe('pruneStaleLayout', () => {
  it('drops entries for nodes that no longer exist', () => {
    const yaml = `${DIAMOND_YAML}layout:
  gate: { x: 1, y: 2 }
  ghost: { x: 3, y: 4 }
`;
    const parsed = parseGraphText(yaml);
    pruneStaleLayout(parsed.doc!);
    const g = reparse(serializeDoc(parsed.doc!));
    expect(g.layout).toEqual({ gate: { x: 1, y: 2 } });
  });

  it('removes the whole block when nothing survives', () => {
    const yaml = `${DIAMOND_YAML}layout:
  ghost: { x: 3, y: 4 }
`;
    const parsed = parseGraphText(yaml);
    pruneStaleLayout(parsed.doc!);
    expect(serializeDoc(parsed.doc!)).not.toMatch(/layout:/);
  });
});

describe('round-trip fidelity on the shipped library graphs', () => {
  // The strong diffability claim, measured rather than asserted by
  // hand-wave: a parse→serialize round trip of the real library files
  // changes NOTHING except the author's extra alignment spaces after a
  // `to:` key (`to:   { … }` → `to: { … }`). The CST records that a
  // value is on the same line as its key, not how many spaces the
  // author put there, so no serializer can reproduce it. Twelve lines
  // across two files, none of them semantic.
  for (const id of ['chat_default', 'toolloop_default']) {
    it(`${id} round-trips modulo key-colon alignment only`, () => {
      const original = libraryGraphYAML(id);
      const out = roundTrip(original);
      const normalise = (t: string) =>
        t
          .split('\n')
          .map((l) => l.replace(/^(\s*[\w"'-]+:)\s+/, '$1 '))
          .join('\n');
      expect(normalise(out)).toBe(normalise(original));
    });
  }
});

describe('comment preservation', () => {
  it('keeps every comment in chat_default across a canvas op', () => {
    const original = libraryGraphYAML('chat_default');
    const commentsBefore = original.split('\n').filter((l) => l.trim().startsWith('#'));
    const out = apply(original, {
      type: 'add-node',
      kind: 'transform',
      position: { x: 5, y: 5 },
    });
    const commentsAfter = out.split('\n').filter((l) => l.trim().startsWith('#'));
    expect(commentsAfter.length).toBe(commentsBefore.length);
    // Spot-check the one that documents the live bug — losing this
    // comment would delete institutional knowledge, which is exactly
    // why the Document API is used instead of parse+stringify.
    expect(out).toContain('WHY THE ROUTER IS A BODY NODE AND NOT A KERNEL-VISIBLE ONE');
  });
});

describe('allocateNodeId', () => {
  it('starts at _1 and skips taken ids', () => {
    expect(allocateNodeId('model', [])).toBe('model_1');
    expect(allocateNodeId('model', ['model_1', 'model_2'])).toBe('model_3');
  });
});
