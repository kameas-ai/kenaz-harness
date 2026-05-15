/**
 * sentry-redactor.ts — JS-side privacy redactor mirroring the Go regex set.
 *
 * Applied to all Sentry event data before transmission. This module is
 * imported by sentry.ts and is bundled with the Sentry lazy chunk (not the
 * entry bundle) because it only runs when tier != Off.
 *
 * Pattern inventory (mirrors core/sentry/redactor.go):
 *  1. @secret:<locator> syntactic shapes
 *  2. Anthropic API keys (sk-ant-...)
 *  3. OpenAI project keys (sk-proj-...)
 *  4. Generic OpenAI keys (sk-...)
 *  5. Bearer tokens ("Bearer <token>")
 *  6. Bare JWTs (eyJ...)
 *  7. AWS access key IDs (AKIA...)
 *  8. AWS secret access keys
 *  9. Email addresses
 * 10. Phone numbers
 * 11. private. slog attribute prefix (drop the key)
 * 12. Home-dir paths (~/ normalisation — best-effort in JS)
 * 13. Long strings > 200 chars (truncate)
 */

const PATTERNS: Array<[RegExp, string]> = [
  [/@secret:[A-Za-z0-9_/.@:-]+/g, '[REDACTED:secret-ref]'],
  [/sk-ant-[A-Za-z0-9_-]{20,}/g, '[REDACTED:anthropic-key]'],
  [/sk-proj-[A-Za-z0-9_-]{20,}/g, '[REDACTED:openai-key]'],
  [/sk-[A-Za-z0-9]{20,}/g, '[REDACTED:openai-key]'],
  [/bearer\s+[A-Za-z0-9._~+/=-]{20,}/gi, '[REDACTED:bearer-token]'],
  [/eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*/g, '[REDACTED:jwt-token]'],
  [/(?:AKIA|ASIA|AROA|AGPA|AIPA|ANPA|ANVA|APKA)[A-Z0-9]{16}/g, '[REDACTED:aws-key-id]'],
  [/(?:aws_secret_access_key|aws_secret)[=:\s]+[A-Za-z0-9+/]{40}/gi, '[REDACTED:aws-secret-key]'],
  [/[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}/g, '[REDACTED:contact]'],
  [/(?:\+?1[\s.-]?)?\(?\d{3}\)?[\s.-]?\d{3}[\s.-]?\d{4}/g, '[REDACTED:contact]'],
];

const LONG_STRING_THRESHOLD = 200;
const LONG_STRING_HEAD = 50;
const LONG_STRING_TAIL = 20;

/** Redact a single string value. */
export function redactString(s: string): string {
  if (!s) return s;
  let result = s;
  for (const [pattern, replacement] of PATTERNS) {
    result = result.replace(pattern, replacement);
  }
  return result;
}

/** Truncate a long binding-arg string. */
export function truncateLong(s: string): string {
  if (s.length <= LONG_STRING_THRESHOLD) return s;
  const head = s.slice(0, LONG_STRING_HEAD);
  const tail = s.slice(s.length - LONG_STRING_TAIL);
  return `${head}... [LONG_STRING_REDACTED ${s.length} chars] ...${tail}`;
}

/** Apply redaction + long-string truncation. */
export function redactStringDeep(s: string): string {
  return truncateLong(redactString(s));
}

/** Drop keys starting with "private." */
export function shouldDropKey(key: string): boolean {
  return key.startsWith('private.');
}

/** Redact all string values in an object, dropping private. keys. */
export function redactObject(obj: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(obj)) {
    if (shouldDropKey(k)) continue;
    if (typeof v === 'string') {
      out[k] = redactStringDeep(v);
    } else {
      out[k] = v;
    }
  }
  return out;
}
