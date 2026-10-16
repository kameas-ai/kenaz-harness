import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  clearSessionCredentials,
  readSessionCredential,
  readSessionModel,
  writeSessionCredential,
  writeSessionModel,
} from '@/lib/sessionModelStash';
import type { FleetSessionView } from '@/lib/types';

const ALICE = '0123456789abcdef.0123456789abcdef01234567';
const BOB = 'fedcba9876543210.fedcba9876543210fedcba98';

describe('per-chat key selection (WP04 review fix)', () => {
  beforeEach(() => window.localStorage.clear());

  it('is scoped per account: another account never reads it', () => {
    writeSessionCredential(ALICE, 's1', 'openai', 'c-org');
    expect(readSessionCredential(ALICE, 's1', 'openai')).toBe('c-org');
    expect(readSessionCredential(BOB, 's1', 'openai')).toBeNull();
    expect(readSessionCredential('', 's1', 'openai')).toBeNull();
    // The model stash itself carries no key selection.
    writeSessionModel('s1', { providerId: 'openai', modelId: 'm' });
    expect(readSessionModel('s1')).toEqual({ providerId: 'openai', modelId: 'm' });
  });

  it('is bound to its provider: a provider change drops it', () => {
    writeSessionModel('s1', { providerId: 'openai', modelId: 'm' });
    writeSessionCredential(ALICE, 's1', 'openai', 'c-org');
    writeSessionModel('s1', { providerId: 'openai', modelId: 'm2' });
    expect(readSessionCredential(ALICE, 's1', 'openai')).toBe('c-org');
    writeSessionModel('s1', { providerId: 'anthropic', modelId: 'claude' });
    expect(readSessionCredential(ALICE, 's1', 'openai')).toBeNull();
    expect(readSessionCredential(ALICE, 's1', 'anthropic')).toBeNull();
  });

  it('drops a legacy credentialId left in the model stash', () => {
    window.localStorage.setItem(
      'kenaz.session.config.s1',
      JSON.stringify({ providerId: 'openai', modelId: 'm', credentialId: 'c-org' }),
    );
    writeSessionModel('s1', { providerId: 'anthropic', modelId: 'x' });
    expect(window.localStorage.getItem('kenaz.session.config.s1')).not.toContain('credentialId');
  });

  it('clearSessionCredentials removes every account, or all but one', () => {
    writeSessionCredential(ALICE, 's1', 'openai', 'c-a');
    writeSessionCredential(BOB, 's2', 'openai', 'c-b');
    clearSessionCredentials(ALICE);
    expect(readSessionCredential(ALICE, 's1', 'openai')).toBe('c-a');
    expect(readSessionCredential(BOB, 's2', 'openai')).toBeNull();
    clearSessionCredentials();
    expect(readSessionCredential(ALICE, 's1', 'openai')).toBeNull();
  });
});

function snapshot(accountKey?: string): FleetSessionView {
  const lane = { status: 'unknown', consecutiveFailures: 0 };
  return {
    state: accountKey ? 'signed_in' : 'signed_out',
    autoRetry: true,
    tokensUsable: !!accountKey,
    accountKey,
    claims: { hasSubject: !!accountKey, hasOrgClaim: false },
    capabilities: { tier: '', enabled: {}, fetchedAt: '', source: 'test' },
    sync: { contextSync: { ...lane }, unitPoll: { ...lane }, telemetry: { ...lane }, catalogRevocation: { ...lane } },
    updatedAt: '',
  } as FleetSessionView;
}

describe('fleet session clears key selections on sign-out and account change', () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.resetModules();
  });

  it('a sign-out snapshot clears the signed-in account selections', async () => {
    const fs = await import('@/lib/fleetSession');
    const stash = await import('@/lib/sessionModelStash');
    fs.applyFleetSession(snapshot(ALICE));
    stash.writeSessionCredential(ALICE, 's1', 'openai', 'c-org');
    fs.applyFleetSession(snapshot(ALICE)); // same account: kept
    expect(stash.readSessionCredential(ALICE, 's1', 'openai')).toBe('c-org');
    fs.applyFleetSession(snapshot(undefined)); // signed out
    expect(stash.readSessionCredential(ALICE, 's1', 'openai')).toBeNull();
  });

  it('signing in as someone else clears the previous account selections', async () => {
    const fs = await import('@/lib/fleetSession');
    const stash = await import('@/lib/sessionModelStash');
    fs.applyFleetSession(snapshot(ALICE));
    stash.writeSessionCredential(ALICE, 's1', 'openai', 'c-org');
    fs.applyFleetSession(snapshot(BOB));
    expect(stash.readSessionCredential(ALICE, 's1', 'openai')).toBeNull();
  });

  it('the first snapshot of a run drops other accounts and keeps the current one', async () => {
    const stash = await import('@/lib/sessionModelStash');
    stash.writeSessionCredential(ALICE, 's1', 'openai', 'c-a');
    stash.writeSessionCredential(BOB, 's2', 'openai', 'c-b');
    const fs = await import('@/lib/fleetSession');
    fs.applyFleetSession(snapshot(BOB));
    expect(stash.readSessionCredential(BOB, 's2', 'openai')).toBe('c-b');
    expect(stash.readSessionCredential(ALICE, 's1', 'openai')).toBeNull();
  });
});
