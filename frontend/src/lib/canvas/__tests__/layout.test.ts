/**
 * Auto-layout tests (visual-graph-authoring-01PMUX01 WP02).
 *
 * These are also the RECORD OF THE dagre-vs-elkjs DECISION. plan.md
 * said to decide empirically against the routed `chat_default`'s
 * diamond/skip topology and to switch to elkjs HERE if dagre failed it.
 * The legibility assertions below are the measurement: zero node
 * overlaps, every edge pointing down-rank, and the decision diamond
 * reconverging. dagre passes them, so dagre stays.
 */
import { describe, expect, it } from 'vitest';

import { buildGraphAdapter } from '../graphAdapter';
import { parseGraphText } from '../graphSpec';
import { autoLayout, type LayoutBox } from '../layout';
import type { CanvasEdge, CanvasNode } from '../types';

import { DIAMOND_YAML, libraryGraphYAML, syntheticGraphYAML } from '../__fixtures__/graphFixtures';

function adapterFor(yaml: string): { nodes: CanvasNode[]; edges: CanvasEdge[] } {
  const parsed = parseGraphText(yaml);
  expect(parsed.error).toBeNull();
  const adapter = buildGraphAdapter({
    graph: parsed.graph,
    manifests: [],
    readOnly: true,
    checkEdge: async () => ({ ok: true }),
    applyOp: () => undefined,
  });
  return { nodes: adapter.nodes, edges: adapter.edges };
}

function overlaps(a: LayoutBox, b: LayoutBox): boolean {
  return (
    a.x < b.x + b.width &&
    b.x < a.x + a.width &&
    a.y < b.y + b.height &&
    b.y < a.y + a.height
  );
}

/** Node pairs whose boxes intersect, excluding group/member pairs. */
function overlappingPairs(nodes: CanvasNode[], boxes: Record<string, LayoutBox>): string[] {
  const out: string[] = [];
  for (let i = 0; i < nodes.length; i += 1) {
    for (let j = i + 1; j < nodes.length; j += 1) {
      const a = nodes[i];
      const b = nodes[j];
      // A member is *supposed* to sit inside its container's frame.
      if (a.group === b.id || b.group === a.id) continue;
      if (overlaps(boxes[a.id], boxes[b.id])) out.push(`${a.id}/${b.id}`);
    }
  }
  return out;
}

describe('autoLayout', () => {
  it('is deterministic — two runs produce identical coordinates', () => {
    const { nodes, edges } = adapterFor(libraryGraphYAML('chat_default'));
    const first = autoLayout(nodes, edges);
    const second = autoLayout(nodes, edges);
    expect(second.positions).toEqual(first.positions);
    expect(second.boxes).toEqual(first.boxes);
  });

  it('does not depend on the order the spec listed nodes and edges in', () => {
    const { nodes, edges } = adapterFor(libraryGraphYAML('chat_default'));
    const forward = autoLayout(nodes, edges);
    const shuffled = autoLayout([...nodes].reverse(), [...edges].reverse());
    expect(shuffled.positions).toEqual(forward.positions);
  });

  it('produces integer coordinates (layout persists as ints)', () => {
    const { nodes, edges } = adapterFor(libraryGraphYAML('chat_default'));
    const { positions } = autoLayout(nodes, edges);
    for (const p of Object.values(positions)) {
      expect(Number.isInteger(p.x)).toBe(true);
      expect(Number.isInteger(p.y)).toBe(true);
    }
  });

  describe('dagre legibility — the routed chat_default (the elk decision)', () => {
    const { nodes, edges } = adapterFor(libraryGraphYAML('chat_default'));
    const { boxes } = autoLayout(nodes, edges);

    it('places every node exactly once', () => {
      for (const n of nodes) {
        expect(boxes[n.id], `no box for ${n.id}`).toBeDefined();
      }
    });

    it('overlaps no two node boxes', () => {
      expect(overlappingPairs(nodes, boxes)).toEqual([]);
    });

    it('frames the agent_loop body around its three members', () => {
      const members = nodes.filter((n) => n.group === 'agent_loop').map((n) => n.id);
      expect(members.sort()).toEqual(['assistant_turn', 'route', 'tool_dispatch']);
      const frame = boxes['agent_loop'];
      for (const id of members) {
        const b = boxes[id];
        expect(b.x).toBeGreaterThanOrEqual(frame.x);
        expect(b.y).toBeGreaterThanOrEqual(frame.y);
        expect(b.x + b.width).toBeLessThanOrEqual(frame.x + frame.width);
        expect(b.y + b.height).toBeLessThanOrEqual(frame.y + frame.height);
      }
    });

    it('draws every outer edge downward — no back-edges to read', () => {
      const memberOf = new Map(nodes.filter((n) => n.group).map((n) => [n.id, n.group as string]));
      const backwards: string[] = [];
      for (const e of edges) {
        const from = memberOf.get(e.source) ?? e.source;
        const to = memberOf.get(e.target) ?? e.target;
        if (from === to) continue;
        if (boxes[from].y >= boxes[to].y) backwards.push(`${from}→${to}`);
      }
      expect(backwards).toEqual([]);
    });

    it('reconverges the replan_check diamond on exit_gate', () => {
      // replan_check fans to recover (true) and exit_gate (false), and
      // recover feeds exit_gate. A legible layout puts the decision
      // above both and the join below both.
      const decision = boxes['replan_check'];
      const recover = boxes['recover'];
      const join = boxes['exit_gate'];
      expect(decision.y).toBeLessThan(recover.y);
      expect(decision.y).toBeLessThan(join.y);
      expect(recover.y).toBeLessThan(join.y);
    });
  });

  it('lays out a decision diamond without overlaps', () => {
    const { nodes, edges } = adapterFor(DIAMOND_YAML);
    const { boxes } = autoLayout(nodes, edges);
    expect(overlappingPairs(nodes, boxes)).toEqual([]);
    expect(boxes['gate'].y).toBeLessThan(boxes['left'].y);
    expect(boxes['gate'].y).toBeLessThan(boxes['right'].y);
    expect(boxes['left'].y).toBeLessThan(boxes['join'].y);
    // The two arms sit side by side, not stacked.
    expect(boxes['left'].y).toBe(boxes['right'].y);
    expect(boxes['left'].x).not.toBe(boxes['right'].x);
  });

  it('handles a 50-node synthetic graph without overlaps', () => {
    const { nodes, edges } = adapterFor(syntheticGraphYAML(50));
    expect(nodes).toHaveLength(50);
    const { boxes } = autoLayout(nodes, edges);
    expect(Object.keys(boxes)).toHaveLength(50);
    expect(overlappingPairs(nodes, boxes)).toEqual([]);
  });

  it('lets authored layout coordinates win over auto-layout (FR-004)', () => {
    const yaml = `${DIAMOND_YAML}layout:
  gate: { x: 999, y: 777 }
`;
    const { nodes, edges } = adapterFor(yaml);
    const { positions } = autoLayout(nodes, edges);
    expect(positions['gate']).toEqual({ x: 999, y: 777 });
    // The un-authored nodes still get real positions.
    expect(positions['left']).toBeDefined();
    expect(positions['left']).not.toEqual({ x: 0, y: 0 });
  });

  it('returns an empty result for an empty graph', () => {
    const { positions, boxes } = autoLayout([], []);
    expect(positions).toEqual({});
    expect(boxes).toEqual({});
  });
});
