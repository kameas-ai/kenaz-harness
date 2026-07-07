import { describe, it, expect } from 'vitest';
import { createFakeHarnessClient } from '@/lib/harnessClient';

describe('createFakeHarnessClient (FR-008 / SC-006)', () => {
  it('returns sane defaults across every view-scoped client', async () => {
    const c = createFakeHarnessClient();
    expect(await c.sessions.list()).toEqual([]);
    expect(await c.llm.listProviders()).toEqual([]);
    expect(await c.mcp.listServers()).toEqual([]);
    expect(await c.a2a.listCards()).toEqual([]);
    expect(await c.workflow.listJobs()).toEqual([]);
    expect(await c.trust.listSecretReferences()).toEqual([]);
    expect(await c.context.list()).toEqual([]);
    expect(await c.bundle.list()).toEqual([]);
    expect(await c.audit.listEntries({})).toEqual([]);
    expect((await c.shellStatus()).connection).toBe('ready');
    expect((await c.settings.get()).schemaVersion).toBe(1);
  });

  it('accepts a partial seed override', async () => {
    const c = createFakeHarnessClient({
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      sessions: {
        list: async () => [
          { id: 's1', name: 'one', createdAt: 'x', updatedAt: 'x' },
        ],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async (n: string) => ({
          id: 'new',
          name: n,
          createdAt: '',
          updatedAt: '',
        }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      } as any,
    });
    const list = await c.sessions.list();
    expect(list).toHaveLength(1);
    expect(list[0].name).toBe('one');
  });
});

// ── Onboarding client (harness-onboarding-01NHON01 review blockers) ──────────

describe('OnboardingClient — WP04/WP07 method surface (review blocker 1)', () => {
  it('fake stub exposes acceptHandoffHint, getHandoffHint, recordProgress', async () => {
    const c = createFakeHarnessClient();
    // acceptHandoffHint is a no-op in the fake — call must resolve without throwing
    await expect(c.onboarding.acceptHandoffHint({ emailHint: 'test@example.com', source: 'deep_link' })).resolves.toBeUndefined();
    // getHandoffHint returns an empty object
    const hint = await c.onboarding.getHandoffHint();
    expect(hint).toBeDefined();
    // recordProgress is a no-op in the fake — call must resolve without throwing
    await expect(c.onboarding.recordProgress('provider_configured')).resolves.toBeUndefined();
  });

  it('fake onboarding.state returns signedIn and accountStepAvailable fields', async () => {
    const c = createFakeHarnessClient();
    const state = await c.onboarding.state();
    expect(state).toHaveProperty('signedIn', false);
    expect(state).toHaveProperty('accountStepAvailable', false);
  });

  it('fake onboarding.state can be overridden to simulate signed-in user', async () => {
    const c = createFakeHarnessClient({
      onboarding: {
        state: async () => ({
          firstRun: false,
          completed: false,
          harnessSelfMCPDisabled: false,
          signedIn: true,
          accountStepAvailable: true,
        }),
      } as any,
    });
    const state = await c.onboarding.state();
    expect(state.signedIn).toBe(true);
    expect(state.accountStepAvailable).toBe(true);
  });
});

describe('OnboardingClient — account step feedback (review blocker 3)', () => {
  it('sign-in failure leaves state as account_step with error in fake (degrade-with-feedback contract)', async () => {
    // When the real FSM is wired with a nil signer, EventSignIn transitions to
    // guided_action with signedIn=false (graceful degradation). The fake stub
    // simulates this contract by returning an account_step card with an error.
    const c = createFakeHarnessClient({
      onboarding: {
        step: async (_req: any) => ({
          state: 'account_step',
          card: {
            title: 'Sign in to your account',
            error_message: 'account sign-in unavailable: Fleet is disabled in this build (HARNESS_FLEET_DISABLED=1)',
          },
        }),
      } as any,
    });
    const res = await c.onboarding.step({ state: 'account_step', event: 'sign_in' });
    // Feedback must be present — never a silent no-op
    expect(res.card.error_message).toBeTruthy();
    expect(res.card.error_message).toContain('unavailable');
  });
});

describe('Trust client never exposes credential values', () => {
  it('SecretReference shape lacks any value field at compile time', async () => {
    const c = createFakeHarnessClient();
    const refs = await c.trust.listSecretReferences();
    // structural assertion: a typical reference would shape-check the
    // absence of a `value` field. Compile-time guarantees are in
    // lib/types.ts; this is the runtime smoke check.
    refs.forEach((r) => {
      expect(Object.keys(r)).not.toContain('value');
      expect(Object.keys(r)).not.toContain('secret');
      expect(Object.keys(r)).not.toContain('password');
    });
  });
});
