/**
 * CustomEndpointFields.spec.ts
 *
 * Snapshot + interaction tests for the custom-openai endpoint form
 * sub-component (custom-openai-compatible-endpoint-01KQ8VN0 WP06).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import CustomEndpointFields from '@/views/providers/CustomEndpointFields.vue';
import {
  createFakeHarnessClient,
  type HarnessClient,
} from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { CustomTemplateSummary, CustomCapabilityMatrix } from '@/lib/types';

function makeClient(overrides: Partial<HarnessClient['llm']> = {}): HarnessClient {
  const fake = createFakeHarnessClient();
  Object.assign(fake.llm, overrides);
  return fake;
}

function mountFields(client: HarnessClient) {
  return mount(CustomEndpointFields, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
}

const GROQ_TEMPLATE: CustomTemplateSummary = {
  id: 'groq',
  name: 'Groq',
  base_url: 'https://api.groq.com/openai/v1',
  auth_scheme: 'bearer',
};

const SUCCESS_MATRIX: CustomCapabilityMatrix = {
  endpoint: 'https://api.groq.com/openai/v1',
  probed_at: 1000000,
  streaming: 'true',
  tool_calling: 'true',
  streaming_usage: 'false',
};

const PARTIAL_MATRIX: CustomCapabilityMatrix = {
  endpoint: 'https://api.example.com/v1',
  probed_at: 1000001,
  streaming: 'true',
  tool_calling: 'unknown',
  streaming_usage: 'unknown',
};

const ALL_UNKNOWN_MATRIX: CustomCapabilityMatrix = {
  endpoint: 'https://api.mylocal.dev/v1',
  probed_at: 1000002,
  streaming: 'unknown',
  tool_calling: 'unknown',
  streaming_usage: 'unknown',
};

describe('CustomEndpointFields', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders base URL and model fields', () => {
    const client = makeClient();
    const wrapper = mountFields(client);
    expect(wrapper.find('[data-testid="custom-base-url"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="custom-model"]').exists()).toBe(true);
  });

  it('renders auth scheme toggle with all 4 options', () => {
    const client = makeClient();
    const wrapper = mountFields(client);
    for (const scheme of ['bearer', 'api-key-header', 'custom', 'none']) {
      expect(
        wrapper.find(`[data-testid="custom-auth-scheme-${scheme}"]`).exists(),
      ).toBe(true);
    }
  });

  it('shows API key field for bearer auth and hides it for none', async () => {
    const client = makeClient();
    const wrapper = mountFields(client);
    // default is bearer — key field should be present
    expect(wrapper.find('[data-testid="custom-api-key"]').exists()).toBe(true);
    // switch to none
    await wrapper
      .find('[data-testid="custom-auth-scheme-none"]')
      .trigger('click');
    expect(wrapper.find('[data-testid="custom-api-key"]').exists()).toBe(false);
  });

  it('shows template chips when listCustomTemplates returns entries', async () => {
    const client = makeClient({
      listCustomTemplates: vi.fn(async () => [GROQ_TEMPLATE]),
    });
    const wrapper = mountFields(client);
    await flushPromises();
    expect(
      wrapper.find('[data-testid="custom-template-groq"]').exists(),
    ).toBe(true);
  });

  it('clicking a template chip fills base URL and auth scheme', async () => {
    const client = makeClient({
      listCustomTemplates: vi.fn(async () => [GROQ_TEMPLATE]),
    });
    const wrapper = mountFields(client);
    await flushPromises();
    await wrapper.find('[data-testid="custom-template-groq"]').trigger('click');
    const urlInput = wrapper.find<HTMLInputElement>(
      '[data-testid="custom-base-url"]',
    );
    expect(urlInput.element.value).toBe('https://api.groq.com/openai/v1');
    expect(
      wrapper.find('[data-testid="custom-auth-scheme-bearer"]').classes(),
    ).toContain('bg-surface-2');
  });

  it('probe button is disabled when URL is empty', () => {
    const client = makeClient();
    const wrapper = mountFields(client);
    const btn = wrapper.find('[data-testid="custom-probe-btn"]');
    expect(btn.attributes('disabled')).toBeDefined();
  });

  it('probe button is enabled when URL is a valid URL', async () => {
    const client = makeClient({
      probeCustomEndpoint: vi.fn(async () => ({
        matrix: SUCCESS_MATRIX,
        error: undefined,
      })),
    });
    const wrapper = mountFields(client);
    await wrapper
      .find('[data-testid="custom-base-url"]')
      .setValue('https://api.groq.com/openai/v1');
    const btn = wrapper.find('[data-testid="custom-probe-btn"]');
    expect(btn.attributes('disabled')).toBeUndefined();
  });

  it('renders capability matrix after successful probe — success path', async () => {
    const client = makeClient({
      probeCustomEndpoint: vi.fn(async () => ({
        matrix: SUCCESS_MATRIX,
        error: undefined,
      })),
    });
    const wrapper = mountFields(client);
    await wrapper
      .find('[data-testid="custom-base-url"]')
      .setValue('https://api.groq.com/openai/v1');
    await wrapper.find('[data-testid="custom-probe-btn"]').trigger('click');
    await flushPromises();
    expect(
      wrapper.find('[data-testid="custom-capability-matrix"]').exists(),
    ).toBe(true);
    expect(
      wrapper.find('[data-testid="cap-streaming"]').text(),
    ).toBe('Yes');
    expect(
      wrapper.find('[data-testid="cap-tool-calling"]').text(),
    ).toBe('Yes');
    expect(
      wrapper.find('[data-testid="cap-streaming-usage"]').text(),
    ).toBe('No');
  });

  it('renders capability matrix after partial probe — partial path', async () => {
    const client = makeClient({
      probeCustomEndpoint: vi.fn(async () => ({
        matrix: PARTIAL_MATRIX,
        error: undefined,
      })),
    });
    const wrapper = mountFields(client);
    await wrapper
      .find('[data-testid="custom-base-url"]')
      .setValue('https://api.example.com/v1');
    await wrapper.find('[data-testid="custom-probe-btn"]').trigger('click');
    await flushPromises();
    expect(
      wrapper.find('[data-testid="cap-streaming"]').text(),
    ).toBe('Yes');
    expect(
      wrapper.find('[data-testid="cap-tool-calling"]').text(),
    ).toBe('?');
    expect(
      wrapper.find('[data-testid="cap-streaming-usage"]').text(),
    ).toBe('?');
  });

  it('renders capability matrix — all unknown path', async () => {
    const client = makeClient({
      probeCustomEndpoint: vi.fn(async () => ({
        matrix: ALL_UNKNOWN_MATRIX,
        error: undefined,
      })),
    });
    const wrapper = mountFields(client);
    await wrapper
      .find('[data-testid="custom-base-url"]')
      .setValue('https://api.mylocal.dev/v1');
    await wrapper.find('[data-testid="custom-probe-btn"]').trigger('click');
    await flushPromises();
    expect(
      wrapper.find('[data-testid="cap-streaming"]').text(),
    ).toBe('?');
    expect(
      wrapper.find('[data-testid="cap-tool-calling"]').text(),
    ).toBe('?');
    expect(
      wrapper.find('[data-testid="cap-streaming-usage"]').text(),
    ).toBe('?');
  });

  it('shows probe error when probe returns an error message', async () => {
    const client = makeClient({
      probeCustomEndpoint: vi.fn(async () => ({
        matrix: { ...ALL_UNKNOWN_MATRIX, endpoint: 'https://api.fail.com/v1' },
        error: 'auth failed (status=401)',
      })),
    });
    const wrapper = mountFields(client);
    await wrapper
      .find('[data-testid="custom-base-url"]')
      .setValue('https://api.fail.com/v1');
    await wrapper.find('[data-testid="custom-probe-btn"]').trigger('click');
    await flushPromises();
    expect(
      wrapper.find('[data-testid="custom-probe-error"]').text(),
    ).toContain('auth failed');
  });

  it('emits probed event with matrix on success', async () => {
    const client = makeClient({
      probeCustomEndpoint: vi.fn(async () => ({
        matrix: SUCCESS_MATRIX,
        error: undefined,
      })),
    });
    const wrapper = mountFields(client);
    await wrapper
      .find('[data-testid="custom-base-url"]')
      .setValue('https://api.groq.com/openai/v1');
    await wrapper.find('[data-testid="custom-probe-btn"]').trigger('click');
    await flushPromises();
    const emitted = wrapper.emitted('probed');
    expect(emitted).toBeTruthy();
    expect(emitted![0][0]).toMatchObject({ streaming: 'true' });
  });

  it('emits update:value when base URL changes', async () => {
    const client = makeClient();
    const wrapper = mountFields(client);
    await wrapper
      .find('[data-testid="custom-base-url"]')
      .setValue('https://api.myserver.com/v1');
    const emitted = wrapper.emitted('update:value');
    expect(emitted).toBeTruthy();
    const last = emitted![emitted!.length - 1][0] as { baseURL: string };
    expect(last.baseURL).toBe('https://api.myserver.com/v1');
  });

  it('shows custom auth header field for api-key-header scheme', async () => {
    const client = makeClient();
    const wrapper = mountFields(client);
    await wrapper
      .find('[data-testid="custom-auth-scheme-api-key-header"]')
      .trigger('click');
    expect(
      wrapper.find('[data-testid="custom-auth-header"]').exists(),
    ).toBe(true);
  });
});
