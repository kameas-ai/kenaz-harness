/**
 * WorkflowGraphEditor, rebuilt on the shared canvas
 * (visual-graph-authoring-01PMUX01 WP06, FR-006).
 *
 * These tests were rewritten wholesale rather than adapted. The suite
 * they replace asserted things that no longer exist and should not: an
 * SVG element, ↑/↓ reorder buttons, and a "canvas" whose only meaning
 * was declaration order. What is asserted now is what the component
 * actually is — a shell over `GraphCanvas` driven by the workflows
 * adapter — plus the two things the old editor could not do at all:
 * express a dependency, and refuse a cycle.
 *
 * The DELETION is asserted too (see the last block): the mission's
 * no-partial rule means the sequential-SVG implementation must be gone,
 * not merely unused.
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import WorkflowGraphEditor from '@/components/workflows/WorkflowGraphEditor.vue';
import { WIRE_STEP_FIELDS } from '@/lib/canvas/workflowAdapter';
import type { WorkflowsWorkflow } from '@/lib/workflowsClient';

const HERE = dirname(fileURLToPath(import.meta.url));
const COMPONENT = resolve(HERE, '../WorkflowGraphEditor.vue');

const FAKE_WF: WorkflowsWorkflow = {
  id: 'my-wf',
  name: 'My Workflow',
  version: 1,
  steps: [
    { name: 'fetch', kind: 'http_request' },
    { name: 'parse', kind: 'transform', inputsFrom: ['fetch'] },
    { name: 'save', kind: 'write_artifact', inputsFrom: ['parse'] },
  ],
};

function mountEditor(props: Record<string, unknown> = {}) {
  return mount(WorkflowGraphEditor, { props });
}

/**
 * vue-flow syncs its internal node store from the `nodes` prop across a
 * macrotask, so a canvas assertion that follows a mutation needs one
 * more turn than a microtask flush gives it.
 */
async function settle() {
  await flushPromises();
  await new Promise((r) => setTimeout(r, 0));
  await flushPromises();
}

/** The node labels the canvas drew — the first span of each node card. */
function nodeIDs(wrapper: ReturnType<typeof mountEditor>): string[] {
  return wrapper
    .findAll('[data-testid^="canvas-node-"]')
    .map((w) => w.findAll('span')[0]?.text() ?? '');
}

describe('WorkflowGraphEditor — the shell', () => {
  it('renders the editor root', () => {
    expect(
      mountEditor().find('[data-testid="workflow-graph-editor"]').exists(),
    ).toBe(true);
  });

  it('mounts the SHARED canvas, not a private one', () => {
    // `graph-canvas` is GraphCanvas's own test id — the same component
    // the agentgraph editor mounts. That is FR-001 in one assertion.
    expect(mountEditor().find('[data-testid="graph-canvas"]').exists()).toBe(true);
  });

  it('pre-populates id and name from the workflow prop', () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    expect(
      (wrapper.get('[data-testid="wge-id"]').element as HTMLInputElement).value,
    ).toBe('my-wf');
    expect(
      (wrapper.get('[data-testid="wge-name"]').element as HTMLInputElement).value,
    ).toBe('My Workflow');
  });

  it('shows every step kind in the palette', () => {
    const wrapper = mountEditor();
    expect(wrapper.find('[data-testid="wge-palette"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="wge-palette-model_turn"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="wge-palette-shell"]').exists()).toBe(true);
    // The palette this replaces silently omitted mcp_call.
    expect(wrapper.find('[data-testid="wge-palette-mcp_call"]').exists()).toBe(true);
  });

  it('emits cancel', async () => {
    const wrapper = mountEditor();
    await wrapper.get('[data-testid="wge-cancel"]').trigger('click');
    expect(wrapper.emitted('cancel')).toBeTruthy();
  });

  it('does not allow editing in readonly mode', () => {
    const wrapper = mountEditor({ workflow: FAKE_WF, readonly: true });
    expect(wrapper.find('[data-testid="wge-save"]').exists()).toBe(false);
    expect(wrapper.get('[data-testid="wge-id"]').attributes('disabled')).toBeDefined();
    expect(
      wrapper.get('[data-testid="graph-canvas"]').attributes('data-readonly'),
    ).toBe('true');
  });
});

describe('WorkflowGraphEditor — steps as nodes, inputs_from as edges', () => {
  it('renders one node per step', async () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    await settle();
    expect(
      wrapper.get('[data-testid="graph-canvas"]').attributes('data-node-count'),
    ).toBe('3');
    expect(nodeIDs(wrapper).sort()).toEqual(['fetch', 'parse', 'save']);
  });

  it('renders the dependency edges the old editor could not express', () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    expect(wrapper.vm.adapter.edges.map((e) => e.id)).toEqual([
      'fetch:out→parse:in',
      'parse:out→save:in',
    ]);
  });

  it('adds a step with the kind as its name when the palette is clicked', async () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    await settle();
    await wrapper.get('[data-testid="wge-palette-shell"]').trigger('click');
    await settle();
    expect(nodeIDs(wrapper)).toContain('shell');
    // A dropped step starts unconnected — the author draws the edge.
    expect(
      wrapper.vm.draft.steps.find((s) => s.name === 'shell')?.inputsFrom,
    ).toBeUndefined();
  });

  it('does not collide names when the same kind is added twice', async () => {
    const wrapper = mountEditor();
    await wrapper.get('[data-testid="wge-palette-shell"]').trigger('click');
    await wrapper.get('[data-testid="wge-palette-shell"]').trigger('click');
    expect(wrapper.vm.draft.steps.map((s) => s.name)).toEqual(['shell', 'shell_2']);
  });

  it('writes a drawn edge into the target step inputs_from', async () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    await wrapper.vm.applyOp({
      type: 'connect',
      edge: { source: 'fetch', sourcePort: 'out', target: 'save', targetPort: 'in' },
    });
    expect(wrapper.vm.draft.steps.find((s) => s.name === 'save')?.inputsFrom).toEqual([
      'parse',
      'fetch',
    ]);
  });

  it('removes the dependency when an edge is deleted', async () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    await wrapper.vm.applyOp({ type: 'disconnect', edgeId: 'parse:out→save:in' });
    expect(
      wrapper.vm.draft.steps.find((s) => s.name === 'save')?.inputsFrom,
    ).toBeUndefined();
  });

  // Deleting a step that others depend on must prune the references:
  // an inputs_from naming a missing step is ErrUnknownReference and the
  // workflow would no longer save at all.
  it('prunes every mention when a step is deleted', async () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    await wrapper.vm.applyOp({ type: 'delete-node', id: 'parse' });
    expect(wrapper.vm.draft.steps.map((s) => s.name)).toEqual(['fetch', 'save']);
    expect(
      wrapper.vm.draft.steps.find((s) => s.name === 'save')?.inputsFrom,
    ).toBeUndefined();
  });
});

describe('WorkflowGraphEditor — the step config form', () => {
  it('shows only the fields the selected kind has', async () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    wrapper.vm.selectedName = 'fetch';
    await wrapper.vm.$nextTick();
    expect(wrapper.find('[data-testid="wge-step-url"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="wge-step-method"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="wge-step-cmd"]').exists()).toBe(false);
  });

  it('round-trips a url edit into the draft', async () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    wrapper.vm.selectedName = 'fetch';
    await wrapper.vm.$nextTick();
    await wrapper.get('[data-testid="wge-step-url"]').setValue('https://example.com');
    expect(wrapper.vm.draft.steps.find((s) => s.name === 'fetch')?.url).toBe(
      'https://example.com',
    );
  });

  it('rewrites dependents when a step is renamed', async () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    wrapper.vm.selectedName = 'fetch';
    await wrapper.vm.$nextTick();
    await wrapper.get('[data-testid="wge-step-name"]').setValue('download');
    await wrapper.get('[data-testid="wge-step-name"]').trigger('change');
    expect(wrapper.vm.draft.steps.map((s) => s.name)).toEqual([
      'download',
      'parse',
      'save',
    ]);
    expect(wrapper.vm.draft.steps.find((s) => s.name === 'parse')?.inputsFrom).toEqual([
      'download',
    ]);
  });

  it('says so when a kind has no wire-carried config', async () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    wrapper.vm.selectedName = 'parse';
    await wrapper.vm.$nextTick();
    expect(wrapper.find('[data-testid="wge-step-yaml-only"]').exists()).toBe(true);
  });
});

describe('WorkflowGraphEditor — save + validation', () => {
  it('emits save with the assembled workflow, dependencies included', async () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    await wrapper.get('[data-testid="wge-save"]').trigger('click');
    const payload = wrapper.emitted('save')?.[0]?.[0] as WorkflowsWorkflow;
    expect(payload.id).toBe('my-wf');
    expect(payload.steps).toHaveLength(3);
    expect(payload.steps[1].inputsFrom).toEqual(['fetch']);
  });

  it('blocks save and lists the errors when required fields are missing', () => {
    const wrapper = mountEditor();
    expect(
      wrapper.get('[data-testid="wge-save"]').attributes('disabled'),
    ).toBeDefined();
    expect(wrapper.find('[data-testid="wge-errors"]').exists()).toBe(true);
  });

  /*
   * The structured save path reconstructs a workflow from the wire
   * `Step`, which is a subset of the Go one. That predates this WP and
   * the editor it replaces inherited it in SILENCE — the fix here is
   * that it is now stated, not that it is gone.
   */
  it('states the fields that survive a save, not the kinds that do not', () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    const survivors = wrapper.get('[data-testid="wge-lossy-survivors"]').text();
    // Exactly the wire Step's fields — the whole vocabulary a structured
    // save can rebuild from.
    for (const f of WIRE_STEP_FIELDS) expect(survivors).toContain(f);
    expect(survivors).not.toContain('template');
  });

  it('names the specific fields this workflow stands to lose', () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    const dropped = wrapper.get('[data-testid="wge-lossy-dropped"]').text();
    expect(dropped).toContain('template'); // transform
    expect(dropped).toContain('content'); // write_artifact
    expect(dropped).toContain('headers'); // http_request — lossy too
  });

  /*
   * The first cut of the warning listed only kinds whose REQUIRED config
   * was missing, so a shell-only workflow showed no banner at all —
   * while quietly dropping env, cwd and timeout_ms. Every kind is lossy;
   * the banner is unconditional for that reason.
   */
  it('warns even for a workflow of only "simple" kinds', () => {
    const wrapper = mountEditor({
      workflow: {
        id: 'w',
        name: 'W',
        version: 1,
        steps: [{ name: 'a', kind: 'shell', cmd: 'ls' }],
      },
    });
    expect(wrapper.get('[data-testid="wge-lossy-dropped"]').text()).toContain('cwd');
  });

  it('does not warn on an empty draft or in readonly mode', () => {
    expect(mountEditor().find('[data-testid="wge-lossy-warning"]').exists()).toBe(
      false,
    );
    expect(
      mountEditor({ workflow: FAKE_WF, readonly: true })
        .find('[data-testid="wge-lossy-warning"]')
        .exists(),
    ).toBe(false);
  });

  it('previews YAML including the dependency edges', async () => {
    const wrapper = mountEditor({ workflow: FAKE_WF });
    expect(wrapper.find('[data-testid="wge-yaml-preview"]').exists()).toBe(false);
    await wrapper.get('[data-testid="wge-yaml-toggle"]').trigger('click');
    const yaml = wrapper.get('[data-testid="wge-yaml-preview"]').text();
    expect(yaml).toContain('kind: http_request');
    expect(yaml).toContain('inputs_from');
  });
});

/*
 * The no-partial rule. A rebuilt editor that leaves the old one in the
 * file is two editors, and the campaign rejects that outright — so the
 * absence is an assertion rather than a claim in a commit message.
 */
describe('WorkflowGraphEditor — the sequential SVG is gone', () => {
  const source = readFileSync(COMPONENT, 'utf8');

  it('has no hand-rolled SVG canvas left in the source', () => {
    for (const marker of ['<svg', '<rect', '<foreignObject', 'marker-end', 'arrowhead']) {
      expect(source).not.toContain(marker);
    }
  });

  it('has no declaration-order layout maths left', () => {
    for (const marker of ['NODE_GAP', 'CANVAS_PADDING', 'nodeY', 'canvasHeight']) {
      expect(source).not.toContain(marker);
    }
  });

  it('has no reorder-by-position controls left', () => {
    for (const marker of ['moveUp', 'moveDown', 'wge-node-up-', 'wge-node-down-']) {
      expect(source).not.toContain(marker);
    }
    expect(mountEditor({ workflow: FAKE_WF }).find('svg[role="img"]').exists()).toBe(
      false,
    );
  });

  it('draws its nodes through the shared canvas component', () => {
    expect(source).toContain("import GraphCanvas from '@/components/canvas/GraphCanvas.vue'");
    expect(source).toContain('buildWorkflowAdapter');
  });
});
