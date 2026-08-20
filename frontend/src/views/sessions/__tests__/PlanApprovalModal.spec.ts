import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import PlanApprovalModal from '@/views/sessions/PlanApprovalModal.vue';
import { createFakeHarnessClient, createUnsupportedServedClient, type HarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

// ── Mount helper ───────────────────────────────────────────────────────────
//
// trust-surfaces-that-fire-01PMZ202 WP21 / UNIT-19 (AC-15b): the modal now
// routes every RPC through harnessClient (installed via Vue provide/inject)
// instead of the optional-chained `window.go?.rpc?.Bindings?.X` probe that
// silently resolved `undefined` — and therefore `await`ed cleanly, and
// therefore emitted 'approved' — whenever `window.go` was absent (the
// served-mode default). Tests provide a fake client the same way the rest
// of the suite does (see ShareSessionDialog.spec.ts).

function mountModal(
  client: HarnessClient,
  props: { sessionId: string; planId: string; plan: string } = {
    sessionId: 'sess-1',
    planId: 'plan-001',
    plan: '# My Plan\n\nStep 1: do something.',
  },
) {
  return mount(PlanApprovalModal, {
    props,
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
}

// ── Tests ─────────────────────────────────────────────────────────────────

describe('PlanApprovalModal', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('renders the modal with plan text', () => {
    const client = createFakeHarnessClient();
    const w = mountModal(client);
    expect(w.find('[data-testid="plan-approval-modal"]').exists()).toBe(true);
    expect(w.find('[data-testid="plan-approval-modal-plan-text"]').text()).toContain('Step 1: do something.');
    w.unmount();
  });

  it('has aria role="dialog" and aria-modal="true"', () => {
    const client = createFakeHarnessClient();
    const w = mountModal(client);
    const modal = w.find('[data-testid="plan-approval-modal"]');
    expect(modal.attributes('role')).toBe('dialog');
    expect(modal.attributes('aria-modal')).toBe('true');
    w.unmount();
  });

  it('calls Planmode_Approve and emits approved on Approve click', async () => {
    const approve = vi.fn().mockResolvedValue({ approved: true, session_id: 'sess-1', plan_id: 'plan-001' });
    const client = createFakeHarnessClient({ Planmode_Approve: approve });
    const w = mountModal(client);

    await w.find('[data-testid="plan-approval-modal-approve"]').trigger('click');
    await flushPromises();

    expect(approve).toHaveBeenCalledWith({
      session_id: 'sess-1',
      plan_id: 'plan-001',
    });
    expect(w.emitted('approved')).toBeTruthy();
    expect(w.emitted('approved')![0]).toEqual(['plan-001']);
    w.unmount();
  });

  it('calls Planmode_Discard and emits discarded on Discard click', async () => {
    const discard = vi.fn().mockResolvedValue({ approved: false, reason: 'discarded', session_id: 'sess-1', plan_id: 'plan-001' });
    const client = createFakeHarnessClient({ Planmode_Discard: discard });
    const w = mountModal(client);

    await w.find('[data-testid="plan-approval-modal-discard"]').trigger('click');
    await flushPromises();

    expect(discard).toHaveBeenCalledWith({
      session_id: 'sess-1',
      plan_id: 'plan-001',
    });
    expect(w.emitted('discarded')).toBeTruthy();
    expect(w.emitted('discarded')![0]).toEqual(['plan-001']);
    w.unmount();
  });

  it('shows textarea after Edit click and hides it after Cancel edit', async () => {
    const client = createFakeHarnessClient();
    const w = mountModal(client);

    // Initially no editor
    expect(w.find('[data-testid="plan-approval-modal-editor"]').exists()).toBe(false);

    // Click Edit
    await w.find('[data-testid="plan-approval-modal-edit"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="plan-approval-modal-editor"]').exists()).toBe(true);
    expect(w.find('[data-testid="plan-approval-modal-approve"]').exists()).toBe(false);

    // Click Cancel edit
    await w.find('[data-testid="plan-approval-modal-cancel-edit"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="plan-approval-modal-editor"]').exists()).toBe(false);
    expect(w.find('[data-testid="plan-approval-modal-approve"]').exists()).toBe(true);
    w.unmount();
  });

  it('calls Planmode_Edit with edited text and emits edited on Save & approve', async () => {
    const edit = vi.fn().mockResolvedValue({ approved: true, session_id: 'sess-1', plan_id: 'plan-001' });
    const client = createFakeHarnessClient({ Planmode_Edit: edit });
    const w = mountModal(client);

    // Enter edit mode
    await w.find('[data-testid="plan-approval-modal-edit"]').trigger('click');
    await flushPromises();

    const textarea = w.find<HTMLTextAreaElement>('[data-testid="plan-approval-modal-editor"]');
    await textarea.setValue('# Edited Plan\n\nNew content.');

    await w.find('[data-testid="plan-approval-modal-save-edit"]').trigger('click');
    await flushPromises();

    expect(edit).toHaveBeenCalledWith({
      session_id: 'sess-1',
      plan_id: 'plan-001',
      edited_plan: '# Edited Plan\n\nNew content.',
    });
    expect(w.emitted('edited')).toBeTruthy();
    expect(w.emitted('edited')![0]).toEqual(['plan-001', '# Edited Plan\n\nNew content.']);
    w.unmount();
  });

  it('Save & approve button is disabled when edited text is empty', async () => {
    const client = createFakeHarnessClient();
    const w = mountModal(client);

    await w.find('[data-testid="plan-approval-modal-edit"]').trigger('click');
    await flushPromises();

    const textarea = w.find<HTMLTextAreaElement>('[data-testid="plan-approval-modal-editor"]');
    await textarea.setValue('   ');

    const saveBtn = w.find<HTMLButtonElement>('[data-testid="plan-approval-modal-save-edit"]');
    expect(saveBtn.element.disabled).toBe(true);
    w.unmount();
  });

  it('shows error notice and emits error when Approve RPC fails', async () => {
    const client = createFakeHarnessClient({
      Planmode_Approve: vi.fn().mockRejectedValue(new Error('server unavailable')),
    });
    const w = mountModal(client);

    await w.find('[data-testid="plan-approval-modal-approve"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="plan-approval-modal-error"]').exists()).toBe(true);
    expect(w.find('[data-testid="plan-approval-modal-error"]').text()).toContain('server unavailable');
    expect(w.emitted('error')).toBeTruthy();
    w.unmount();
  });

  it('shows error notice and emits error when Discard RPC fails', async () => {
    const client = createFakeHarnessClient({
      Planmode_Discard: vi.fn().mockRejectedValue(new Error('discard failed')),
    });
    const w = mountModal(client);

    await w.find('[data-testid="plan-approval-modal-discard"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="plan-approval-modal-error"]').exists()).toBe(true);
    expect(w.find('[data-testid="plan-approval-modal-error"]').text()).toContain('discard failed');
    w.unmount();
  });

  it('does not dismiss on overlay click (non-dismissive spec FR-010)', async () => {
    const client = createFakeHarnessClient();
    const w = mountModal(client);
    // Trigger click on the outer dialog container (the overlay)
    await w.find('[data-testid="plan-approval-modal"]').trigger('click');
    await flushPromises();
    // Modal must still be present
    expect(w.find('[data-testid="plan-approval-modal"]').exists()).toBe(true);
    expect(w.emitted('approved')).toBeFalsy();
    expect(w.emitted('discarded')).toBeFalsy();
    w.unmount();
  });

  it('disables action buttons while submitting', async () => {
    // Make Approve hang forever so we can inspect the interim disabled state
    let resolveApprove!: () => void;
    const approvePromise = new Promise<void>((res) => { resolveApprove = res; });
    const client = createFakeHarnessClient({
      Planmode_Approve: vi.fn().mockReturnValue(approvePromise),
    });
    const w = mountModal(client);

    await w.find('[data-testid="plan-approval-modal-approve"]').trigger('click');
    // Do NOT flush — submitting should be true now
    await w.vm.$nextTick();

    const approveBtn = w.find<HTMLButtonElement>('[data-testid="plan-approval-modal-approve"]');
    const discardBtn = w.find<HTMLButtonElement>('[data-testid="plan-approval-modal-discard"]');
    expect(approveBtn.element.disabled).toBe(true);
    expect(discardBtn.element.disabled).toBe(true);

    // Resolve and clean up
    resolveApprove();
    await flushPromises();
    w.unmount();
  });

  // ── AC-15b: served mode raises instead of silently resolving ───────────
  //
  // Before this WP, the modal called `window.go?.rpc?.Bindings?.Planmode_
  // Approve(...)` directly. In served mode `window.go` is undefined, so the
  // optional-chained expression evaluated to `undefined`; `await undefined`
  // resolves immediately, the try/catch never sees an error, and
  // emit('approved') fired — the UI reported the plan approved when no RPC
  // was ever sent. createUnsupportedServedClient() is the exact client
  // shape a served-mode browser session gets for any RPC core/serve does
  // not expose (see harnessClient.ts) — Planmode_Approve/_Discard/_Edit are
  // not in that wired subset, so every call rejects with
  // ServedUnsupportedError.
  it('AC-15b: served-mode client rejects Approve — does not emit approved, emits error', async () => {
    const client = createUnsupportedServedClient();
    const w = mountModal(client);

    await w.find('[data-testid="plan-approval-modal-approve"]').trigger('click');
    await flushPromises();

    expect(w.emitted('approved')).toBeFalsy();
    expect(w.emitted('error')).toBeTruthy();
    expect(w.find('[data-testid="plan-approval-modal-error"]').exists()).toBe(true);
    w.unmount();
  });

  it('AC-15b: served-mode client rejects Discard — does not emit discarded, emits error', async () => {
    const client = createUnsupportedServedClient();
    const w = mountModal(client);

    await w.find('[data-testid="plan-approval-modal-discard"]').trigger('click');
    await flushPromises();

    expect(w.emitted('discarded')).toBeFalsy();
    expect(w.emitted('error')).toBeTruthy();
    w.unmount();
  });

  it('AC-15b: served-mode client rejects Edit — does not emit edited, emits error', async () => {
    const client = createUnsupportedServedClient();
    const w = mountModal(client);

    await w.find('[data-testid="plan-approval-modal-edit"]').trigger('click');
    await flushPromises();
    const textarea = w.find<HTMLTextAreaElement>('[data-testid="plan-approval-modal-editor"]');
    await textarea.setValue('# Edited Plan\n\nNew content.');
    await w.find('[data-testid="plan-approval-modal-save-edit"]').trigger('click');
    await flushPromises();

    expect(w.emitted('edited')).toBeFalsy();
    expect(w.emitted('error')).toBeTruthy();
    w.unmount();
  });
});
