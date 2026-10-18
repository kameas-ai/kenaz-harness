import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { nextTick } from 'vue';
import BasePermissionModal from '@/components/permissions/BasePermissionModal.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { PermissionRequest } from '@/lib/types';
import { useToastQueue, _resetToastQueue } from '@/composables/useToastQueue';

/**
 * BasePermissionModal — FR-001 / WP01 review migration tests.
 *
 * Verifies that the modal now uses BaseDialog for:
 *   - Escape-to-close → calls decide('deny')
 *   - focus-trap (managed by BaseDialog)
 *   - renders correctly with a request
 */

function makeRequest(overrides?: Partial<PermissionRequest>): PermissionRequest {
  return {
    request_id: 'req-001',
    family: 'bash',
    resource_display: 'rm -rf /tmp/test',
    reason: 'Cleaning up temp files',
    dangerous_tier: false,
    danger_copy: '',
    ...overrides,
  } as PermissionRequest;
}

function mountModal(request: PermissionRequest | null, resolveImpl = vi.fn(async () => undefined)) {
  const client = createFakeHarnessClient({
    permissions: {
      resolve: resolveImpl,
      listGrants: async () => [],
      revokeGrant: async () => undefined,
      listPending: async () => [],
    },
  });

  return mount(BasePermissionModal, {
    props: {
      familyLabel: 'Bash command',
      request,
    },
    attachTo: document.body,
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      // Disable Teleport so content lands in document.body normally.
      stubs: { Teleport: false },
    },
  });
}

describe('BasePermissionModal (FR-001 BaseDialog migration)', () => {
  beforeEach(() => {
    _resetToastQueue();
  });

  afterEach(() => {
    document.body.innerHTML = '';
    vi.restoreAllMocks();
  });

  it('renders the modal when a request is provided', async () => {
    const req = makeRequest();
    const wrapper = mountModal(req);
    await nextTick();

    expect(document.body.querySelector('[data-testid="base-permission-modal"]')).not.toBeNull();
    expect(document.body.textContent).toContain('rm -rf /tmp/test');

    wrapper.unmount();
  });

  it('does not render when request is null', async () => {
    const wrapper = mountModal(null);
    await nextTick();

    expect(document.body.querySelector('[data-testid="base-permission-modal"]')).toBeNull();

    wrapper.unmount();
  });

  it('Escape key calls decide("deny") via BaseDialog close handler', async () => {
    const resolveMock = vi.fn(async () => undefined);
    const req = makeRequest();
    const wrapper = mountModal(req, resolveMock);
    await nextTick();

    // Escape is now handled by BaseDialog (capture listener on document).
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await flushPromises();

    expect(resolveMock).toHaveBeenCalledWith('req-001', 'deny');

    wrapper.unmount();
  });

  it('Allow once button calls decide("allow_once")', async () => {
    const resolveMock = vi.fn(async () => undefined);
    const req = makeRequest();
    const wrapper = mountModal(req, resolveMock);
    await nextTick();

    const btn = document.querySelector('[data-testid="perm-modal-allow-once"]') as HTMLElement;
    expect(btn).not.toBeNull();
    btn.click();
    await flushPromises();

    expect(resolveMock).toHaveBeenCalledWith('req-001', 'allow_once');

    wrapper.unmount();
  });

  it('Deny button calls decide("deny")', async () => {
    const resolveMock = vi.fn(async () => undefined);
    const req = makeRequest();
    const wrapper = mountModal(req, resolveMock);
    await nextTick();

    const btn = document.querySelector('[data-testid="perm-modal-deny"]') as HTMLElement;
    expect(btn).not.toBeNull();
    btn.click();
    await flushPromises();

    expect(resolveMock).toHaveBeenCalledWith('req-001', 'deny');

    wrapper.unmount();
  });

  it('dangerous tier renders danger copy', async () => {
    const req = makeRequest({
      dangerous_tier: true,
      danger_copy: 'This may delete important files.',
    });
    const wrapper = mountModal(req);
    await nextTick();

    const dangerEl = document.querySelector('[data-testid="perm-modal-danger-copy"]');
    expect(dangerEl).not.toBeNull();
    expect(dangerEl!.textContent).toContain('Dangerous');
    expect(dangerEl!.textContent).toContain('This may delete important files.');

    wrapper.unmount();
  });

  it('dismisses and surfaces an honest message when resolve rejects with ErrUnknownRequest (backend restart trap)', async () => {
    // Reproduces the real backend error text: core/policy/cedar/prompt.go's
    // Registry.Resolve returns cedar.ErrUnknownRequest = "cedar/prompt:
    // unknown request id" when the pending entry the modal is holding no
    // longer exists in the (in-process, non-persistent) prompt registry —
    // e.g. because the backend restarted while the prompt was pending.
    const resolveMock = vi.fn(async () => {
      throw new Error('cedar/prompt: unknown request id');
    });
    const req = makeRequest();
    const wrapper = mountModal(req, resolveMock);
    await nextTick();

    const btn = document.querySelector('[data-testid="perm-modal-allow-once"]') as HTMLElement;
    expect(btn).not.toBeNull();
    btn.click();
    await flushPromises();

    // The modal must not be left stuck: it must emit resolved so the
    // parent queue drops the stale request (which hides the modal, since
    // BashPermissionModal/etc. render :request="head" from the queue).
    expect(wrapper.emitted('resolved')).toBeTruthy();
    expect(wrapper.emitted('resolved')![0][0]).toBe('req-001');

    // And it must never report the vanished request as approved.
    const decisionEmitted = wrapper.emitted('resolved')![0][1] as string;
    expect(decisionEmitted).not.toBe('allow_once');
    expect(decisionEmitted).not.toBe('allow_always');

    // The user must be told plainly what happened, in a surface that
    // survives the modal closing (a toast), not just the modal's own
    // (about-to-disappear) inline error strip.
    const { toasts } = useToastQueue();
    expect(toasts.length).toBe(1);
    expect(toasts[0].message.toLowerCase()).toContain('restart');

    wrapper.unmount();
  });

  it('overflow badge shows extra pending count', async () => {
    const req = makeRequest();
    const wrapper = mountModal(req);
    await wrapper.setProps({ queueLength: 3 });
    await nextTick();

    const badge = document.querySelector('[data-testid="perm-modal-overflow"]');
    expect(badge).not.toBeNull();
    expect(badge!.textContent).toContain('+2 more pending');

    wrapper.unmount();
  });
});
