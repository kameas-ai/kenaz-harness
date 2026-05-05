import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import TokenMeterChip from '@/components/chat/TokenMeterChip.vue';

describe('TokenMeterChip (per-message-token-meter-01KR3PQR)', () => {
  // ── Visibility gate ──────────────────────────────────────────────────

  it('renders nothing when promptTokens and completionTokens are absent', () => {
    const w = mount(TokenMeterChip, { props: {} });
    expect(w.find('[data-testid="token-meter-chip"]').exists()).toBe(false);
  });

  it('renders nothing when only promptTokens is provided', () => {
    const w = mount(TokenMeterChip, { props: { promptTokens: 100 } });
    expect(w.find('[data-testid="token-meter-chip"]').exists()).toBe(false);
  });

  it('renders the chip when both promptTokens and completionTokens are provided', () => {
    const w = mount(TokenMeterChip, {
      props: { promptTokens: 1200, completionTokens: 240 },
    });
    expect(w.find('[data-testid="token-meter-chip"]').exists()).toBe(true);
  });

  // ── Label formatting ─────────────────────────────────────────────────

  it('shows token counts in compact k-notation', () => {
    const w = mount(TokenMeterChip, {
      props: { promptTokens: 1200, completionTokens: 240 },
    });
    const btn = w.find('[data-testid="token-meter-chip-button"]');
    expect(btn.text()).toContain('1.2k');
    expect(btn.text()).toContain('240');
    expect(btn.text()).toContain('1.4k tok');
  });

  it('appends exact cost (no tilde) for provider-reported cost', () => {
    const w = mount(TokenMeterChip, {
      props: {
        promptTokens: 1000,
        completionTokens: 200,
        costUsd: 0.012,
        costSource: 'provider',
      },
    });
    const label = w.find('[data-testid="token-meter-chip-button"]').text();
    expect(label).toContain('$0.012');
    expect(label).not.toContain('~$');
  });

  it('appends estimated cost with tilde for derived cost', () => {
    const w = mount(TokenMeterChip, {
      props: {
        promptTokens: 1000,
        completionTokens: 200,
        costUsd: 0.015,
        costSource: 'derived',
      },
    });
    const label = w.find('[data-testid="token-meter-chip-button"]').text();
    expect(label).toContain('~$');
  });

  it('omits cost from label when costSource is "unknown"', () => {
    const w = mount(TokenMeterChip, {
      props: {
        promptTokens: 500,
        completionTokens: 100,
        costUsd: 0.005,
        costSource: 'unknown',
      },
    });
    const label = w.find('[data-testid="token-meter-chip-button"]').text();
    expect(label).not.toContain('$');
  });

  it('omits cost from label when costUsd is 0', () => {
    const w = mount(TokenMeterChip, {
      props: {
        promptTokens: 500,
        completionTokens: 100,
        costUsd: 0,
        costSource: 'derived',
      },
    });
    const label = w.find('[data-testid="token-meter-chip-button"]').text();
    expect(label).not.toContain('$');
  });

  it('formats large token counts with M suffix', () => {
    const w = mount(TokenMeterChip, {
      props: { promptTokens: 1_500_000, completionTokens: 200_000 },
    });
    const label = w.find('[data-testid="token-meter-chip-button"]').text();
    expect(label).toContain('1.5M');
  });

  // ── Popover open/close ───────────────────────────────────────────────

  it('popover is hidden by default', () => {
    const w = mount(TokenMeterChip, {
      props: { promptTokens: 1000, completionTokens: 200 },
    });
    expect(w.find('[data-testid="token-meter-popover"]').exists()).toBe(false);
  });

  it('opens the popover when the chip button is clicked', async () => {
    const w = mount(TokenMeterChip, {
      props: { promptTokens: 1000, completionTokens: 200 },
    });
    await w.find('[data-testid="token-meter-chip-button"]').trigger('click');
    expect(w.find('[data-testid="token-meter-popover"]').exists()).toBe(true);
  });

  it('closes the popover when the close button is clicked', async () => {
    const w = mount(TokenMeterChip, {
      props: { promptTokens: 1000, completionTokens: 200 },
    });
    await w.find('[data-testid="token-meter-chip-button"]').trigger('click');
    expect(w.find('[data-testid="token-meter-popover"]').exists()).toBe(true);
    await w.find('[data-testid="token-meter-popover-close"]').trigger('click');
    expect(w.find('[data-testid="token-meter-popover"]').exists()).toBe(false);
  });

  it('toggles the popover closed on a second chip click', async () => {
    const w = mount(TokenMeterChip, {
      props: { promptTokens: 1000, completionTokens: 200 },
    });
    await w.find('[data-testid="token-meter-chip-button"]').trigger('click');
    expect(w.find('[data-testid="token-meter-popover"]').exists()).toBe(true);
    await w.find('[data-testid="token-meter-chip-button"]').trigger('click');
    expect(w.find('[data-testid="token-meter-popover"]').exists()).toBe(false);
  });

  // ── Popover content ──────────────────────────────────────────────────

  it('popover shows prompt token count', async () => {
    const w = mount(TokenMeterChip, {
      props: { promptTokens: 1234, completionTokens: 56 },
    });
    await w.find('[data-testid="token-meter-chip-button"]').trigger('click');
    expect(w.find('[data-testid="token-meter-prompt"]').text()).toContain('1,234');
  });

  it('popover shows completion token count', async () => {
    const w = mount(TokenMeterChip, {
      props: { promptTokens: 1000, completionTokens: 567 },
    });
    await w.find('[data-testid="token-meter-chip-button"]').trigger('click');
    expect(w.find('[data-testid="token-meter-completion"]').text()).toContain('567');
  });

  it('popover shows total token count', async () => {
    const w = mount(TokenMeterChip, {
      props: { promptTokens: 1000, completionTokens: 200 },
    });
    await w.find('[data-testid="token-meter-chip-button"]').trigger('click');
    expect(w.find('[data-testid="token-meter-total"]').text()).toContain('1,200');
  });

  it('popover shows cost row with tilde for derived source', async () => {
    const w = mount(TokenMeterChip, {
      props: {
        promptTokens: 1000,
        completionTokens: 200,
        costUsd: 0.018,
        costSource: 'derived',
      },
    });
    await w.find('[data-testid="token-meter-chip-button"]').trigger('click');
    const costCell = w.find('[data-testid="token-meter-cost"]');
    expect(costCell.exists()).toBe(true);
    expect(costCell.text()).toContain('~');
  });

  it('popover shows cost source label', async () => {
    const w = mount(TokenMeterChip, {
      props: {
        promptTokens: 1000,
        completionTokens: 200,
        costUsd: 0.018,
        costSource: 'provider',
      },
    });
    await w.find('[data-testid="token-meter-chip-button"]').trigger('click');
    expect(w.find('[data-testid="token-meter-cost-source"]').text()).toContain(
      'Provider-reported',
    );
  });

  it('popover hides cost row when costSource is "unknown"', async () => {
    const w = mount(TokenMeterChip, {
      props: {
        promptTokens: 1000,
        completionTokens: 200,
        costUsd: 0.005,
        costSource: 'unknown',
      },
    });
    await w.find('[data-testid="token-meter-chip-button"]').trigger('click');
    expect(w.find('[data-testid="token-meter-cost"]').exists()).toBe(false);
  });

  // ── Privacy: no raw color literals ──────────────────────────────────

  it('uses no raw color literals (privacy CI invariant #4)', async () => {
    const w = mount(TokenMeterChip, {
      props: {
        promptTokens: 1200,
        completionTokens: 240,
        costUsd: 0.012,
        costSource: 'derived',
      },
    });
    await w.find('[data-testid="token-meter-chip-button"]').trigger('click');
    expect(w.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
  });
});
