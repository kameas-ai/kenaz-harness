/**
 * Drag-and-drop authoring flows (visual-graph-authoring-01PMUX01 WP03).
 *
 * HONEST SCOPE. vue-flow's pointer pipeline needs a laid-out viewport
 * (element rects, ResizeObserver) and happy-dom reports a zero-size
 * container — vue-flow itself warns about it on every mount. Simulating
 * a real handle-to-handle drag here would be simulating our own mock,
 * not the library, and the test would pass whether or not the wiring
 * worked. So these tests drive the handlers vue-flow invokes and assert
 * the SPEC OP that comes out the other side, which is the contract that
 * actually matters: gesture → op → buffer.
 *
 * What that leaves uncovered, stated plainly: that vue-flow calls these
 * handlers with the arguments assumed here. That is a Playwright smoke,
 * tracked as a follow-up (plan.md's stated fallback for this risk).
 */
import { mount } from '@vue/test-utils';
import { describe, expect, it, vi } from 'vitest';

import { buildGraphAdapter } from '@/lib/canvas/graphAdapter';
import { parseGraphText } from '@/lib/canvas/graphSpec';
import type { CanvasEdgeRequest, EdgeCheckResult, SpecOp } from '@/lib/canvas/types';
import { DIAMOND_YAML } from '@/lib/canvas/__fixtures__/graphFixtures';

import GraphCanvas from '../GraphCanvas.vue';

interface Harness {
  ops: SpecOp[];
  checked: Array<Record<string, string>>;
  vm: {
    onDrop: (ev: DragEvent) => void;
    onConnect: (c: {
      source: string;
      target: string;
      sourceHandle?: string | null;
      targetHandle?: string | null;
    }) => Promise<void>;
    onNodeDragStop: (p: {
      nodes?: Array<{ id: string; position: { x: number; y: number } }>;
    }) => void;
    deleteSelection: () => void;
    pendingLayout: () => Record<string, { x: number; y: number }>;
    movedNodeIds: () => string[];
    selectedEdgeId: string;
    rejection: string;
  };
  wrapper: ReturnType<typeof mount>;
}

function harness(
  opts: {
    readOnly?: boolean;
    verdict?: EdgeCheckResult;
    selectedNodeId?: string;
  } = {},
): Harness {
  const ops: SpecOp[] = [];
  const checked: Array<Record<string, string>> = [];
  const parsed = parseGraphText(DIAMOND_YAML);
  const adapter = buildGraphAdapter({
    graph: parsed.graph,
    manifests: [{ id: 'transform', callable: true, category: 'compute' }],
    readOnly: opts.readOnly ?? false,
    checkEdge: async (edge) => {
      checked.push({ ...edge });
      return opts.verdict ?? { ok: true };
    },
    applyOp: (op) => {
      ops.push(op);
    },
  });
  const wrapper = mount(GraphCanvas, {
    props: {
      adapter,
      fitViewOnInit: false,
      selectedNodeId: opts.selectedNodeId ?? '',
    },
  });
  return { ops, checked, vm: wrapper.vm as unknown as Harness['vm'], wrapper };
}

function dropEvent(kind: string): DragEvent {
  return {
    clientX: 120,
    clientY: 80,
    preventDefault: () => undefined,
    dataTransfer: {
      types: ['application/x-kenaz-node-kind'],
      getData: (mime: string) =>
        mime === 'application/x-kenaz-node-kind' ? kind : '',
    },
  } as unknown as DragEvent;
}

describe('palette drop', () => {
  it('emits an add-node op carrying the kind and the drop position', () => {
    const h = harness();
    h.vm.onDrop(dropEvent('transform'));
    expect(h.ops).toHaveLength(1);
    const op = h.ops[0];
    expect(op.type).toBe('add-node');
    if (op.type !== 'add-node') throw new Error('unreachable');
    expect(op.kind).toBe('transform');
    expect(Number.isInteger(op.position.x)).toBe(true);
    expect(Number.isInteger(op.position.y)).toBe(true);
  });

  it('emits nothing when the drag carries no kind', () => {
    const h = harness();
    h.vm.onDrop({
      clientX: 0,
      clientY: 0,
      preventDefault: () => undefined,
      dataTransfer: { types: [], getData: () => '' },
    } as unknown as DragEvent);
    expect(h.ops).toEqual([]);
  });

  it('emits nothing on a read-only canvas', () => {
    const h = harness({ readOnly: true });
    h.vm.onDrop(dropEvent('transform'));
    expect(h.ops).toEqual([]);
  });

  it('registers no drop handler at all when read-only', () => {
    const h = harness({ readOnly: true });
    const root = h.wrapper.find('[data-testid="graph-canvas"]');
    // Structural, not cosmetic: a read-only canvas is not focusable and
    // exposes no Delete control, so there is no mutation path to guard.
    expect(root.attributes('tabindex')).toBeUndefined();
    expect(h.wrapper.find('[data-testid="canvas-delete"]').exists()).toBe(false);
  });
});

describe('edge drawing', () => {
  it('asks the adapter before committing, then emits connect', async () => {
    const h = harness();
    await h.vm.onConnect({
      source: 'start',
      target: 'join',
      sourceHandle: 'out',
      targetHandle: 'in',
    });
    expect(h.checked).toEqual([
      { source: 'start', sourcePort: 'out', target: 'join', targetPort: 'in' },
    ]);
    expect(h.ops).toEqual([
      {
        type: 'connect',
        edge: {
          source: 'start',
          sourcePort: 'out',
          target: 'join',
          targetPort: 'in',
        },
      },
    ]);
  });

  it('refuses the edge and shows the validator message on a rejection', async () => {
    const h = harness({
      verdict: { ok: false, reason: 'type mismatch a.out(text) → b.in(json)' },
    });
    await h.vm.onConnect({
      source: 'start',
      target: 'join',
      sourceHandle: 'out',
      targetHandle: 'in',
    });
    expect(h.ops).toEqual([]);
    await h.wrapper.vm.$nextTick();
    const banner = h.wrapper.find('[data-testid="canvas-edge-rejected"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('type mismatch a.out(text) → b.in(json)');
  });

  it('falls back to a generic message when the verdict carries none', async () => {
    const h = harness({ verdict: { ok: false } });
    await h.vm.onConnect({
      source: 'start',
      target: 'join',
      sourceHandle: 'out',
      targetHandle: 'in',
    });
    await h.wrapper.vm.$nextTick();
    expect(h.wrapper.find('[data-testid="canvas-edge-rejected"]').text()).toContain(
      'not allowed',
    );
  });

  it('clears the rejection once a legal edge lands', async () => {
    const check = vi
      .fn(async (_edge: CanvasEdgeRequest): Promise<EdgeCheckResult> => ({ ok: true }))
      .mockResolvedValueOnce({ ok: false, reason: 'nope' })
      .mockResolvedValueOnce({ ok: true });
    const parsed = parseGraphText(DIAMOND_YAML);
    const wrapper = mount(GraphCanvas, {
      props: {
        adapter: buildGraphAdapter({
          graph: parsed.graph,
          manifests: [],
          readOnly: false,
          checkEdge: check,
          applyOp: () => undefined,
        }),
        fitViewOnInit: false,
      },
    });
    const vm = wrapper.vm as unknown as Harness['vm'];
    const conn = {
      source: 'start',
      target: 'join',
      sourceHandle: 'out',
      targetHandle: 'in',
    };
    await vm.onConnect(conn);
    expect(vm.rejection).toBe('nope');
    await vm.onConnect(conn);
    expect(vm.rejection).toBe('');
  });

  it('does not draw edges on a read-only canvas', async () => {
    const h = harness({ readOnly: true });
    await h.vm.onConnect({
      source: 'start',
      target: 'join',
      sourceHandle: 'out',
      targetHandle: 'in',
    });
    expect(h.checked).toEqual([]);
    expect(h.ops).toEqual([]);
  });
});

describe('delete', () => {
  it('deletes the selected node', () => {
    const h = harness({ selectedNodeId: 'gate' });
    h.vm.deleteSelection();
    expect(h.ops).toEqual([{ type: 'delete-node', id: 'gate' }]);
  });

  it('deletes the selected edge when no node is selected', async () => {
    const h = harness();
    (h.wrapper.vm as unknown as { selectedEdgeId: string }).selectedEdgeId =
      'gate:true→left:in';
    await h.wrapper.vm.$nextTick();
    h.vm.deleteSelection();
    expect(h.ops).toEqual([{ type: 'disconnect', edgeId: 'gate:true→left:in' }]);
  });

  it('does nothing with an empty selection', () => {
    const h = harness();
    h.vm.deleteSelection();
    expect(h.ops).toEqual([]);
  });

  it('does nothing on a read-only canvas', () => {
    const h = harness({ readOnly: true, selectedNodeId: 'gate' });
    h.vm.deleteSelection();
    expect(h.ops).toEqual([]);
  });
});

describe('drag to move', () => {
  it('keeps positions in component state — no op is emitted', () => {
    const h = harness();
    h.vm.onNodeDragStop({ nodes: [{ id: 'gate', position: { x: 33.6, y: 71.2 } }] });
    // The whole point of the plan's "layout churn" mitigation: a drag
    // writes no YAML. Only save() does, from pendingLayout().
    expect(h.ops).toEqual([]);
    expect(h.vm.pendingLayout()['gate']).toEqual({ x: 34, y: 71 });
  });

  it('ignores drags on a read-only canvas', () => {
    const h = harness({ readOnly: true });
    const before = { ...h.vm.pendingLayout() };
    h.vm.onNodeDragStop({ nodes: [{ id: 'gate', position: { x: 99, y: 99 } }] });
    expect(h.vm.pendingLayout()).toEqual(before);
  });

  // Unwired-sweep pin (2026-08-14). `CanvasAdapter.persistsLayout` was
  // set by both adapters and read by NOTHING — workflowAdapter.ts's
  // "move-nodes never arrives" comment described an invariant the canvas
  // never enforced, and it held only because WorkflowGraphEditor happens
  // not to hold a ref to the canvas. pendingLayout() now honours the flag,
  // so a family whose spec has no layout block cannot grow one.
  it('emits no layout for an adapter that does not persist layout', () => {
    const parsed = parseGraphText(DIAMOND_YAML);
    const base = buildGraphAdapter({
      graph: parsed.graph,
      manifests: [],
      readOnly: false,
      checkEdge: async () => ({ ok: true }),
      applyOp: () => undefined,
    });
    const wrapper = mount(GraphCanvas, {
      props: {
        adapter: { ...base, persistsLayout: false },
        fitViewOnInit: false,
      },
    });
    const vm = wrapper.vm as unknown as Harness['vm'];
    vm.onNodeDragStop({ nodes: [{ id: 'gate', position: { x: 33.6, y: 71.2 } }] });
    expect(vm.movedNodeIds()).toContain('gate');
    expect(vm.pendingLayout()).toEqual({});
  });
});
