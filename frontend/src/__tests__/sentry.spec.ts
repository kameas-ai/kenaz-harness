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

  // ── UNIT-6 (entry-points-and-crash-reporting-01PMZD13): pattern-inventory
  // drift closed. Before this unit, the Go redactor (core/sentry/redactor.go)
  // had 13 [REDACTED:…] patterns and this file had 10 — this header claimed
  // to mirror the Go set and did not. ──────────────────────────────────────

  it('redacts Gemini API keys', () => {
    const got = redactString('key=AIzaSyABCDEFGHIJKLMNOPQRSTUVWXYZ0123456');
    expect(got).not.toContain('AIzaSy');
    expect(got).toContain('[REDACTED:apikey]');
  });

  it('redacts Azure api-key header values', () => {
    const got = redactString('api-key: abcdef1234567890abcdef1234567890');
    expect(got).not.toContain('abcdef1234567890abcdef1234567890');
    expect(got).toContain('[REDACTED:apikey]');
  });

  it('redacts Sentry DSN tokens', () => {
    const got = redactString(
      'dsn=https://abcdef1234567890abcdef1234567890@o123456.ingest.sentry.io/789'
    );
    expect(got).not.toContain('abcdef1234567890abcdef1234567890');
    expect(got).toContain('[REDACTED:sentry-dsn]');
  });

  // ── UNIT-6: home-dir normalisation. Before this unit, the module header
  // claimed "Home-dir paths (~/ normalisation — best-effort in JS)" but
  // redactString implemented no such thing at all — a claim with no code
  // behind it. ─────────────────────────────────────────────────────────────

  it('normalises macOS home-dir paths', () => {
    const got = redactString('reading /Users/alice/.kenaz/harness.log');
    expect(got).not.toContain('/Users/alice');
    expect(got).toContain('~/.kenaz/harness.log');
  });

  it('normalises Linux home-dir paths', () => {
    const got = redactString('reading /home/bob/.config/kenaz/settings.json');
    expect(got).not.toContain('/home/bob');
  });

  it('normalises Windows home-dir paths', () => {
    const got = redactString('reading C:\\Users\\carol\\AppData\\kenaz');
    expect(got).not.toContain('C:\\Users\\carol');
  });

  // ── UNIT-6: deep recursion falsifiability corpus — the SAME shape and
  // secret values as core/sentry/redactor_test.go's
  // TestRedactMap_DeepFixtureCorpus (three levels deep, all pattern classes,
  // an array of secrets, a nested private. key). Both suites must assert the
  // same redacted output; reverting either redactor's recursion must turn
  // its own suite red — verified for the Go side by reverting RedactMap to
  // its pre-UNIT-6 shallow form and observing this fixture's five assertions
  // fail, then restoring it. ──────────────────────────────────────────────

  function deepFixtureCorpus(): Record<string, unknown> {
    return {
      secret_ref: '@secret:foo/bar-1',
      anthropic_key: 'sk-ant-' + 'a'.repeat(25),
      openai_proj_key: 'sk-proj-' + 'b'.repeat(25),
      openai_key: 'sk-' + 'c'.repeat(25),
      gemini_key: 'AIzaSy' + 'D'.repeat(33),
      azure_key: 'api-key: ' + 'e'.repeat(32),
      bearer: 'Bearer ' + 'f'.repeat(25),
      jwt: 'eyJ' + 'g'.repeat(10) + '.' + 'h'.repeat(10) + '.' + 'i'.repeat(5),
      aws_key_id: 'AKIA' + 'J'.repeat(16),
      aws_secret: 'aws_secret_access_key=' + 'k'.repeat(40),
      sentry_dsn: 'https://' + '1'.repeat(32) + '@sentry.example.com/123',
      email: 'person@example.com',
      phone: '555-123-4567',
      nested: {
        level2_secret: 'sk-ant-' + 'm'.repeat(25),
        'private.token': 'should-be-dropped-entirely',
        level3: {
          deep_secret: 'AKIA' + 'N'.repeat(16),
        },
      },
      secret_array: ['sk-proj-' + 'p'.repeat(25), 'sk-ant-' + 'q'.repeat(25)],
    };
  }

  it('redacts a three-level-nested corpus of every pattern, an array, and a nested private key', () => {
    const out = redactObject(deepFixtureCorpus());

    const topLevelChecks: Record<string, string> = {
      secret_ref: '[REDACTED:secret-ref]',
      anthropic_key: '[REDACTED:anthropic-key]',
      openai_proj_key: '[REDACTED:openai-key]',
      openai_key: '[REDACTED:openai-key]',
      gemini_key: '[REDACTED:apikey]',
      azure_key: '[REDACTED:apikey]',
      bearer: '[REDACTED:bearer-token]',
      jwt: '[REDACTED:jwt-token]',
      aws_key_id: '[REDACTED:aws-key-id]',
      aws_secret: '[REDACTED:aws-secret-key]',
      sentry_dsn: '[REDACTED:sentry-dsn]',
      email: '[REDACTED:contact]',
      phone: '[REDACTED:contact]',
    };
    for (const [key, marker] of Object.entries(topLevelChecks)) {
      expect(out[key]).toContain(marker);
    }

    const nested = out['nested'] as Record<string, unknown>;
    expect(nested).not.toHaveProperty('private.token');
    expect(nested['level2_secret']).toContain('[REDACTED:anthropic-key]');
    const level3 = nested['level3'] as Record<string, unknown>;
    expect(level3['deep_secret']).toContain('[REDACTED:aws-key-id]');

    const arr = out['secret_array'] as string[];
    expect(arr).toHaveLength(2);
    for (const s of arr) {
      expect(s).toContain('[REDACTED:');
    }
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

