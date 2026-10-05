/**
 * fleetSession.spec.ts — the shared fleet-session store
 * (fleet-session-truth-01DOGF0A WP03), driven with a fake client and the
 * served event bus as the fake event source.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { flushPromises } from '@vue/test-utils';
import {
  FOCUS_RETRY_MS,
  _resetFleetSessionForTest,
  fleetSessionCapability,
  fleetSessionState,
  fleetSignedIn,
  useFleetSession,
} from '@/lib/fleetSession';
import { fakeFleetSession } from '@/lib/harnessClient';
import { dispatchServedEvent } from '@/lib/useServedEvents';
import type { FleetSessionView } from '@/lib/types';

function source(initial: FleetSessionView) {
  let current = initial;
  return {
    set: (v: FleetSessionView) => {
      current = v;
    },
    client: {
      settings: {
        fleetSession: vi.fn(async () => current),
        fleetRefreshIdentity: vi.fn(async () => ({})),
        fleetSignIn: vi.fn(async () => ({})),
        fleetSignOut: vi.fn(async () => {}),
      },
    },
  };
}

describe('fleetSession store', () => {
  beforeEach(() => _resetFleetSessionForTest());
  afterEach(() => _resetFleetSessionForTest());

  it('starts unknown, then reads the backend snapshot once', async () => {
    const s = source(fakeFleetSession({ state: 'signed_in' }));
    expect(fleetSessionState.value).toBe('unknown');
    useFleetSession(s.client);
    useFleetSession(s.client); // a second consumer does not re-read
    await flushPromises();
    expect(fleetSessionState.value).toBe('signed_in');
    expect(s.client.settings.fleetSession).toHaveBeenCalledTimes(1);
  });

  it('degraded counts as signed in (FR-3)', async () => {
    const s = source(fakeFleetSession({ state: 'degraded', reason: 'network' }));
    useFleetSession(s.client);
    await flushPromises();
    expect(fleetSignedIn.value).toBe(true);
  });

  it('follows fleet:session-changed pushes', async () => {
    const s = source(fakeFleetSession({ state: 'signed_out' }));
    useFleetSession(s.client);
    await flushPromises();
    dispatchServedEvent(
      'fleet:session-changed',
      fakeFleetSession({
        state: 'signed_in',
        capabilities: { tier: 't', enabled: { shared_team_graph: true }, fetchedAt: '', source: 'fleet' },
      }),
    );
    expect(fleetSessionState.value).toBe('signed_in');
    expect(fleetSessionCapability('shared_team_graph')).toBe(true);
  });

  it('a read issued before a push does not roll the store back', async () => {
    let release!: (v: FleetSessionView) => void;
    const slow = new Promise<FleetSessionView>((r) => (release = r));
    const client = {
      settings: {
        fleetSession: vi.fn(() => slow),
        fleetRefreshIdentity: vi.fn(async () => ({})),
        fleetSignIn: vi.fn(async () => ({})),
        fleetSignOut: vi.fn(async () => {}),
      },
    };
    useFleetSession(client);
    dispatchServedEvent('fleet:session-changed', fakeFleetSession({ state: 'signed_in' }));
    release(fakeFleetSession({ state: 'signed_out' }));
    await flushPromises();
    expect(fleetSessionState.value).toBe('signed_in');
  });

  it('capability() is false for a default-deny set and while signed out', async () => {
    const s = source(
      fakeFleetSession({
        state: 'signed_out',
        capabilities: { tier: '', enabled: { sites_hosting: true }, fetchedAt: '', source: 'fleet' },
      }),
    );
    useFleetSession(s.client);
    await flushPromises();
    expect(fleetSessionCapability('sites_hosting')).toBe(false);
  });

  it('FR-4: window focus after the threshold retries a stopped (not-provisioned) session', async () => {
    const old = new Date(Date.now() - FOCUS_RETRY_MS - 1000).toISOString();
    const s = source(
      fakeFleetSession({ state: 'degraded', reason: 'not_provisioned', autoRetry: false, lastAttemptAt: old }),
    );
    useFleetSession(s.client);
    await flushPromises();
    window.dispatchEvent(new Event('focus'));
    await flushPromises();
    expect(s.client.settings.fleetRefreshIdentity).toHaveBeenCalledOnce();
  });

  it('FR-4: window focus inside the threshold only re-reads (no enroll storm)', async () => {
    const recent = new Date().toISOString();
    const s = source(
      fakeFleetSession({ state: 'degraded', reason: 'not_provisioned', autoRetry: false, lastAttemptAt: recent }),
    );
    useFleetSession(s.client);
    await flushPromises();
    window.dispatchEvent(new Event('focus'));
    await flushPromises();
    expect(s.client.settings.fleetRefreshIdentity).not.toHaveBeenCalled();
    expect(s.client.settings.fleetSession).toHaveBeenCalledTimes(2);
  });

  it('a transport error keeps the previous snapshot (no invented "signed out")', async () => {
    const s = source(fakeFleetSession({ state: 'signed_in' }));
    const { refresh } = useFleetSession(s.client);
    await flushPromises();
    s.client.settings.fleetSession.mockRejectedValueOnce(new Error('bridge gone'));
    await refresh();
    expect(fleetSessionState.value).toBe('signed_in');
  });
});
