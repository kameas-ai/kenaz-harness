/**
 * RecoveryCodeFlow.spec.ts — fleet-context-sync-01NDFSEX15 WP07
 *
 * Three specs:
 *   1. generate mode — calls ContextSync_GenerateRecoveryCode, displays code, requires ack
 *   2. apply mode — calls ContextSync_ApplyRecoveryCode with pasted code, emits done
 *   3. apply mode — shows error when ContextSync_ApplyRecoveryCode rejects
 *
 * Note: BaseDialog uses <Teleport to="body">, so we mount with
 * attachTo: document.body and query via document.body.querySelector.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import RecoveryCodeFlow from '@/views/settings/RecoveryCodeFlow.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

// ── vue-router stub ────────────────────────────────────────────────────────
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useRoute: () => undefined,
}));

const FAKE_CODE = 'KENAZ-ABCD1234-EFGH5678-IJKL9012';

function mountFlow(
  mode: 'generate' | 'apply',
  overrides: {
    generateFn?: ReturnType<typeof vi.fn>;
    applyFn?: ReturnType<typeof vi.fn>;
  } = {},
) {
  const generateFn = overrides.generateFn ?? vi.fn(async () => FAKE_CODE);
  const applyFn = overrides.applyFn ?? vi.fn(async () => {});
  const client = createFakeHarnessClient({
    ContextSync_GenerateRecoveryCode: generateFn,
    ContextSync_ApplyRecoveryCode: applyFn,
  });
  const wrapper = mount(RecoveryCodeFlow, {
    props: { open: true, mode },
    attachTo: document.body,
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { wrapper, generateFn, applyFn };
}

// ── helpers ────────────────────────────────────────────────────────────────

function q(testId: string): HTMLElement | null {
  return document.body.querySelector(`[data-testid="${testId}"]`);
}

describe('RecoveryCodeFlow', () => {
  beforeEach(() => {
    // Stub clipboard (not available in happy-dom)
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn(async () => {}) },
      configurable: true,
    });
  });

  afterEach(() => {
    document.body.innerHTML = '';
    vi.clearAllMocks();
  });

  it('1. generate mode: generates code and requires acknowledgement before Done', async () => {
    const { generateFn } = mountFlow('generate');

    // Click generate
    const generateBtn = q('generate-code-btn') as HTMLButtonElement;
    expect(generateBtn).not.toBeNull();
    generateBtn.click();
    await flushPromises();

    expect(generateFn).toHaveBeenCalledOnce();

    // Code should be displayed
    const codeEl = q('recovery-code-display');
    expect(codeEl).not.toBeNull();
    expect(codeEl!.textContent).toContain(FAKE_CODE);

    // Done button should be disabled before acknowledgement
    const doneBtn = q('recovery-done-btn') as HTMLButtonElement;
    expect(doneBtn.disabled).toBe(true);

    // Check the acknowledgement checkbox
    const ackCheckbox = q('recovery-ack-checkbox') as HTMLInputElement;
    expect(ackCheckbox).not.toBeNull();
    ackCheckbox.checked = true;
    ackCheckbox.dispatchEvent(new Event('change'));
    await flushPromises();

    // Done button should now be enabled
    expect((q('recovery-done-btn') as HTMLButtonElement).disabled).toBe(false);

    // Click done
    (q('recovery-done-btn') as HTMLButtonElement).click();
    await flushPromises();
  });

  it('2. apply mode: calls ContextSync_ApplyRecoveryCode and emits done', async () => {
    const { wrapper, applyFn } = mountFlow('apply');

    const textarea = q('apply-code-input') as HTMLTextAreaElement;
    expect(textarea).not.toBeNull();
    textarea.value = FAKE_CODE;
    textarea.dispatchEvent(new Event('input'));
    await flushPromises();

    const applyBtn = q('apply-code-btn') as HTMLButtonElement;
    expect(applyBtn.disabled).toBe(false);
    applyBtn.click();
    await flushPromises();

    expect(applyFn).toHaveBeenCalledWith(FAKE_CODE);
    expect(wrapper.emitted('done')).toBeTruthy();
    expect(wrapper.emitted('close')).toBeTruthy();
  });

  it('3. apply mode: shows error when ContextSync_ApplyRecoveryCode rejects', async () => {
    const applyFn = vi.fn(async () => { throw new Error('invalid recovery code'); });
    const { wrapper } = mountFlow('apply', { applyFn });

    const textarea = q('apply-code-input') as HTMLTextAreaElement;
    textarea.value = FAKE_CODE;
    textarea.dispatchEvent(new Event('input'));
    await flushPromises();

    (q('apply-code-btn') as HTMLButtonElement).click();
    await flushPromises();

    const errEl = q('recovery-error');
    expect(errEl).not.toBeNull();
    expect(errEl!.textContent).toContain('invalid recovery code');
    expect(wrapper.emitted('close')).toBeFalsy();
  });
});
