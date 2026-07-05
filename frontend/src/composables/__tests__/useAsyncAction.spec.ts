/**
 * useAsyncAction.spec.ts — covers WP01 optimistic-write failure pathway.
 *
 * Verifies:
 * - Resolving action: no toast, optimistic state preserved.
 * - Rejecting action: revert called, toast pushed with message.
 * - `run` never rejects (errors handled internally).
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { runAsyncAction } from '@/composables/useAsyncAction';
import { useToastQueue, _resetToastQueue } from '@/composables/useToastQueue';

describe('useAsyncAction / runAsyncAction', () => {
  beforeEach(() => {
    _resetToastQueue();
    vi.useFakeTimers();
  });

  it('resolving action: optimistic called, no revert, no toast', async () => {
    const optimistic = vi.fn();
    const revert = vi.fn();

    const result = await runAsyncAction({
      optimistic,
      revert,
      action: async () => 'ok',
      errorLabel: 'Save test',
    });

    expect(optimistic).toHaveBeenCalledOnce();
    expect(revert).not.toHaveBeenCalled();
    expect(result.error).toBe(false);
    expect(result.value).toBe('ok');

    const { toasts } = useToastQueue();
    expect(toasts.length).toBe(0);
  });

  it('rejecting action: revert called, toast pushed', async () => {
    const optimistic = vi.fn();
    const revert = vi.fn();

    const result = await runAsyncAction({
      optimistic,
      revert,
      action: async () => {
        throw new Error('disk full');
      },
      errorLabel: 'Save settings',
    });

    expect(optimistic).toHaveBeenCalledOnce();
    expect(revert).toHaveBeenCalledOnce();
    expect(result.error).toBe(true);

    const { toasts } = useToastQueue();
    expect(toasts.length).toBe(1);
    expect(toasts[0].message).toContain('Save settings');
    expect(toasts[0].message).toContain('disk full');
  });

  it('run never rejects', async () => {
    await expect(
      runAsyncAction({
        action: async () => {
          throw new Error('boom');
        },
        errorLabel: 'Test',
      }),
    ).resolves.not.toThrow();
  });

  it('custom toastMessage overrides default', async () => {
    await runAsyncAction({
      action: async () => {
        throw new Error('inner error');
      },
      errorLabel: 'Foo',
      toastMessage: () => 'Custom message shown to user',
    });

    const { toasts } = useToastQueue();
    expect(toasts.length).toBe(1);
    expect(toasts[0].message).toBe('Custom message shown to user');
  });

  it('undoFn is attached to the toast on rejection', async () => {
    const undoFn = vi.fn();
    await runAsyncAction({
      action: async () => {
        throw new Error('fail');
      },
      errorLabel: 'Toggle',
      undoFn,
    });

    const { toasts } = useToastQueue();
    expect(toasts.length).toBe(1);
    expect(toasts[0].undoFn).toBeDefined();
  });

  it('no optimistic or revert: action still runs and surfaces toast on failure', async () => {
    const result = await runAsyncAction({
      action: async () => {
        throw new Error('bare failure');
      },
      errorLabel: 'Bare',
    });

    expect(result.error).toBe(true);
    const { toasts } = useToastQueue();
    expect(toasts.length).toBe(1);
  });
});
