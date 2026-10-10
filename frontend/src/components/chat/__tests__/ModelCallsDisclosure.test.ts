/**
 * ModelCallsDisclosure (llm-call-telemetry-01LLMTEL01 WP02): the read-only
 * "Model calls" view under the cost readout reads Sessions_ModelCalls for
 * the session when opened, renders each call's model, route, tokens,
 * latency, cost and outcome, and fetches nothing in served mode.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ModelCallsDisclosure from '@/components/chat/ModelCallsDisclosure.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { ModelCall } from '@/lib/types';

function call(o: Partial<ModelCall>): ModelCall {
  return {
    spanId: 'span',
    startedAtMs: 1_760_000_000_000,
    durationMs: 1500,
    status: 'ok',
    provider: 'openrouter',
    requestModel: 'openai/gpt-4o',
    responseModel: '',
    responseId: 'gen-1',
    upstream: '',
    finishReasons: ['stop'],
    errorType: '',
    attempt: 0,
    fallback: false,
    inputTokens: 1200,
    outputTokens: 34,
    cacheReadTokens: 800,
    cacheWriteTokens: 0,
    reasoningTokens: 0,
    ttftMs: 230,
    costUsd: 0.0123,
    statsState: '',
    providerLatencyMs: null,
    providerGenerationTimeMs: null,
    providerCostUsd: null,
    nativeInputTokens: null,
    nativeOutputTokens: null,
    nativeReasoningTokens: null,
    nativeCachedTokens: null,
    nativeFinishReason: '',
    ...o,
  };
}

function setup(calls: ModelCall[], servedMode = false) {
  const base = createFakeHarnessClient();
  const modelCalls = vi.fn(async () => calls);
  const client = { ...base, sessions: { ...base.sessions, modelCalls } };
  const w = mount(ModelCallsDisclosure, {
    props: { sessionId: 'sess-1', servedMode },
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { w, modelCalls };
}

describe('ModelCallsDisclosure', () => {
  it('stays closed and fetches nothing until opened', () => {
    const { w, modelCalls } = setup([call({})]);
    expect(w.find('[data-testid="model-calls-panel"]').exists()).toBe(false);
    expect(modelCalls).not.toHaveBeenCalled();
  });

  it('lists the session calls with tokens, latency, cost, route and outcome', async () => {
    const { w, modelCalls } = setup([
      call({
        spanId: 'b',
        status: 'error',
        errorType: 'rate_limited',
        finishReasons: [],
        attempt: 1,
        costUsd: null,
        inputTokens: 0,
        outputTokens: 0,
        cacheReadTokens: 0,
        ttftMs: 0,
      }),
      call({
        spanId: 'a',
        responseModel: 'openai/gpt-4o-2024-08-06',
        upstream: 'azure',
        statsState: 'fetched',
        providerLatencyMs: 812,
        providerGenerationTimeMs: 1490,
        providerCostUsd: 0.0042,
        nativeInputTokens: 1201,
        nativeOutputTokens: 35,
      }),
    ]);
    await w.find('[data-testid="model-calls-toggle"]').trigger('click');
    await flushPromises();
    expect(modelCalls).toHaveBeenCalledWith('sess-1', 50);
    expect(w.find('[data-testid="model-calls-summary"]').text()).toBe('2 calls · 1 failed');

    const ok = w.find('[data-testid="model-call-a"]');
    expect(ok.find('[data-testid="model-call-model"]').text()).toBe('openai/gpt-4o-2024-08-06');
    expect(ok.find('[data-testid="model-call-route"]').text()).toBe('openrouter → azure');
    expect(ok.find('[data-testid="model-call-tokens"]').text()).toContain('in 1.2k · out 34 · cached 800');
    expect(ok.find('[data-testid="model-call-latency"]').text()).toBe('1.5 s · ttft 230 ms');
    // The provider-reported cost from the stats pull wins over the readout.
    expect(ok.find('[data-testid="model-call-cost"]').text()).toBe('$0.0042');
    expect(ok.find('[data-testid="model-call-outcome"]').text()).toBe('stop');
    expect(ok.find('[data-testid="model-call-provider-stats"]').text()).toContain('latency 812 ms');
    expect(ok.find('[data-testid="model-call-provider-stats"]').text()).toContain('generation 1.5 s');

    const bad = w.find('[data-testid="model-call-b"]');
    expect(bad.find('[data-testid="model-call-outcome"]').text()).toBe('rate_limited');
    expect(bad.find('[data-testid="model-call-cost"]').text()).toBe('—');
    expect(bad.find('[data-testid="model-call-attempt"]').text()).toContain('attempt 2');
    expect(bad.find('[data-testid="model-call-provider-stats"]').exists()).toBe(false);
  });

  it('re-reads on every reopen and says so when nothing is recorded', async () => {
    const { w, modelCalls } = setup([]);
    const toggle = w.find('[data-testid="model-calls-toggle"]');
    await toggle.trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="model-calls-empty"]').exists()).toBe(true);
    await toggle.trigger('click');
    await toggle.trigger('click');
    await flushPromises();
    expect(modelCalls).toHaveBeenCalledTimes(2);
  });

  it('shows the boundary panel in served mode and fetches nothing', async () => {
    const { w, modelCalls } = setup([call({})], true);
    await w.find('[data-testid="model-calls-toggle"]').trigger('click');
    await flushPromises();
    expect(modelCalls).not.toHaveBeenCalled();
    expect(w.find('[data-testid="model-calls-panel"]').text()).toContain('Model calls');
    expect(w.find('[data-testid="model-call-span"]').exists()).toBe(false);
  });
});
