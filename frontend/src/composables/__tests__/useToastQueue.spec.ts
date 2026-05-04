/**
 * useToastQueue.spec.ts — covers WP01 toast queue: push, dismiss,
 * auto-dismiss timer, undo callback flow.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  push,
  dismiss,
  invokeUndo,
  useToastQueue,
  _resetToastQueue,
} from '@/composables/useToastQueue';

describe('useToastQueue', () => {
  beforeEach(() => {
    _resetToastQueue();
    vi.useFakeTimers();
  });

  it('push appends a toast and returns its id', () => {
    const id = push('hello');
    expect(id).toBeGreaterThan(0);
    const { toasts } = useToastQueue();
    expect(toasts.length).toBe(1);
    expect(toasts[0].message).toBe('hello');
  });

  it('auto-dismisses after durationMs', () => {
    push('vanish', { durationMs: 5000 });
    const { toasts } = useToastQueue();
    expect(toasts.length).toBe(1);
    vi.advanceTimersByTime(5001);
    expect(toasts.length).toBe(0);
  });

  it('manual dismiss removes immediately', () => {
    const id = push('x', { durationMs: 60000 });
    const { toasts } = useToastQueue();
    expect(toasts.length).toBe(1);
    dismiss(id);
    expect(toasts.length).toBe(0);
  });

  it('invokeUndo runs callback and removes the toast', async () => {
    const fn = vi.fn();
    const id = push('save', { undoFn: fn });
    await invokeUndo(id);
    expect(fn).toHaveBeenCalledOnce();
    const { toasts } = useToastQueue();
    expect(toasts.length).toBe(0);
  });

  it('invokeUndo on missing id is a no-op', async () => {
    await invokeUndo(99999);
    // no throw
  });
});
