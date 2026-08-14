/**
 * NodeAttributeEditor tests — exercises FR-024 / FR-026 / FR-028:
 *   - Each AttrType renders the matching input widget.
 *   - Bounds enforcement clamps int/float inputs.
 *   - Required attrs show an asterisk + validation message.
 *   - Enum dropdowns are populated from the manifest.
 *   - Provenance chip surfaces the resolver layer.
 *   - Inherited attrs render above kind-specific attrs.
 *   - JSON fallback handles object/array shapes with Format JSON.
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import NodeAttributeEditor from '@/views/agentgraph/NodeAttributeEditor.vue';
import type { NodeManifestDetail } from '@/lib/types';

function mountEditor(opts: {
  kindId?: string;
  attrs?: Record<string, unknown>;
  manifest: NodeManifestDetail | null;
  graphNodeIds?: string[];
  readOnly?: boolean;
}) {
  return mount(NodeAttributeEditor, {
    props: {
      nodeId: 'n1',
      kindId: opts.kindId ?? 'planner',
      attrs: opts.attrs ?? {},
      manifest: opts.manifest,
      readOnly: opts.readOnly ?? false,
      graphNodeIds: opts.graphNodeIds ?? [],
      activityOptions: [
        { id: 'plan', label: 'plan' },
        { id: 'reflect', label: 'reflect' },
      ],
    },
  });
}

describe('NodeAttributeEditor', () => {
  it('shows the empty-state when no kind is selected', () => {
    const wrapper = mountEditor({ kindId: '', manifest: null });
    expect(wrapper.find('[data-testid="node-attr-empty"]').exists()).toBe(true);
  });

  it('renders the loading state when the manifest is null but a kind is set', () => {
    const wrapper = mountEditor({ kindId: 'planner', manifest: null });
    expect(wrapper.find('[data-testid="node-attr-loading"]').exists()).toBe(true);
  });

  it('renders one widget per AttrSpec.type', () => {
    const manifest: NodeManifestDetail = {
      summary: { id: 'kitchen_sink', callable: true },
      chain: ['kitchen_sink'],
      attrs: [
        { name: 'name', type: 'string' },
        { name: 'count', type: 'int', min: 0, max: 10 },
        { name: 'ratio', type: 'float' },
        { name: 'enabled', type: 'bool' },
        { name: 'mode', type: 'enum', enum: ['a', 'b', 'c'] },
        { name: 'pick_model', type: 'model_ref' },
        { name: 'activity', type: 'activity_ref' },
        { name: 'upstream', type: 'node_id_ref' },
        { name: 'meta', type: 'object' },
      ],
      ports: {},
      provenance: [],
    };
    const wrapper = mountEditor({
      manifest,
      attrs: {},
      graphNodeIds: ['a', 'b'],
    });
    expect(
      (wrapper.get('[data-testid="attr-input-name"]').element as HTMLInputElement).type,
    ).toBe('text');
    expect(
      (wrapper.get('[data-testid="attr-input-count"]').element as HTMLInputElement).type,
    ).toBe('number');
    expect(
      (wrapper.get('[data-testid="attr-input-ratio"]').element as HTMLInputElement).type,
    ).toBe('number');
    expect(
      (wrapper.get('[data-testid="attr-input-enabled"]').element as HTMLInputElement).type,
    ).toBe('checkbox');
    expect(wrapper.get('[data-testid="attr-input-mode"]').element.tagName).toBe('SELECT');
    // model_ref / activity_ref / node_id_ref all render as <select>.
    expect(wrapper.get('[data-testid="attr-input-pick_model"]').element.tagName).toBe('SELECT');
    expect(wrapper.get('[data-testid="attr-input-activity"]').element.tagName).toBe('SELECT');
    expect(wrapper.get('[data-testid="attr-input-upstream"]').element.tagName).toBe('SELECT');
    // object → textarea + Format JSON button.
    expect(wrapper.get('[data-testid="attr-input-meta"]').element.tagName).toBe('TEXTAREA');
    expect(wrapper.find('[data-testid="attr-format-meta"]').exists()).toBe(true);
  });

  it('emits update:attrs on string input change', async () => {
    const manifest: NodeManifestDetail = {
      summary: { id: 'p', callable: true },
      chain: ['p'],
      attrs: [{ name: 'title', type: 'string' }],
      ports: {},
      provenance: [],
    };
    const wrapper = mountEditor({ manifest, attrs: { title: '' } });
    await wrapper.get('[data-testid="attr-input-title"]').setValue('hello');
    const events = wrapper.emitted('update:attrs');
    expect(events).toBeTruthy();
    expect(events![events!.length - 1][0]).toEqual({ title: 'hello' });
  });

  it('clamps int input to declared min/max bounds', async () => {
    const manifest: NodeManifestDetail = {
      summary: { id: 'p', callable: true },
      chain: ['p'],
      attrs: [{ name: 'count', type: 'int', min: 1, max: 5, required: true }],
      ports: {},
      provenance: [],
    };
    const wrapper = mountEditor({ manifest, attrs: { count: 1 } });
    const input = wrapper.get('[data-testid="attr-input-count"]');
    await input.setValue('99');
    let events = wrapper.emitted('update:attrs');
    expect(events![events!.length - 1][0]).toEqual({ count: 5 });
    await input.setValue('-3');
    events = wrapper.emitted('update:attrs');
    expect(events![events!.length - 1][0]).toEqual({ count: 1 });
  });

  it('shows an asterisk on required attrs and surfaces a missing-value message', () => {
    const manifest: NodeManifestDetail = {
      summary: { id: 'p', callable: true },
      chain: ['p'],
      attrs: [{ name: 'condition', type: 'string', required: true }],
      ports: {},
      provenance: [],
    };
    const wrapper = mountEditor({ manifest, attrs: {} });
    const label = wrapper.get('[data-testid="attr-row-condition"] label');
    expect(label.text()).toContain('*');
    expect(wrapper.find('[data-testid="attr-required-condition"]').exists()).toBe(true);
  });

  it('populates enum select options from the AttrSpec.enum list', () => {
    const manifest: NodeManifestDetail = {
      summary: { id: 'p', callable: true },
      chain: ['p'],
      attrs: [
        {
          name: 'verbosity',
          type: 'enum',
          enum: ['terse', 'standard', 'verbose'],
          required: true,
        },
      ],
      ports: {},
      provenance: [],
    };
    const wrapper = mountEditor({ manifest, attrs: { verbosity: 'standard' } });
    const select = wrapper.get('[data-testid="attr-input-verbosity"]');
    const options = select.findAll('option').map((o) => o.attributes('value'));
    // No empty placeholder when required.
    expect(options).toEqual(['terse', 'standard', 'verbose']);
  });

  it('shows a provenance chip with the resolver layer label', () => {
    const manifest: NodeManifestDetail = {
      summary: { id: 'p', callable: true },
      chain: ['compute', 'p'],
      attrs: [
        { name: 'max_tokens', type: 'int', default: 5 },
        { name: 'verbosity', type: 'string' },
      ],
      ports: {},
      provenance: [
        { fieldPath: 'attrs.max_tokens', layer: 'user-override' },
        { fieldPath: 'attrs.verbosity', layer: 'shipped' },
      ],
    };
    const wrapper = mountEditor({ manifest, attrs: {} });
    const userChip = wrapper.get('[data-testid="attr-provenance-max_tokens"]');
    expect(userChip.text().toLowerCase()).toContain('user');
    const shippedChip = wrapper.get('[data-testid="attr-provenance-verbosity"]');
    expect(shippedChip.text().toLowerCase()).toContain('shipped');
  });

  it('renders inherited attrs above the kind-specific section with a divider per archetype', () => {
    const manifest: NodeManifestDetail = {
      summary: { id: 'planner', callable: true },
      chain: ['compute', 'planner'],
      attrs: [
        { name: 'system_prompt', type: 'string' },
        { name: 'temperature', type: 'float' },
        { name: 'verbosity', type: 'enum', enum: ['terse', 'standard'] },
      ],
      ports: {},
      provenance: [
        { fieldPath: 'attrs.system_prompt', layer: 'archetype-compute' },
        { fieldPath: 'attrs.temperature', layer: 'archetype-compute' },
        { fieldPath: 'attrs.verbosity', layer: 'shipped' },
      ],
    };
    const wrapper = mountEditor({ manifest, attrs: {} });
    expect(wrapper.find('[data-testid="attr-section-compute"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="attr-divider-compute"]').exists()).toBe(true);
    // Leaf section divider when archetype attrs exist.
    expect(wrapper.find('[data-testid="attr-divider-leaf"]').exists()).toBe(true);
    // Inherited section appears before leaf section in DOM order.
    const html = wrapper.html();
    const computeIdx = html.indexOf('attr-section-compute');
    const leafIdx = html.indexOf('attr-section-leaf');
    expect(computeIdx).toBeGreaterThanOrEqual(0);
    expect(leafIdx).toBeGreaterThan(computeIdx);
  });

  it('JSON fallback parses on Format JSON click and emits the parsed object', async () => {
    const manifest: NodeManifestDetail = {
      summary: { id: 'p', callable: true },
      chain: ['p'],
      attrs: [{ name: 'meta', type: 'object' }],
      ports: {},
      provenance: [],
    };
    const wrapper = mountEditor({ manifest, attrs: { meta: { a: 1 } } });
    const ta = wrapper.get('[data-testid="attr-input-meta"]');
    await ta.setValue('{"b":2}');
    await wrapper.get('[data-testid="attr-format-meta"]').trigger('click');
    const events = wrapper.emitted('update:attrs');
    expect(events).toBeTruthy();
    expect(events![events!.length - 1][0]).toEqual({ meta: { b: 2 } });
  });

  it('JSON fallback surfaces a parse error for invalid JSON on blur', async () => {
    const manifest: NodeManifestDetail = {
      summary: { id: 'p', callable: true },
      chain: ['p'],
      attrs: [{ name: 'meta', type: 'object' }],
      ports: {},
      provenance: [],
    };
    const wrapper = mountEditor({ manifest, attrs: { meta: {} } });
    const ta = wrapper.get('[data-testid="attr-input-meta"]');
    await ta.setValue('{not valid json');
    await ta.trigger('blur');
    expect(wrapper.find('[data-testid="attr-json-error-meta"]').exists()).toBe(true);
  });
});

/*
 * View-only mode (visual-graph-authoring-01PMUX01 WP05) — the second half
 * of the WP12-review N3 hazard: an attribute editor that could type into
 * a record of something that already happened.
 *
 * The editor-level test that came with WP05 asserted this against a
 * manifest with NO attrs, so it was checking for the absence of inputs in
 * a box that had none either way — flipping `v-if="readOnly"` to `false`
 * left it passing. Every assertion below runs against a manifest with one
 * attr of every widget type, so the absence means something.
 */
const KITCHEN_SINK: NodeManifestDetail = {
  summary: { id: 'kitchen_sink', callable: true },
  chain: ['kitchen_sink'],
  attrs: [
    { name: 'name', type: 'string' },
    { name: 'count', type: 'int', min: 0, max: 10 },
    { name: 'ratio', type: 'float' },
    { name: 'enabled', type: 'bool' },
    { name: 'mode', type: 'enum', enum: ['a', 'b', 'c'] },
    { name: 'pick_model', type: 'model_ref' },
    { name: 'activity', type: 'activity_ref' },
    { name: 'upstream', type: 'node_id_ref' },
    { name: 'meta', type: 'object' },
    { name: 'freeform', type: 'wat' },
  ],
  ports: {},
  provenance: [],
};

const SINK_VALUES = {
  name: 'hello',
  count: 3,
  ratio: 1.5,
  enabled: true,
  mode: 'b',
  pick_model: 'sonnet',
  activity: 'plan',
  upstream: 'a',
  meta: { k: 'v' },
  freeform: 'other',
};

describe('NodeAttributeEditor — read-only', () => {
  const writable = () =>
    mountEditor({ manifest: KITCHEN_SINK, attrs: SINK_VALUES, graphNodeIds: ['a'] });
  const readOnly = () =>
    mountEditor({
      manifest: KITCHEN_SINK,
      attrs: SINK_VALUES,
      graphNodeIds: ['a'],
      readOnly: true,
    });

  it('renders a widget per attr when writable — the control group', () => {
    const w = writable();
    // Without this the read-only assertions below would pass vacuously.
    expect(w.findAll('[data-testid^="attr-input-"]').length).toBe(
      KITCHEN_SINK.attrs!.length,
    );
    expect(w.findAll('[data-testid^="attr-value-"]').length).toBe(0);
  });

  it('renders NO input, select or textarea in read-only mode', () => {
    const w = readOnly();
    expect(w.findAll('input').length).toBe(0);
    expect(w.findAll('select').length).toBe(0);
    expect(w.findAll('textarea').length).toBe(0);
    expect(w.findAll('[data-testid^="attr-input-"]').length).toBe(0);
    // The Format JSON button is a mutation affordance too.
    expect(w.find('[data-testid="attr-format-meta"]').exists()).toBe(false);
  });

  it('renders one value row per attr instead', () => {
    const w = readOnly();
    expect(w.findAll('[data-testid^="attr-value-"]').length).toBe(
      KITCHEN_SINK.attrs!.length,
    );
    expect(w.get('[data-testid="attr-value-name"]').text()).toBe('hello');
    expect(w.get('[data-testid="attr-value-count"]').text()).toBe('3');
    expect(w.get('[data-testid="attr-value-enabled"]').text()).toBe('true');
    expect(w.get('[data-testid="attr-value-mode"]').text()).toBe('b');
    expect(w.get('[data-testid="attr-value-meta"]').text()).toBe('{"k":"v"}');
    // The catch-all widget is covered too.
    expect(w.get('[data-testid="attr-value-freeform"]').text()).toBe('other');
  });

  it('shows an em dash rather than a blank for an unset attr', () => {
    const w = mountEditor({ manifest: KITCHEN_SINK, attrs: {}, readOnly: true });
    expect(w.get('[data-testid="attr-value-name"]').text()).toBe('—');
  });

  /*
   * The point of doing it structurally: there is no element left that
   * could emit. A `disabled` attribute would leave the handler bound and
   * one `.trigger('input')` away from writing into the buffer.
   */
  it('has no path to emit update:attrs', () => {
    const w = readOnly();
    expect(w.emitted('update:attrs')).toBeUndefined();
    expect(w.findAll('button').length).toBe(0);
  });

  it('does not nag about required attrs on a record nobody may edit', () => {
    const manifest: NodeManifestDetail = {
      ...KITCHEN_SINK,
      attrs: [{ name: 'needed', type: 'string', required: true }],
    };
    expect(
      mountEditor({ manifest, attrs: {}, readOnly: true })
        .find('[data-testid="attr-required-needed"]')
        .exists(),
    ).toBe(false);
    expect(
      mountEditor({ manifest, attrs: {} })
        .find('[data-testid="attr-required-needed"]')
        .exists(),
    ).toBe(true);
  });
});
