/**
 * SessionsView.syncDegraded.test.ts — fleet-session-truth-01DOGF0A WP06, P-7
 * (UI half). Dogfood F7: context-sync appends 404'd for a session the
 * toolbar called "Synced to fleet", and nothing on screen said so. The
 * backend append breaker reports per-session failure into the fleet-session
 * snapshot; the session header renders it.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SessionsView from '@/views/sessions/SessionsView.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { fakeFleetSession } from '@/lib/harnessClient';
import { _resetFleetSessionForTest, applyFleetSession } from '@/lib/fleetSession';
import { setConnectionState } from '@/lib/useConnectionState';

const SID = 'sess-sync-degraded';

async function mountView() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', component: SessionsView },
      { path: '/:p(.*)*', component: defineComponent({ render: () => h('div') }) },
    ],
  });
  await router.push(`/sessions/${SID}`);
  await router.isReady();
  const w = mount(SessionsView, {
    global: {
      plugins: [router, { install: (app) => void provideFakeClient(app, {}) }],
    },
    attachTo: document.body,
  });
  await flushPromises();
  return w;
}

function degradedFor(sessionId: string) {
  const lane = { status: 'unknown', consecutiveFailures: 0 };
  return fakeFleetSession({
    state: 'signed_in',
    sync: {
      contextSync: {
        status: 'degraded',
        reason: 'remote_context_missing',
        consecutiveFailures: 1,
        sessions: [
          {
            sessionId,
            reason: 'remote_context_missing',
            lastError: 'fleet: context append status 404',
            consecutiveFailures: 1,
            open: true,
            dropped: 7,
          },
        ],
      },
      unitPoll: { ...lane },
      telemetry: { ...lane },
    },
  });
}

describe('SessionsView — "Not syncing" badge (FR-6)', () => {
  beforeEach(() => {
    _resetFleetSessionForTest();
    setConnectionState('ready');
  });
  afterEach(() => _resetFleetSessionForTest());

  it('P-7: a failing synced session shows ONE "Not syncing — <reason>" badge with the details', async () => {
    const w = await mountView();
    expect(w.find('[data-testid="session-sync-toolbar"]').exists()).toBe(true);
    expect(w.find('[data-testid="session-sync-degraded"]').exists()).toBe(false);

    applyFleetSession(degradedFor(SID));
    await flushPromises();
    const badges = w.findAll('[data-testid="session-sync-degraded"]');
    expect(badges.length).toBe(1);
    expect(badges[0]!.text()).toContain('Not syncing — remote context missing');
    expect(badges[0]!.attributes('title')).toContain('7 message(s) were not synced');
    w.unmount();
  });

  it('another session failing does not badge this one', async () => {
    const w = await mountView();
    applyFleetSession(degradedFor('some-other-session'));
    await flushPromises();
    expect(w.find('[data-testid="session-sync-degraded"]').exists()).toBe(false);
    w.unmount();
  });
});

function unsupported() {
  const lane = { status: 'unknown', consecutiveFailures: 0 };
  return fakeFleetSession({
    state: 'signed_in',
    sync: {
      // What the backend publishes once a plain 404 latches the session-sync
      // route unsupported (core/fleet/append_breaker.go markUnsupported):
      // lane Off, the reason, and NO per-session breaker rows.
      contextSync: { status: 'off', reason: 'fleet_endpoint_unsupported', consecutiveFailures: 0 },
      unitPoll: { ...lane },
      telemetry: { ...lane },
    },
  });
}

describe('SessionsView — fleet server without session sync (latched unsupported)', () => {
  beforeEach(() => {
    _resetFleetSessionForTest();
    setConnectionState('ready');
  });
  afterEach(() => _resetFleetSessionForTest());

  it('once latched, the toolbar says events stay local and does not offer to enable sync', async () => {
    const w = await mountView();
    expect(w.find('[data-testid="session-sync-degraded"]').exists()).toBe(false);
    expect(w.find('[data-testid="session-sync-toggle"]').text()).toContain('Sync to fleet');

    applyFleetSession(unsupported());
    await flushPromises();
    const badge = w.find('[data-testid="session-sync-degraded"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toContain("this fleet server doesn't support session sync — events stay local");
    const toggle = w.find('[data-testid="session-sync-toggle"]');
    expect(toggle.text()).toContain('Sync unavailable');
    expect(toggle.text()).not.toContain('Synced to fleet');
    expect(toggle.attributes('disabled')).toBeDefined();
    w.unmount();
  });
});
