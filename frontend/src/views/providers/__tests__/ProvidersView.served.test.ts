/**
 * Served-mode Providers surface.
 *
 * Before this change the whole view was replaced by
 * "Providers is not available in served mode … run the harness as a desktop
 * app" — advice a workbench user cannot act on, since there is no desktop
 * harness inside the VM. These tests pin the replacement behaviour: state
 * what IS configured, or state how to configure it, and never offer a write
 * affordance that the served transport would reject.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ProvidersView from '@/views/providers/ProvidersView.vue';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import {
  createFakeHarnessClient,
  type HarnessClient,
} from '@/lib/harnessClient';
import type { Provider } from '@/lib/types';
import { ref } from 'vue';

// Controllable served-mode flag; the suite defaults to served = true because
// that is what these tests are about. The desktop regression case flips it.
let servedModeFlag = true;
vi.mock('@/lib/useServedMode', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/useServedMode')>();
  return {
    ...actual,
    isServedMode: () => servedModeFlag,
    useServedMode: () => ref(servedModeFlag),
  };
});

const hostProvider: Provider = {
  id: 'kenaz-host',
  name: 'kenaz-host',
  tier: 'host',
  kind: 'anthropic',
  model: 'claude-sonnet-4-5',
  cred: { kind: 'env', locator: 'ANTHROPIC_API_KEY' },
  source: 'host',
  validated: false,
};

function mountServed(providers: Provider[]) {
  const fake = createFakeHarnessClient();
  const client: HarnessClient = {
    ...fake,
    llm: { ...fake.llm, listProviders: vi.fn().mockResolvedValue(providers) },
  };
  return mount(ProvidersView, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
}

beforeEach(() => {
  servedModeFlag = true;
});

describe('ProvidersView (served mode)', () => {
  it('never shows the desktop-app dead end', async () => {
    const wrapper = mountServed([hostProvider]);
    await flushPromises();

    expect(
      wrapper.find('[data-testid="not-available-in-served-mode"]').exists(),
    ).toBe(false);
    expect(wrapper.text()).not.toContain('not available in served mode');
    expect(wrapper.text()).not.toContain('desktop app');
  });

  it('reports a host-configured provider read-only, naming the kind and where to manage it', async () => {
    const wrapper = mountServed([hostProvider]);
    await flushPromises();

    const notice = wrapper.find('[data-testid="host-provider-notice"]');
    expect(notice.exists()).toBe(true);
    expect(notice.text()).toContain('configured by the host control plane');
    expect(notice.text()).toContain('anthropic');
    expect(notice.text()).toContain('Kenaz');

    // The row itself still renders so the user can see the model in play.
    expect(wrapper.text()).toContain('claude-sonnet-4-5');
    // …and it is tagged "host", not mislabelled "bundle".
    expect(
      wrapper.find('[data-testid="provider-source-tag-kenaz-host"]').text(),
    ).toBe('host');
  });

  it('offers no write affordance for a host provider', async () => {
    const wrapper = mountServed([hostProvider]);
    await flushPromises();

    expect(wrapper.find('[data-testid="open-add-provider"]').exists()).toBe(
      false,
    );
    expect(
      wrapper.find('[data-testid="edit-provider-kenaz-host"]').exists(),
    ).toBe(false);
    // TestProvider is not ported to the served transport — a Test button
    // would reject on click.
    expect(wrapper.text()).not.toContain('Test');
    // served-mode-is-a-real-mode-01PMZ707 WP03, spec.md §5.3: the local-
    // runtime detection cards are a write surface too (Add local runtime) —
    // confirmed hidden, not just the table's own buttons.
    expect(
      wrapper.find('[data-testid="local-runtimes-section"]').exists(),
    ).toBe(false);
  });

  it('tells the user how to fix it when no provider was delivered', async () => {
    const wrapper = mountServed([]);
    await flushPromises();

    const notice = wrapper.find('[data-testid="no-host-provider-notice"]');
    expect(notice.exists()).toBe(true);
    expect(notice.text()).toContain('Kenaz');
    expect(notice.text()).toContain('reopen the workbench');
    // No instruction the user cannot follow from inside the VM.
    expect(wrapper.text()).not.toContain('desktop app');
  });
});

describe('ProvidersView (desktop mode regression)', () => {
  it('keeps the full editable surface', async () => {
    servedModeFlag = false;
    const personal: Provider = {
      id: 'my-own',
      name: 'My Own',
      tier: 'personal',
      model: 'claude-sonnet',
      source: 'personal',
      validated: false,
    };
    const wrapper = mountServed([personal]);
    await flushPromises();

    expect(wrapper.find('[data-testid="open-add-provider"]').exists()).toBe(
      true,
    );
    expect(wrapper.find('[data-testid="edit-provider-my-own"]').exists()).toBe(
      true,
    );
    expect(wrapper.text()).toContain('Test');
    expect(wrapper.find('[data-testid="host-provider-notice"]').exists()).toBe(
      false,
    );
    expect(
      wrapper.find('[data-testid="no-host-provider-notice"]').exists(),
    ).toBe(false);
  });
});
