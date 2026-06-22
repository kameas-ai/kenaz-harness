/**
 * featureFlags.spec.ts — tests for the fleet capability gating helpers.
 * (fleet-capability-surface-01NDFSEX09 WP12)
 */
import { describe, it, expect, beforeEach } from 'vitest';
import { capability, signedIn, initFeatureFlags } from '../featureFlags';
import type { AppInfo } from '../types';

// Minimal AppInfo factory for tests.
function makeInfo(
  caps: Record<string, boolean>,
  tier: AppInfo['tier'] = 'pro',
): AppInfo {
  return {
    build: 'test',
    commit: 'abc',
    buildTime: '',
    goVersion: '',
    platform: 'test',
    windowSize: { width: 1280, height: 800 },
    capabilities: caps,
    tier,
  };
}

describe('featureFlags', () => {
  // Reset state between tests.
  beforeEach(() => {
    initFeatureFlags(null);
  });

  describe('capability()', () => {
    it('returns false when appInfo is null (not yet fetched)', () => {
      expect(capability('launcher_updates')).toBe(false);
    });

    it('returns false when capabilities map is absent from appInfo', () => {
      initFeatureFlags({
        build: 'test',
        commit: '',
        buildTime: '',
        goVersion: '',
        platform: 'test',
        windowSize: { width: 0, height: 0 },
        // no capabilities field
      });
      expect(capability('launcher_updates')).toBe(false);
    });

    it('returns false when capabilities map is empty', () => {
      initFeatureFlags(makeInfo({}));
      expect(capability('launcher_updates')).toBe(false);
    });

    it('returns true when capability key is explicitly true', () => {
      initFeatureFlags(makeInfo({ launcher_updates: true }));
      expect(capability('launcher_updates')).toBe(true);
    });

    it('returns false when capability key is explicitly false', () => {
      initFeatureFlags(makeInfo({ launcher_updates: false }));
      expect(capability('launcher_updates')).toBe(false);
    });

    it('returns false for a missing key (not present in map)', () => {
      initFeatureFlags(makeInfo({ launcher_updates: true }));
      expect(capability('shared_team_graph')).toBe(false);
    });

    it('returns false for an unknown key (no panic)', () => {
      initFeatureFlags(makeInfo({ launcher_updates: true }));
      expect(capability('not_a_real_capability_xyz')).toBe(false);
    });

    it('returns true for each capability that is set to true', () => {
      const caps: Record<string, boolean> = {
        launcher_updates: true,
        iso_distribution: true,
        sso_saml: false,
        shared_team_graph: true,
      };
      initFeatureFlags(makeInfo(caps));
      expect(capability('launcher_updates')).toBe(true);
      expect(capability('iso_distribution')).toBe(true);
      expect(capability('sso_saml')).toBe(false);
      expect(capability('shared_team_graph')).toBe(true);
    });
  });

  describe('signedIn', () => {
    it('is false when appInfo is null', () => {
      expect(signedIn.value).toBe(false);
    });

    it('is false when capabilities map is empty (signed-out state)', () => {
      initFeatureFlags(makeInfo({}));
      expect(signedIn.value).toBe(false);
    });

    it('is false when capabilities is undefined', () => {
      initFeatureFlags({
        build: 'test',
        commit: '',
        buildTime: '',
        goVersion: '',
        platform: 'test',
        windowSize: { width: 0, height: 0 },
      });
      expect(signedIn.value).toBe(false);
    });

    it('is true when capabilities map has at least one entry', () => {
      initFeatureFlags(makeInfo({ launcher_updates: true }));
      expect(signedIn.value).toBe(true);
    });

    it('is true even when all values are false (signed in but no caps enabled)', () => {
      // The map being non-empty means we received a fleet response →
      // the user is signed in even if all caps are false.
      initFeatureFlags(makeInfo({ launcher_updates: false }));
      expect(signedIn.value).toBe(true);
    });

    it('updates reactively when initFeatureFlags is called again', () => {
      // Start signed-out.
      expect(signedIn.value).toBe(false);
      // Sign in.
      initFeatureFlags(makeInfo({ launcher_updates: true }));
      expect(signedIn.value).toBe(true);
      // Sign out.
      initFeatureFlags(makeInfo({}));
      expect(signedIn.value).toBe(false);
    });
  });

  describe('OSS-first gating pattern', () => {
    it('capability returns false for any key when signed out', () => {
      // Simulates v-if="signedIn && capability('launcher_updates')"
      initFeatureFlags(null);
      const gateOpen = signedIn.value && capability('launcher_updates');
      expect(gateOpen).toBe(false);
    });

    it('gate is open when signed in and capability enabled', () => {
      initFeatureFlags(makeInfo({ launcher_updates: true }));
      const gateOpen = signedIn.value && capability('launcher_updates');
      expect(gateOpen).toBe(true);
    });

    it('gate is closed when signed in but capability disabled', () => {
      initFeatureFlags(makeInfo({ launcher_updates: false }));
      const gateOpen = signedIn.value && capability('launcher_updates');
      expect(gateOpen).toBe(false);
    });
  });
});
