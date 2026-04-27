import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import PolicyView, {
  type PolicyDecision,
  type PolicyFile,
} from '@/views/policy/PolicyView.vue';

function file(over: Partial<PolicyFile> = {}): PolicyFile {
  return {
    name: 'default_policy.cedar',
    parse_ok: true,
    embedded: true,
    bytes: 512,
    ...over,
  };
}

function decision(over: Partial<PolicyDecision> = {}): PolicyDecision {
  return {
    outcome: 'allow',
    action: 'tool_exec',
    principal: 'User::"local"',
    resource: 'Tool::"websearch__search"',
    reason: 'permit policy matched',
    matched_policy: 'default_policy.cedar#0#0',
    evaluated_at: '2026-04-26T00:00:00Z',
    ...over,
  };
}

describe('PolicyView (agent-kernel-graph WP14)', () => {
  it('renders the canvas head with section number 14 and title', () => {
    const w = mount(PolicyView, {
      props: { files: [], decisions: [] },
    });
    expect(w.text()).toContain('14');
    expect(w.text()).toContain('POLICY');
    expect(w.text()).toContain('Cedar policy bundle');
  });

  it('renders one entry per policy file with parse status', () => {
    const w = mount(PolicyView, {
      props: {
        files: [
          file({ name: 'default_policy.cedar', embedded: true, parse_ok: true }),
          file({ name: '01_user.cedar', embedded: false, parse_ok: false, parse_err: 'syntax error at line 3' }),
        ],
        decisions: [],
      },
    });
    expect(w.text()).toContain('default_policy.cedar');
    expect(w.text()).toContain('01_user.cedar');
    expect(w.text()).toContain('OK');
    expect(w.text()).toContain('FAILED');
    expect(w.text()).toContain('syntax error at line 3');
  });

  it('renders the recent-decisions list with outcome badges', () => {
    const w = mount(PolicyView, {
      props: {
        files: [],
        decisions: [
          decision({ outcome: 'allow', action: 'tool_exec' }),
          decision({
            outcome: 'deny',
            action: 'memory_write',
            resource: 'Memory::"global"',
            reason: 'forbid policy matched',
          }),
          decision({
            outcome: 'not_applicable',
            action: 'network_request',
            resource: 'Network::"example.com"',
            reason: 'no policy matched',
          }),
        ],
      },
    });
    const text = w.text();
    expect(text).toContain('allow');
    expect(text).toContain('deny');
    expect(text).toContain('not_applicable');
    expect(text).toContain('tool_exec');
    expect(text).toContain('Memory::"global"');
    expect(text).toContain('Network::"example.com"');
  });

  it('renders an empty-state when there are no decisions', () => {
    const w = mount(PolicyView, {
      props: {
        files: [file()],
        decisions: [],
      },
    });
    expect(w.text()).toContain('No decisions yet');
  });

  it('invokes the onReload callback when Reload is clicked', async () => {
    const reload = vi.fn().mockResolvedValue(undefined);
    const w = mount(PolicyView, {
      props: {
        files: [file()],
        decisions: [],
        onReload: reload,
      },
    });
    await w.find('[data-testid="policy-reload"]').trigger('click');
    await flushPromises();
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('disables the Reload button when no callback is supplied', () => {
    const w = mount(PolicyView, {
      props: { files: [], decisions: [] },
    });
    const btn = w.find('[data-testid="policy-reload"]');
    expect(btn.attributes('disabled')).toBeDefined();
  });

  it('surfaces a reload error to the user', async () => {
    const reload = vi.fn().mockRejectedValue(new Error('disk read failed'));
    const w = mount(PolicyView, {
      props: {
        files: [file()],
        decisions: [],
        onReload: reload,
      },
    });
    await w.find('[data-testid="policy-reload"]').trigger('click');
    await flushPromises();
    expect(w.text()).toContain('Reload failed');
    expect(w.text()).toContain('disk read failed');
  });
});
