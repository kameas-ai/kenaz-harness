/**
 * DeliveryBanner / UndeliveredBadge — the request_too_large remedy
 * (tool-context-budget-01TCBUD01 WP06): "Open tools" is offered only for
 * that failure and only where the Tools menu can act (not served mode).
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import DeliveryBanner from '@/components/chat/DeliveryBanner.vue';
import UndeliveredBadge from '@/components/chat/UndeliveredBadge.vue';
import type { DeliveryFailure } from '@/lib/delivery';

const TOO_LARGE: DeliveryFailure = {
  turnSpanId: 'u-1',
  failureClass: 'user_actionable',
  code: 'request_too_large',
  summary: "The request is larger than the model's context window",
};

describe('request_too_large remedy', () => {
  it('the banner names N of M and emits open-tools', async () => {
    const w = mount(DeliveryBanner, {
      props: { failure: TOO_LARGE, sizeContext: { toolsTokens: 90_000, windowTokens: 128_000 } },
    });
    expect(w.find('[data-testid="delivery-banner-reason"]').text()).toContain(
      `Tool definitions use ${(90_000).toLocaleString()} of this model's ${(128_000).toLocaleString()} tokens`,
    );
    await w.find('[data-testid="delivery-banner-tools"]').trigger('click');
    expect(w.emitted('open-tools')).toHaveLength(1);
  });

  it('the banner hides Open tools where the menu cannot act', () => {
    const w = mount(DeliveryBanner, { props: { failure: TOO_LARGE, toolsAvailable: false } });
    expect(w.find('[data-testid="delivery-banner-tools"]').exists()).toBe(false);
    expect(w.find('[data-testid="delivery-banner-reason"]').text()).toContain('Unload tools or pick a larger model');
  });

  it('the badge offers Open tools only on the message a Retry would re-run', async () => {
    const target = mount(UndeliveredBadge, {
      props: { failure: TOO_LARGE, canRetry: true, toolsAvailable: true },
    });
    await target.find('[data-testid="undelivered-open-tools"]').trigger('click');
    expect(target.emitted('open-tools')).toHaveLength(1);

    const older = mount(UndeliveredBadge, {
      props: { failure: TOO_LARGE, canRetry: false, toolsAvailable: true },
    });
    expect(older.find('[data-testid="undelivered-open-tools"]').exists()).toBe(false);
  });
});
