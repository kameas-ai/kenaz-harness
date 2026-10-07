/**
 * fleet-session-truth-01DOGF0A review follow-ups (frontend): F5 re-auth keeps
 * the gates open; F8 SessionExpiredBanner reads the shared store.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import SessionExpiredBanner from '@/components/ui/SessionExpiredBanner.vue';
import { fakeFleetSession } from '@/lib/harnessClient';
import { _resetFleetSessionForTest, applyFleetSession } from '@/lib/fleetSession';
import { capability, signedIn } from '@/lib/featureFlags';

const caps = { tier: 'enterprise', enabled: { sites_hosting: true }, fetchedAt: '', source: 'fleet' };

describe('review F5 — a re-auth does not close the gates', () => {
  beforeEach(() => _resetFleetSessionForTest());
  afterEach(() => _resetFleetSessionForTest());

  it('signing_in with usable tokens keeps signedIn + capabilities', () => {
    applyFleetSession(fakeFleetSession({ state: 'signing_in', tokensUsable: true, capabilities: caps }));
    expect(signedIn.value).toBe(true);
    expect(capability('sites_hosting')).toBe(true);
  });

  it('signing_in from signed out keeps every gate closed', () => {
    applyFleetSession(fakeFleetSession({ state: 'signing_in', tokensUsable: false, capabilities: caps }));
    expect(signedIn.value).toBe(false);
    expect(capability('sites_hosting')).toBe(false);
  });
});

describe('review F8 — SessionExpiredBanner reads the shared session', () => {
  beforeEach(() => _resetFleetSessionForTest());
  afterEach(() => _resetFleetSessionForTest());

  async function mountBanner() {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] });
    const w = mount(SessionExpiredBanner, { global: { plugins: [router] } });
    await flushPromises();
    return w;
  }

  it('shows for signed_out/session_expired and clears itself when the session recovers', async () => {
    const w = await mountBanner();
    expect(w.find('[data-testid="session-expired-banner"]').exists()).toBe(false);
    applyFleetSession(fakeFleetSession({ state: 'signed_out', reason: 'session_expired' }));
    await flushPromises();
    expect(w.find('[data-testid="session-expired-banner"]').exists()).toBe(true);
    // The backend's recovery probe succeeded: no stale "expired" banner.
    applyFleetSession(fakeFleetSession({ state: 'signed_in', tokensUsable: true }));
    await flushPromises();
    expect(w.find('[data-testid="session-expired-banner"]').exists()).toBe(false);
  });

  it('dismiss hides it until the next expiry', async () => {
    const w = await mountBanner();
    applyFleetSession(fakeFleetSession({ state: 'signed_out', reason: 'session_expired' }));
    await flushPromises();
    await w.find('[data-testid="session-expired-dismiss"]').trigger('click');
    expect(w.find('[data-testid="session-expired-banner"]').exists()).toBe(false);
    applyFleetSession(fakeFleetSession({ state: 'signed_in', tokensUsable: true }));
    await flushPromises();
    applyFleetSession(fakeFleetSession({ state: 'signed_out', reason: 'session_expired' }));
    await flushPromises();
    expect(w.find('[data-testid="session-expired-banner"]').exists()).toBe(true);
  });

  // device-keys-handoff-01DEVKH01 WP06: the node_removed terminal state.
  it('shows "removed by an org admin" for signed_out/node_removed', async () => {
    const w = await mountBanner();
    applyFleetSession(fakeFleetSession({ state: 'signed_out', reason: 'node_removed' }));
    await flushPromises();
    const banner = w.find('[data-testid="session-expired-banner"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('Removed by an org admin');
    expect(w.find('[data-testid="session-expired-signin"]').exists()).toBe(true);
  });
});

