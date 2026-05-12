import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import AuthFailureToast from '@/components/chat/AuthFailureToast.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';
import type { AuthFailedPayload, RotationResult } from '@/lib/types';

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  emit: (topic: string, payload: unknown) => void;
  handlers: Map<string, Set<(payload: unknown) => void>>;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
    handlers,
    EventsOn: (topic, cb) => {
      let s = handlers.get(topic);
      if (!s) {
        s = new Set();
        handlers.set(topic, s);
      }
      s.add(cb);
      return () => s!.delete(cb);
    },
    emit: (topic, payload) => {
      const s = handlers.get(topic);
      if (!s) return;
      for (const cb of s) cb(payload);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

function uninstallRuntime() {
  delete (window as unknown as { runtime?: unknown }).runtime;
}

const samplePayload: AuthFailedPayload = {
  sub_id: 'sub-1',
  session_id: 'sess-1',
  profile_id: 'prof-anthropic',
  provider: 'anthropic',
  model: 'claude-3-5-haiku',
  reason: 'invalid_api_key',
};

const goodResult: RotationResult = {
  success: true,
  latency_ms: 200,
  tested_at: '2026-05-12T00:00:00Z',
  auto_resume_token: 'prof-anthropic',
};

const badResult: RotationResult = {
  success: false,
  message: 'Key rejected by provider',
  latency_ms: 100,
  tested_at: '2026-05-12T00:00:00Z',
};

function mountToast(
  llmOverrides: Record<string, unknown> = {},
  initial: AuthFailedPayload | null = null,
  autoResumeEnabled = true,
) {
  return mount(AuthFailureToast, {
    props: { initial, autoResumeEnabled },
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, {
              llm: {
                listProfiles: vi.fn(),
                testAndRotateKey: vi.fn().mockResolvedValue(goodResult),
                resumeAfterKeyRotation: vi.fn().mockResolvedValue(undefined),
                ...llmOverrides,
              } as never,
            });
          },
        },
      ],
    },
  });
}

describe('AuthFailureToast', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
    vi.restoreAllMocks();
  });

  it('renders nothing when no auth failure is queued', () => {
    const w = mountToast();
    expect(w.find('[data-testid="auth-failure-toast"]').exists()).toBe(false);
  });

  it('renders the toast for an initial seed', () => {
    const w = mountToast({}, samplePayload);
    expect(w.find('[data-testid="auth-failure-toast"]').exists()).toBe(true);
    expect(w.find('[data-testid="auth-failure-body"]').text()).toContain('Anthropic');
  });

  it('appears when provider:auth-failed fires on the broker topic', async () => {
    const rt = installFakeRuntime();
    const w = mountToast();
    rt.emit('provider:auth-failed', samplePayload);
    await flushPromises();
    expect(w.find('[data-testid="auth-failure-toast"]').exists()).toBe(true);
  });

  it('calls testAndRotateKey on Enter in input', async () => {
    const testAndRotateKey = vi.fn().mockResolvedValue(goodResult);
    const resumeAfterKeyRotation = vi.fn().mockResolvedValue(undefined);
    const w = mountToast({ testAndRotateKey, resumeAfterKeyRotation }, samplePayload);
    const input = w.find('[data-testid="auth-failure-key-input"]');
    await input.setValue('sk-new-key-123');
    await input.trigger('keydown', { key: 'Enter' });
    await flushPromises();
    expect(testAndRotateKey).toHaveBeenCalledWith(
      'prof-anthropic',
      'sk-new-key-123',
      'inline-toast',
    );
  });

  it('calls resumeAfterKeyRotation when autoResumeEnabled and token present', async () => {
    const testAndRotateKey = vi.fn().mockResolvedValue(goodResult);
    const resumeAfterKeyRotation = vi.fn().mockResolvedValue(undefined);
    const w = mountToast({ testAndRotateKey, resumeAfterKeyRotation }, samplePayload, true);
    await w.find('[data-testid="auth-failure-key-input"]').setValue('sk-good');
    await w.find('[data-testid="auth-failure-rotate"]').trigger('click');
    await flushPromises();
    expect(resumeAfterKeyRotation).toHaveBeenCalledWith('prof-anthropic');
    // Toast should auto-dismiss after successful rotate+resume.
    expect(w.find('[data-testid="auth-failure-toast"]').exists()).toBe(false);
  });

  it('shows confirmation copy when autoResumeEnabled is false', async () => {
    const testAndRotateKey = vi.fn().mockResolvedValue(goodResult);
    const resumeAfterKeyRotation = vi.fn().mockResolvedValue(undefined);
    const w = mountToast({ testAndRotateKey, resumeAfterKeyRotation }, samplePayload, false);
    await w.find('[data-testid="auth-failure-key-input"]').setValue('sk-good');
    await w.find('[data-testid="auth-failure-rotate"]').trigger('click');
    await flushPromises();
    // Auto-resume disabled → show "resend" copy rather than auto-dismissing.
    expect(resumeAfterKeyRotation).not.toHaveBeenCalled();
    expect(w.find('[data-testid="auth-failure-rotated-ok"]').exists()).toBe(true);
    expect(w.find('[data-testid="auth-failure-rotated-ok"]').text()).toContain('resend');
  });

  it('shows error and keeps toast open on bad key', async () => {
    const testAndRotateKey = vi.fn().mockResolvedValue(badResult);
    const w = mountToast({ testAndRotateKey }, samplePayload);
    await w.find('[data-testid="auth-failure-key-input"]').setValue('sk-bad');
    await w.find('[data-testid="auth-failure-rotate"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="auth-failure-error"]').exists()).toBe(true);
    expect(w.find('[data-testid="auth-failure-toast"]').exists()).toBe(true);
  });

  it('dedupes: two events for same profileID → one toast', async () => {
    const rt = installFakeRuntime();
    const w = mountToast();
    rt.emit('provider:auth-failed', samplePayload);
    rt.emit('provider:auth-failed', { ...samplePayload, reason: 'again' });
    await flushPromises();
    // Queue should have exactly one entry.
    expect((w.vm as unknown as { queue: unknown[] }).queue).toHaveLength(1);
  });

  it('dismiss emits dismissed and removes the toast', async () => {
    const w = mountToast({}, samplePayload);
    await w.find('[data-testid="auth-failure-dismiss"]').trigger('click');
    expect(w.emitted('dismissed')?.[0]).toEqual(['prof-anthropic']);
    expect(w.find('[data-testid="auth-failure-toast"]').exists()).toBe(false);
  });
});
