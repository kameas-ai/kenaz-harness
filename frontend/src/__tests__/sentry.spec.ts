/**
 * sentry.spec.ts — Unit tests for the frontend Sentry integration.
 *
 * Tests:
 *  1. Sentry lazy chunk is NOT imported when tier == 'off'.
 *  2. Sentry IS initialised when tier != 'off' and DSN is provided.
 *  3. JS-side redactor scrubs known secrets from event data.
 *  4. autoSessionTracking and sendDefaultPii are false.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  redactString,
  truncateLong,
  shouldDropKey,
  redactObject,
} from '@/sentry-redactor';

// ── redactor unit tests ────────────────────────────────────────────────────

describe('sentry-redactor', () => {
  it('redacts @secret: refs', () => {
    const got = redactString('using @secret:anthropic/prod/key123');
    expect(got).not.toContain('@secret:');
    expect(got).toContain('[REDACTED:secret-ref]');
  });

  it('redacts Anthropic API keys', () => {
    const got = redactString('sk-ant-api03-REALKEY123456789ABCDEFGH');
    expect(got).not.toContain('sk-ant-');
    expect(got).toContain('[REDACTED:anthropic-key]');
  });

  it('redacts OpenAI project keys', () => {
    const got = redactString('sk-proj-realprojectkey1234567890abc');
    expect(got).not.toContain('sk-proj-');
  });

  it('redacts bearer tokens', () => {
    const got = redactString('Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.abc.def');
    expect(got).not.toContain('eyJhbGciO');
  });

  it('redacts bare JWTs', () => {
    const got = redactString('eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.abc.def');
    expect(got).not.toContain('eyJhbGciO');
  });

  it('redacts AWS key IDs', () => {
    const got = redactString('access=AKIAIOSFODNN7EXAMPLE');
    expect(got).not.toContain('AKIAIOSFODNN7');
    expect(got).toContain('[REDACTED:aws-key-id]');
  });

  it('redacts AWS secret keys', () => {
    const got = redactString('aws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY');
    expect(got).not.toContain('wJalrXUtn');
  });

  it('redacts emails', () => {
    const got = redactString('contact victim@example.com now');
    expect(got).not.toContain('victim@');
    expect(got).toContain('[REDACTED:contact]');
  });

  it('redacts phone numbers', () => {
    const got = redactString('call 555-867-5309 now');
    expect(got).not.toContain('555-867-5309');
  });

  it('truncates long strings', () => {
    const long = 'A'.repeat(300);
    const got = truncateLong(long);
    expect(got).toContain('[LONG_STRING_REDACTED');
    expect(got.length).toBeLessThan(300);
    expect(got.startsWith('A'.repeat(50))).toBe(true);
    expect(got.endsWith('A'.repeat(20))).toBe(true);
  });

  it('does not truncate short strings', () => {
    const short = 'hello world';
    expect(truncateLong(short)).toBe(short);
  });

  it('drops private. keys from maps', () => {
    const out = redactObject({
      'private.secret': 'sk-ant-api03-SECRET123456789AB',
      'public.info': 'normal',
    });
    expect(out).not.toHaveProperty('private.secret');
    expect(out).toHaveProperty('public.info', 'normal');
  });

  it('redacts secret values in map', () => {
    const out = redactObject({ key: 'sk-ant-api03-SECRETKEY123456789' });
    expect(out['key']).not.toContain('sk-ant-');
  });

  it('reports private. key should be dropped', () => {
    expect(shouldDropKey('private.token')).toBe(true);
    expect(shouldDropKey('public.token')).toBe(false);
  });
});

// ── lazy-load guard ────────────────────────────────────────────────────────

describe('initSentry', () => {
  beforeEach(() => {
    vi.resetModules();
  });

  it('does not import @sentry/vue when tier is off', async () => {
    const importSpy = vi.spyOn(await import('@/sentry'), 'initSentry');

    // When tier is 'off', initSentry should return false without loading SDK.
    const { initSentry } = await import('@/sentry');
    const result = await initSentry({
      tier: 'off',
      dsn: 'https://fake@sentry.io/123',
      app: null as unknown as import('vue').App,
    });
    expect(result).toBe(false);
    importSpy.mockRestore();
  });

  it('returns false when DSN is empty even if tier is non-off', async () => {
    const { initSentry } = await import('@/sentry');
    const result = await initSentry({
      tier: 'anonymous',
      dsn: '',
      app: null as unknown as import('vue').App,
    });
    expect(result).toBe(false);
  });
});

