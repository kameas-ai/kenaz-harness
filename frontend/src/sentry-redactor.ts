/**
 * sentry-redactor.ts — JS-side privacy redactor mirroring the Go regex set
 * in core/sentry/redactor.go.
 *
 * Applied to all Sentry event data before transmission. This module is
 * imported by sentry.ts and is bundled with the Sentry lazy chunk (not the
 * entry bundle) because it only runs when tier != Off.
 *
 * Pattern inventory (mirrors core/sentry/redactor.go — 13 patterns each,
 * kept in the SAME order as the Go rule table so the two are diffable
 * side by side; entry-points-and-crash-reporting-01PMZD13 UNIT-6 added
 * #5 (Gemini), #6 (Azure api-key) and #12 (Sentry DSN) to close a drift
 * where this header claimed to mirror the Go set and did not):
 *  1. @secret:<locator> syntactic shapes
 *  2. Anthropic API keys (sk-ant-...)
 *  3. OpenAI project keys (sk-proj-...)
 *  4. Generic OpenAI keys (sk-...)
 *  5. Gemini / Google AI Studio API keys (AIzaSy...)
 *  6. Azure OpenAI api-key header values
 *  7. Bearer tokens ("Bearer <token>")
 *  8. Bare JWTs (eyJ...)
 *  9. AWS access key IDs (AKIA...)
 * 10. AWS secret access keys
 * 11. Sentry DSN tokens (https://<32-hex>@host/id) — the worst pattern to
 *     be missing from a CRASH REPORTER: it could leak its own DSN.
 * 12. Email addresses
 * 13. Phone numbers
 *
 * Plus, applied outside the pattern table:
 *  - private. slog attribute prefix (drop the key, at every nesting level)
 *  - Home-dir path normalisation (~/ substitution) — SHAPE-based, not
 *    exact-prefix like the Go side, because the browser has no
 *    os.UserHomeDir() equivalent; see normalizeHomeDirs below.
 *  - Long strings > 200 chars (truncate)
 *  - Recursive walk into nested objects/arrays, depth-bounded, failing
 *    CLOSED at the bound — see redactObject.
 */

const PATTERNS: Array<[RegExp, string]> = [
  [/@secret:[A-Za-z0-9_/.@:-]+/g, '[REDACTED:secret-ref]'],
  [/sk-ant-[A-Za-z0-9_-]{20,}/g, '[REDACTED:anthropic-key]'],
  [/sk-proj-[A-Za-z0-9_-]{20,}/g, '[REDACTED:openai-key]'],
  [/sk-[A-Za-z0-9]{20,}/g, '[REDACTED:openai-key]'],
  // Gemini / Google AI Studio API keys (F-004 parity): 39-char keys
  // starting with "AIzaSy" followed by 33 alphanumeric/hyphen/underscore.
  [/AIzaSy[A-Za-z0-9_-]{33}/g, '[REDACTED:apikey]'],
  // Azure OpenAI api-key header values: "api-key:" context followed by a
  // 32-char lowercase hex string.
  [/api-key[:\s]+[0-9a-f]{32}/gi, '[REDACTED:apikey]'],
  [/bearer\s+[A-Za-z0-9._~+/=-]{20,}/gi, '[REDACTED:bearer-token]'],
  [/eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*/g, '[REDACTED:jwt-token]'],
  [/(?:AKIA|ASIA|AROA|AGPA|AIPA|ANPA|ANVA|APKA)[A-Z0-9]{16}/g, '[REDACTED:aws-key-id]'],
  [/(?:aws_secret_access_key|aws_secret)[=:\s]+[A-Za-z0-9+/]{40}/gi, '[REDACTED:aws-secret-key]'],
  // Sentry DSN: the 32-char lowercase hex public key embedded in the DSN
  // URL, https://<32-hex>@<host>/<project_id>.
  [/https:\/\/[0-9a-f]{32}@[A-Za-z0-9._-]+\/[0-9]+/g, '[REDACTED:sentry-dsn]'],
  [/[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}/g, '[REDACTED:contact]'],
  [/(?:\+?1[\s.-]?)?\(?\d{3}\)?[\s.-]?\d{3}[\s.-]?\d{4}/g, '[REDACTED:contact]'],
];

// Home-dir path normalisation, SHAPE-based rather than exact-prefix. The
// Go redactor knows the process's real home dir from os.UserHomeDir() and
// does a literal ReplaceAll; the browser has no equivalent OS API, so this
// matches the common home-directory path SHAPES on each platform instead.
// This is genuinely best-effort (it cannot know a non-standard home dir
// location), which is why the module-level comment above says so —
// previously that claim was made with no implementation behind it at all.
const HOME_DIR_PATTERNS: RegExp[] = [
  /\/Users\/[^/\s]+/g, // macOS
  /\/home\/[^/\s]+/g, // Linux
  /[A-Za-z]:\\Users\\[^\\\s]+/g, // Windows
];

function normalizeHomeDirs(s: string): string {
  let out = s;
  for (const re of HOME_DIR_PATTERNS) {
    out = out.replace(re, '~');
  }
  return out;
}

const LONG_STRING_THRESHOLD = 200;
const LONG_STRING_HEAD = 50;
const LONG_STRING_TAIL = 20;

/** Redact a single string value. */
export function redactString(s: string): string {
  if (!s) return s;
  let result = normalizeHomeDirs(s);
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

// MaxRedactDepth bounds the recursive walk in redactObject, mirroring
// core/sentry/redactor.go's sibling (added in the same unit) and
// core/sessions/export/redact.go's MaxRedactDepth precedent. A crash
// report's extra/context/vars payload is JSON-shaped data the app itself
// produced; nothing legitimate nests this deep, and an unbounded walk over
// a pathological payload is a stack blowup in a function whose entire job
// is to run on data heading for network transmission. At the bound the
// value is replaced with a marker — fail CLOSED, never pass the
// unexamined original through.
const MAX_REDACT_DEPTH = 24;

/**
 * Recursively redact an arbitrary JS value: strings go through
 * redactStringDeep, plain objects and arrays recurse (dropping
 * private.-prefixed keys at EVERY level, not just the top), depth is
 * bounded, and the walk fails closed at the bound rather than passing an
 * unexamined value through.
 *
 * Before this unit, redactObject's non-string branch was `out[k] = v`
 * unconditionally — a secret nested in an object, inside an array, or
 * keyed under a private.-prefixed name two levels down sailed through
 * untouched. This is the same defect class UNIT-6 also closes in the Go
 * crash reporter (core/sentry.RedactMap) and that v0.63.1 closed in the
 * session-export redactor (core/sessions/export/redact.go's
 * redactStructured) — reusing that function's SHAPE (recursive walk,
 * depth bound, cycle guard, fail-closed), not its rule set (the export
 * redactor's "a key can NAME a secret" forcing behaviour is specific to
 * free-form tool-call arguments and does not apply here).
 */
function redactValue(v: unknown, depth: number, seen: Set<unknown>): unknown {
  if (v === null || v === undefined) return v;
  if (depth > MAX_REDACT_DEPTH) return '[REDACTED:depth-limit]';

  if (typeof v === 'string') return redactStringDeep(v);
  if (typeof v !== 'object') return v; // number, boolean, bigint, symbol, function

  if (seen.has(v)) return '[REDACTED:cycle]';
  seen.add(v);
  try {
    if (Array.isArray(v)) {
      return v.map((item) => redactValue(item, depth + 1, seen));
    }
    const out: Record<string, unknown> = {};
    for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
      if (shouldDropKey(k)) continue;
      out[k] = redactValue(val, depth + 1, seen);
    }
    return out;
  } finally {
    seen.delete(v);
  }
}

/**
 * Redact all values in an object recursively, dropping private. keys at
 * every level. Nested objects and arrays are walked, not passed through —
 * see redactValue.
 */
export function redactObject(obj: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  const seen = new Set<unknown>();
  for (const [k, v] of Object.entries(obj)) {
    if (shouldDropKey(k)) continue;
    out[k] = redactValue(v, 1, seen);
  }
  return out;
}
