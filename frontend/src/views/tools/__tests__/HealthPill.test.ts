/**
 * HealthPill tests — WP03 (mcp-server-health-ui-01KQ8TD6)
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import HealthPill from '@/views/tools/HealthPill.vue';

const STATES = ['running', 'starting', 'restarting', 'failed', 'stopped'] as const;

describe('HealthPill', () => {
  it.each(STATES)('renders without crashing for state=%s', (state) => {
    const wrapper = mount(HealthPill, { props: { state } });
    expect(wrapper.exists()).toBe(true);
  });

  it('shows the state label by default (non-compact)', () => {
    const wrapper = mount(HealthPill, { props: { state: 'running' } });
    // text() returns the concatenated text of all spans, e.g. "●running"
    expect(wrapper.text()).toContain('running');
    // Ensure the label span is rendered.
    const spans = wrapper.findAll('span');
    expect(spans.some((s) => s.text() === 'running')).toBe(true);
  });

  it('hides label in compact mode', () => {
    const wrapper = mount(HealthPill, { props: { state: 'failed', compact: true } });
    // In compact mode, the label span is v-if'd out.
    const labelSpan = wrapper.find('[class*="uppercase"]');
    expect(labelSpan.exists()).toBe(false);
  });

  it('uses custom label when provided', () => {
    const wrapper = mount(HealthPill, { props: { state: 'starting', label: 'warming' } });
    expect(wrapper.text()).toContain('warming');
  });

  it('applies signal-ok colour class for running state', () => {
    const wrapper = mount(HealthPill, { props: { state: 'running' } });
    expect(wrapper.html()).toContain('signal-ok');
  });

  it('applies signal-warn colour class for starting state', () => {
    const wrapper = mount(HealthPill, { props: { state: 'starting' } });
    expect(wrapper.html()).toContain('signal-warn');
  });

  it('applies signal-warn colour class for restarting state', () => {
    const wrapper = mount(HealthPill, { props: { state: 'restarting' } });
    expect(wrapper.html()).toContain('signal-warn');
  });

  it('applies signal-danger colour class for failed state', () => {
    const wrapper = mount(HealthPill, { props: { state: 'failed' } });
    expect(wrapper.html()).toContain('signal-danger');
  });

  it('applies ink-dim colour class for stopped state', () => {
    const wrapper = mount(HealthPill, { props: { state: 'stopped' } });
    expect(wrapper.html()).toContain('ink-dim');
  });

  it('has role=status for accessibility', () => {
    const wrapper = mount(HealthPill, { props: { state: 'running' } });
    expect(wrapper.attributes('role')).toBe('status');
  });

  it('includes aria-label describing the state', () => {
    const wrapper = mount(HealthPill, { props: { state: 'failed' } });
    expect(wrapper.attributes('aria-label')).toContain('failed');
  });

  it('renders the dot character', () => {
    const wrapper = mount(HealthPill, { props: { state: 'running' } });
    expect(wrapper.html()).toContain('●');
  });
});
