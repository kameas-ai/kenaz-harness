import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import PlanApprovalModal from '@/views/sessions/PlanApprovalModal.vue';

// ── Wails window.go stub ───────────────────────────────────────────────────

interface FakeHarnessAPI {
  Planmode_Approve: ReturnType<typeof vi.fn>;
  Planmode_Discard: ReturnType<typeof vi.fn>;
  Planmode_Edit: ReturnType<typeof vi.fn>;
}

function installFakeGo(overrides: Partial<FakeHarnessAPI> = {}): FakeHarnessAPI {
  const api: FakeHarnessAPI = {
    Planmode_Approve: vi.fn().mockResolvedValue({ approved: true, session_id: 'sess', plan_id: 'plan-001' }),
    Planmode_Discard: vi.fn().mockResolvedValue({ approved: false, reason: 'discarded', session_id: 'sess', plan_id: 'plan-001' }),
    Planmode_Edit: vi.fn().mockResolvedValue({ approved: true, session_id: 'sess', plan_id: 'plan-001' }),
    ...overrides,
  };
  (window as unknown as { go: { rpc: { HarnessAPI: FakeHarnessAPI } } }).go = {
    rpc: { HarnessAPI: api },
  };
  return api;
}

function uninstallFakeGo() {
  delete (window as unknown as { go?: unknown }).go;
}

// ── Mount helper ───────────────────────────────────────────────────────────

function mountModal(
  props: { sessionId: string; planId: string; plan: string } = {
    sessionId: 'sess-1',
    planId: 'plan-001',
    plan: '# My Plan\n\nStep 1: do something.',
  },
) {
  return mount(PlanApprovalModal, { props });
}

// ── Tests ─────────────────────────────────────────────────────────────────

describe('PlanApprovalModal', () => {
  beforeEach(() => {
    installFakeGo();
  });

  afterEach(() => {
    uninstallFakeGo();
  });

  it('renders the modal with plan text', () => {
    const w = mountModal();
    expect(w.find('[data-testid="plan-approval-modal"]').exists()).toBe(true);
    expect(w.find('[data-testid="plan-approval-modal-plan-text"]').text()).toContain('Step 1: do something.');
    w.unmount();
  });

  it('has aria role="dialog" and aria-modal="true"', () => {
    const w = mountModal();
    const modal = w.find('[data-testid="plan-approval-modal"]');
    expect(modal.attributes('role')).toBe('dialog');
    expect(modal.attributes('aria-modal')).toBe('true');
    w.unmount();
  });

  it('calls Planmode_Approve and emits approved on Approve click', async () => {
    const api = installFakeGo();
    const w = mountModal();

    await w.find('[data-testid="plan-approval-modal-approve"]').trigger('click');
    await flushPromises();

    expect(api.Planmode_Approve).toHaveBeenCalledWith({
      session_id: 'sess-1',
      plan_id: 'plan-001',
    });
    expect(w.emitted('approved')).toBeTruthy();
    expect(w.emitted('approved')![0]).toEqual(['plan-001']);
    w.unmount();
  });

  it('calls Planmode_Discard and emits discarded on Discard click', async () => {
    const api = installFakeGo();
    const w = mountModal();

    await w.find('[data-testid="plan-approval-modal-discard"]').trigger('click');
    await flushPromises();

    expect(api.Planmode_Discard).toHaveBeenCalledWith({
      session_id: 'sess-1',
      plan_id: 'plan-001',
    });
    expect(w.emitted('discarded')).toBeTruthy();
    expect(w.emitted('discarded')![0]).toEqual(['plan-001']);
    w.unmount();
  });

  it('shows textarea after Edit click and hides it after Cancel edit', async () => {
    const w = mountModal();

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
    const api = installFakeGo();
    const w = mountModal();

    // Enter edit mode
    await w.find('[data-testid="plan-approval-modal-edit"]').trigger('click');
    await flushPromises();

    const textarea = w.find<HTMLTextAreaElement>('[data-testid="plan-approval-modal-editor"]');
    await textarea.setValue('# Edited Plan\n\nNew content.');

    await w.find('[data-testid="plan-approval-modal-save-edit"]').trigger('click');
    await flushPromises();

    expect(api.Planmode_Edit).toHaveBeenCalledWith({
      session_id: 'sess-1',
      plan_id: 'plan-001',
      edited_plan: '# Edited Plan\n\nNew content.',
    });
    expect(w.emitted('edited')).toBeTruthy();
    expect(w.emitted('edited')![0]).toEqual(['plan-001', '# Edited Plan\n\nNew content.']);
    w.unmount();
  });

  it('Save & approve button is disabled when edited text is empty', async () => {
    const w = mountModal();

    await w.find('[data-testid="plan-approval-modal-edit"]').trigger('click');
    await flushPromises();

    const textarea = w.find<HTMLTextAreaElement>('[data-testid="plan-approval-modal-editor"]');
    await textarea.setValue('   ');

    const saveBtn = w.find<HTMLButtonElement>('[data-testid="plan-approval-modal-save-edit"]');
    expect(saveBtn.element.disabled).toBe(true);
    w.unmount();
  });

  it('shows error notice and emits error when Approve RPC fails', async () => {
    installFakeGo({
      Planmode_Approve: vi.fn().mockRejectedValue(new Error('server unavailable')),
    });
    const w = mountModal();

    await w.find('[data-testid="plan-approval-modal-approve"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="plan-approval-modal-error"]').exists()).toBe(true);
    expect(w.find('[data-testid="plan-approval-modal-error"]').text()).toContain('server unavailable');
    expect(w.emitted('error')).toBeTruthy();
    w.unmount();
  });

  it('shows error notice and emits error when Discard RPC fails', async () => {
    installFakeGo({
      Planmode_Discard: vi.fn().mockRejectedValue(new Error('discard failed')),
    });
    const w = mountModal();

    await w.find('[data-testid="plan-approval-modal-discard"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="plan-approval-modal-error"]').exists()).toBe(true);
    expect(w.find('[data-testid="plan-approval-modal-error"]').text()).toContain('discard failed');
    w.unmount();
  });

  it('does not dismiss on overlay click (non-dismissive spec FR-010)', async () => {
    const w = mountModal();
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
    installFakeGo({
      Planmode_Approve: vi.fn().mockReturnValue(approvePromise),
    });
    const w = mountModal();

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
});
