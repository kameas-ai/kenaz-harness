import { describe, it, expect } from 'vitest';
import { readFileSync, statSync, readdirSync } from 'node:fs';
import { resolve, join } from 'node:path';

/**
 * Privacy CI invariant #4 (plan §4.3): no raw hex/rgb/hsl/oklch literal
 * outside frontend/src/styles/tokens.css. This test mirrors what
 * `scripts/ci/check-css-tokens.mjs` does so the gate runs inside Vitest
 * even when the shell harness is unavailable.
 */
const ROOT = resolve(__dirname, '..');
const TOKENS_FILE = resolve(ROOT, 'styles/tokens.css');
const ALLOWLISTED_PATHS = new Set([
  TOKENS_FILE,
  resolve(ROOT, 'assets/fonts/OFL.txt'),
]);
const SKIP_DIRS = new Set(['node_modules', 'dist', '.git']);
const FILE_EXT = /\.(vue|ts|tsx|js|jsx|css|html)$/;

// css-tokens-allow: regex patterns are the matchers, not raw colors
const HEX_RE = /#[0-9a-fA-F]{3,8}\b/;
// css-tokens-allow: regex patterns are the matchers, not raw colors
const RGB_RE = /\brgba?\s*\(/;
// css-tokens-allow: regex patterns are the matchers, not raw colors
const HSL_RE = /\bhsla?\s*\(/;
// css-tokens-allow: regex patterns are the matchers, not raw colors
const OKLCH_RE = /\boklch\s*\(/;
const FORBIDDEN = [HEX_RE, RGB_RE, HSL_RE, OKLCH_RE];

const ALLOW_MARKER = /css-tokens-allow:/;

function* walk(dir: string): Generator<string> {
  for (const entry of readdirSync(dir)) {
    if (SKIP_DIRS.has(entry)) continue;
    const full = join(dir, entry);
    const s = statSync(full);
    if (s.isDirectory()) yield* walk(full);
    else yield full;
  }
}

describe('CSS token discipline (privacy CI invariant #4)', () => {
  it('no raw color literal outside tokens.css', () => {
    const violations: string[] = [];
    for (const file of walk(ROOT)) {
      if (ALLOWLISTED_PATHS.has(file)) continue;
      if (!FILE_EXT.test(file)) continue;
      const text = readFileSync(file, 'utf8');
      const lines = text.split('\n');
      for (let i = 0; i < lines.length; i++) {
        const line = lines[i];
        if (ALLOW_MARKER.test(line)) continue;
        if (FORBIDDEN.some((re) => re.test(line))) {
          violations.push(`${file}:${i + 1} ${line.trim()}`);
        }
      }
    }
    if (violations.length > 0) {
      throw new Error(
        `[css-tokens] ${violations.length} violation(s):\n${violations.join('\n')}`,
      );
    }
    expect(violations).toEqual([]);
  });
});
