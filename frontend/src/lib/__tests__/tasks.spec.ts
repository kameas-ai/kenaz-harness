/**
 * tasks.spec.ts — unit tests for the tasks.ts RPC wrappers.
 *
 * Verifies that rpc() resolves window.go.rpc.Bindings (not .API),
 * mirroring the accessor pattern in harnessClient.ts.
 *
 * WP01 fix: tasks.ts was pointing at window.go.rpc.API which does not
 * exist; the Wails-bound struct is Bindings, exposed at
 * window.go.rpc.Bindings.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

describe('tasks.ts — rpc() resolves window.go.rpc.Bindings', () => {
  const originalWindow = globalThis.window;

  afterEach(() => {
    // Restore window to original state after each test
    Object.defineProperty(globalThis, 'window', {
      value: originalWindow,
      writable: true,
      configurable: true,
    });
    vi.resetModules();
  });

  it('throws when window.go is absent (e.g. vitest/jsdom without Wails)', async () => {
    // Simulate no Wails runtime (default vitest/jsdom environment)
    Object.defineProperty(globalThis, 'window', {
      value: { go: undefined },
      writable: true,
      configurable: true,
    });
    vi.resetModules();
    const { tasksList } = await import('@/lib/tasks');
    await expect(tasksList()).rejects.toThrow('window.go.rpc.Bindings is not available');
  });

  it('throws when window.go.rpc.API exists but Bindings does NOT (the old wrong path)', async () => {
    // The old code pointed at .API — verify it would fail if only .API is present
    Object.defineProperty(globalThis, 'window', {
      value: {
        go: {
          rpc: {
            API: {
              Tasks_List: vi.fn().mockResolvedValue([]),
            },
            // Bindings intentionally absent
          },
        },
      },
      writable: true,
      configurable: true,
    });
    vi.resetModules();
    const { tasksList } = await import('@/lib/tasks');
    // With the fix, rpc() requires .Bindings — it should throw because .Bindings is absent
    await expect(tasksList()).rejects.toThrow('window.go.rpc.Bindings is not available');
  });

  it('calls Tasks_List via window.go.rpc.Bindings and returns the result', async () => {
    const mockTasks = [
      { id: 't1', label: 'test task', status: 'running', createdAt: '', updatedAt: '' },
    ];
    const mockBindings = {
      Tasks_List: vi.fn().mockResolvedValue(mockTasks),
      Tasks_Get: vi.fn(),
      Tasks_Tail: vi.fn().mockResolvedValue([]),
      Tasks_Abort: vi.fn().mockResolvedValue(undefined),
    };
    Object.defineProperty(globalThis, 'window', {
      value: {
        go: {
          rpc: {
            Bindings: mockBindings,
          },
        },
      },
      writable: true,
      configurable: true,
    });
    vi.resetModules();
    const { tasksList } = await import('@/lib/tasks');
    const result = await tasksList();
    expect(mockBindings.Tasks_List).toHaveBeenCalledOnce();
    expect(result).toEqual(mockTasks);
  });

  it('calls Tasks_Abort via window.go.rpc.Bindings', async () => {
    const mockBindings = {
      Tasks_List: vi.fn().mockResolvedValue([]),
      Tasks_Get: vi.fn(),
      Tasks_Tail: vi.fn().mockResolvedValue([]),
      Tasks_Abort: vi.fn().mockResolvedValue(undefined),
    };
    Object.defineProperty(globalThis, 'window', {
      value: {
        go: {
          rpc: {
            Bindings: mockBindings,
          },
        },
      },
      writable: true,
      configurable: true,
    });
    vi.resetModules();
    const { tasksAbort } = await import('@/lib/tasks');
    await tasksAbort('t1');
    expect(mockBindings.Tasks_Abort).toHaveBeenCalledWith('t1');
  });

  it('fakeTasksClient export exists and returns sane defaults', async () => {
    vi.resetModules();
    const { fakeTasksClient } = await import('@/lib/tasks');
    expect(fakeTasksClient.list()).toEqual([]);
    expect(fakeTasksClient.get('any')).toBeUndefined();
    expect(fakeTasksClient.tail('any', 0)).toEqual([]);
    expect(() => fakeTasksClient.abort('any')).not.toThrow();
  });
});
