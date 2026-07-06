/**
 * PublishDialog.spec.ts — fleet-share-and-sync-01NDFSEX14 WP03
 *
 * Five specs:
 *   1. renders form fields when open=true
 *   2. does not render when open=false
 *   3. submit calls catalog.publish with correct input
 *   4. submit shows error message on failure
 *   5. cancel button emits close
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import PublishDialog from '../PublishDialog.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { CatalogItemView } from '@/lib/types';

// Stub BaseDialog to avoid focus-trap complexity in jsdom
vi.mock('@/components/ui/BaseDialog.vue', () => ({
  default: {
    props: ['open', 'title'],
    template: `<div v-if="open" data-testid="base-dialog"><slot /></div>`,
  },
}));

const PUBLISHED_ITEM: CatalogItemView = {
  id: 'cat-001',
  kind: 'workflow',
  slug: 'my-workflow',
  version: '1.2.0',
  description: 'A workflow for tests',
  visibility: 'team',
  installed: false,
};

function buildClient(publishFn = vi.fn(async () => PUBLISHED_ITEM)) {
  return {
    client: createFakeHarnessClient({
      catalog: {
        publish: publishFn,
        list: async () => [],
        install: async () => {},
        uninstall: async () => {},
        installed: async () => [],
      },
    }),
    publishFn,
  };
}

function mountDialog(
  open: boolean,
  kind = 'workflow',
  slug = 'my-workflow',
  client = buildClient().client,
) {
  return mount(PublishDialog, {
    props: { open, kind, slug, payloadJson: '{}' },
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
}

describe('PublishDialog', () => {
  it('1. renders form fields when open=true', () => {
    const wrapper = mountDialog(true);

    expect(wrapper.find('[data-testid="publish-dialog"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="publish-version-input"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="publish-description-input"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="publish-visibility-select"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="publish-submit-btn"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="publish-cancel-btn"]').exists()).toBe(true);
  });

  it('2. does not render dialog content when open=false', () => {
    const wrapper = mountDialog(false);
    expect(wrapper.find('[data-testid="publish-dialog"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="publish-version-input"]').exists()).toBe(false);
  });

  it('3. submit calls catalog.publish with form values', async () => {
    const { client, publishFn } = buildClient();
    const wrapper = mountDialog(true, 'workflow', 'my-workflow', client);

    await wrapper.find('[data-testid="publish-version-input"]').setValue('1.2.0');
    await wrapper.find('[data-testid="publish-description-input"]').setValue('A workflow for tests');
    await wrapper.find('[data-testid="publish-visibility-select"]').setValue('team');

    await wrapper.find('[data-testid="publish-submit-btn"]').trigger('click');
    await flushPromises();

    expect(publishFn).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: 'workflow',
        slug: 'my-workflow',
        version: '1.2.0',
        description: 'A workflow for tests',
        visibility: 'team',
      }),
    );
  });

  it('4. shows error message when publish fails', async () => {
    const publishFn = vi.fn(async () => { throw new Error('network error'); });
    const { client } = buildClient(publishFn);
    const wrapper = mountDialog(true, 'workflow', 'my-workflow', client);

    await wrapper.find('[data-testid="publish-description-input"]').setValue('desc');
    await wrapper.find('[data-testid="publish-submit-btn"]').trigger('click');
    await flushPromises();

    const errorEl = wrapper.find('[data-testid="publish-dialog-error"]');
    expect(errorEl.exists()).toBe(true);
    expect(errorEl.text()).toContain('network error');
  });

  it('5. cancel button emits close', async () => {
    const wrapper = mountDialog(true);
    await wrapper.find('[data-testid="publish-cancel-btn"]').trigger('click');
    expect(wrapper.emitted('close')).toBeTruthy();
  });
});
