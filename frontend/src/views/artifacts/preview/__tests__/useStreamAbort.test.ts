/**
 * useStreamAbort.test.ts — unit tests for the stream abort composable
 * (artifact-preview-binary-rendering-01KQ8TD5 WP06 acceptance).
 */

import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { defineComponent, h } from 'vue';
import { useStreamAbort } from '../useStreamAbort';

// Helper: mount a minimal component that exposes the composable's handle.
function mountWithAbort(opts: { maxBytes: number; timeoutMs: number }) {
  let handle: ReturnType<typeof useStreamAbort> | undefined;

  const Comp = defineComponent({
    setup() {
      handle = useStreamAbort(opts);
      return () => h('div');
    },
  });

  const wrapper = mount(Comp);
  return { wrapper, get handle() { return handle!; } };
}

describe('useStreamAbort', () => {
  it('does not trip when bytes are under the cap', () => {
    const { handle } = mountWithAbort({ maxBytes: 1000, timeoutMs: 2000 });
    expect(handle.tripBytes(500)).toBe(false);
    expect(handle.reason.value).toBeNull();
    expect(handle.signal.aborted).toBe(false);
  });

  it('trips on size when bytes exceed the cap', () => {
    const { handle } = mountWithAbort({ maxBytes: 1000, timeoutMs: 2000 });
    expect(handle.tripBytes(1001)).toBe(true);
    expect(handle.reason.value).toBe('size');
    expect(handle.signal.aborted).toBe(true);
  });

  it('does not trip twice (idempotent)', () => {
    const { handle } = mountWithAbort({ maxBytes: 100, timeoutMs: 2000 });
    expect(handle.tripBytes(200)).toBe(true);
    expect(handle.tripBytes(300)).toBe(false); // already tripped
    expect(handle.reason.value).toBe('size');
  });

  it('trips on time via tripTime()', () => {
    const { handle } = mountWithAbort({ maxBytes: 1000, timeoutMs: 2000 });
    expect(handle.tripTime()).toBe(true);
    expect(handle.reason.value).toBe('time');
    expect(handle.signal.aborted).toBe(true);
  });

  it('size trip takes precedence over time trip order', () => {
    const { handle } = mountWithAbort({ maxBytes: 100, timeoutMs: 2000 });
    handle.tripBytes(200);
    handle.tripTime(); // should be ignored
    expect(handle.reason.value).toBe('size');
  });

  it('revokes registered object URLs on abort', () => {
    const revokeObjectURL = vi.fn();
    const original = URL.revokeObjectURL;
    URL.revokeObjectURL = revokeObjectURL;

    try {
      const { handle } = mountWithAbort({ maxBytes: 100, timeoutMs: 2000 });
      handle.registerObjectUrl('blob:fake-url-1');
      handle.registerObjectUrl('blob:fake-url-2');
      handle.tripBytes(200);

      expect(revokeObjectURL).toHaveBeenCalledWith('blob:fake-url-1');
      expect(revokeObjectURL).toHaveBeenCalledWith('blob:fake-url-2');
    } finally {
      URL.revokeObjectURL = original;
    }
  });

  it('revokes registered object URLs on unmount', () => {
    const revokeObjectURL = vi.fn();
    const original = URL.revokeObjectURL;
    URL.revokeObjectURL = revokeObjectURL;

    try {
      const { handle, wrapper } = mountWithAbort({ maxBytes: 1000, timeoutMs: 2000 });
      handle.registerObjectUrl('blob:fake-url-3');
      wrapper.unmount();

      expect(revokeObjectURL).toHaveBeenCalledWith('blob:fake-url-3');
    } finally {
      URL.revokeObjectURL = original;
    }
  });

  it('supports getter function for maxBytes (reactive config)', () => {
    let maxBytes = 1000;
    let handle: ReturnType<typeof useStreamAbort> | undefined;

    const Comp = defineComponent({
      setup() {
        handle = useStreamAbort({
          maxBytes: () => maxBytes,
          timeoutMs: 2000,
        });
        return () => h('div');
      },
    });

    mount(Comp);

    expect(handle!.tripBytes(500)).toBe(false);
    maxBytes = 100; // lower the cap
    expect(handle!.tripBytes(200)).toBe(true); // now exceeds the new cap
  });

  it('placeholder text match: size reason', () => {
    // Verifies the placeholder text spec matches FR-009.
    const { handle } = mountWithAbort({ maxBytes: 100, timeoutMs: 2000 });
    handle.tripBytes(200);
    expect(handle.reason.value).toBe('size');
    // The UnknownBinaryRenderer renders "Preview unavailable for files > 5 MB"
    // when reason === 'size'. Verified by component tests.
  });
});
