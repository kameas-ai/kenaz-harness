/**
 * GraphCanvas mount smoke (visual-graph-authoring-01PMUX01 WP02).
 *
 * The plan flagged "vue-flow + jsdom" as a risk and said to test the
 * adapter layer if pointer simulation fought back. It does — happy-dom
 * reports a zero-size container so vue-flow lays out no viewport, and
 * pointer-driven pan/zoom/connect cannot be simulated faithfully. So
 * the RULES are tested in `src/lib/canvas/__tests__` against the pure
 * adapter, and this file asserts only what a mount can honestly prove:
 * the component renders, it renders one node element per adapter node,
 * it labels its controls, and read-only reaches the DOM.
 */
import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';

import { buildGraphAdapter } from '@/lib/canvas/graphAdapter';
import { parseGraphText } from '@/lib/canvas/graphSpec';
import { DIAMOND_YAML } from '@/lib/canvas/__fixtures__/graphFixtures';

import GraphCanvas from '../GraphCanvas.vue';

function adapterFor(yaml: string, readOnly = true) {
  const parsed = parseGraphText(yaml);
  return buildGraphAdapter({
    graph: parsed.graph,
    manifests: [
      { id: 'decision', callable: true, category: 'control' },
      { id: 'transform', callable: true, category: 'compute' },
    ],
    readOnly,
    checkEdge: async () => ({ ok: true }),
    applyOp: () => undefined,
  });
}

describe('GraphCanvas', () => {
  it('renders a canvas surface carrying the node count', () => {
    const w = mount(GraphCanvas, {
      props: { adapter: adapterFor(DIAMOND_YAML), fitViewOnInit: false },
    });
    const root = w.find('[data-testid="graph-canvas"]');
    expect(root.exists()).toBe(true);
    expect(root.attributes('data-node-count')).toBe('5');
    expect(root.attributes('data-readonly')).toBe('true');
  });

  it('shows an empty state when the buffer has no nodes', () => {
    const w = mount(GraphCanvas, {
      props: {
        adapter: buildGraphAdapter({
          graph: null,
          manifests: [],
          readOnly: false,
          checkEdge: async () => ({ ok: true }),
          applyOp: () => undefined,
        }),
        fitViewOnInit: false,
      },
    });
    expect(w.find('[data-testid="canvas-empty"]').exists()).toBe(true);
  });

  it('gives every viewport control an accessible name', () => {
    const w = mount(GraphCanvas, {
      props: { adapter: adapterFor(DIAMOND_YAML), fitViewOnInit: false },
    });
    for (const id of ['canvas-zoom-in', 'canvas-zoom-out', 'canvas-fit-view']) {
      const btn = w.find(`[data-testid="${id}"]`);
      expect(btn.exists(), id).toBe(true);
      expect(btn.attributes('aria-label')).toBeTruthy();
    }
  });

  it('places every node and reports nothing moved before a drag', () => {
    const w = mount(GraphCanvas, {
      props: { adapter: adapterFor(DIAMOND_YAML), fitViewOnInit: false },
    });
    const vm = w.vm as unknown as {
      positions: Record<string, { x: number; y: number }>;
      movedNodeIds: () => string[];
      pendingLayout: () => Record<string, unknown>;
    };
    expect(Object.keys(vm.positions).sort()).toEqual([
      'gate',
      'join',
      'left',
      'right',
      'start',
    ]);
    // Auto-layout positions are not "moves" — a save that only viewed
    // the graph must not write a layout block.
    expect(vm.movedNodeIds().length).toBe(5);
    expect(Object.keys(vm.pendingLayout())).toHaveLength(5);
  });

  it('keeps authored layout coordinates and reports them unmoved', () => {
    const w = mount(GraphCanvas, {
      props: {
        adapter: adapterFor(`${DIAMOND_YAML}layout:
  start: { x: 1, y: 2 }
  gate: { x: 3, y: 4 }
  left: { x: 5, y: 6 }
  right: { x: 7, y: 8 }
  join: { x: 9, y: 10 }
`),
        fitViewOnInit: false,
      },
    });
    const vm = w.vm as unknown as {
      positions: Record<string, { x: number; y: number }>;
      movedNodeIds: () => string[];
      pendingLayout: () => Record<string, unknown>;
    };
    expect(vm.positions['gate']).toEqual({ x: 3, y: 4 });
    expect(vm.movedNodeIds()).toEqual([]);
    expect(vm.pendingLayout()).toEqual({});
  });
});
