import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import PlanModeBadge from '@/components/chat/PlanModeBadge.vue';

describe('PlanModeBadge', () => {
  it('renders the badge when isActive is true', () => {
    const wrapper = mount(PlanModeBadge, { props: { isActive: true } });
    expect(wrapper.find('[data-testid="plan-mode-badge"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="plan-mode-badge-chip"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('PLAN MODE');
  });

  it('hides the badge when isActive is false', () => {
    const wrapper = mount(PlanModeBadge, { props: { isActive: false } });
    expect(wrapper.find('[data-testid="plan-mode-badge"]').exists()).toBe(false);
  });

  it('shows "pending approval" chip when pendingPlanId is set', () => {
    const wrapper = mount(PlanModeBadge, {
      props: { isActive: true, pendingPlanId: 'plan-001' },
    });
    expect(wrapper.find('[data-testid="plan-mode-badge-pending"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('pending approval');
  });

  it('hides "pending approval" when pendingPlanId is null', () => {
    const wrapper = mount(PlanModeBadge, {
      props: { isActive: true, pendingPlanId: null },
    });
    expect(wrapper.find('[data-testid="plan-mode-badge-pending"]').exists()).toBe(false);
  });

  it('has an aria role="status" for accessibility', () => {
    const wrapper = mount(PlanModeBadge, { props: { isActive: true } });
    expect(wrapper.find('[data-testid="plan-mode-badge-chip"]').attributes('role')).toBe('status');
  });
});
