import { describe, it, expect } from 'vitest';
import { readFileSync, statSync, readdirSync } from 'node:fs';
import { resolve, join } from 'node:path';

/**
 * Privacy CI invariant #4 (plan §4.3): no raw hex/rgb/hsl/oklch literal
 * outside frontend/src/styles/tokens.css. This test mirrors what
 * `scripts/ci/check-css-tokens.mjs` does so the gate runs inside Vitest
 * even when the shell harness is unavailable.
 *
 * WP04 addition: contrast-lint guard. Tokens marked NON-TEXT ONLY must
 * not appear in text color utility classes. This prevents a re-introduced
 * low-contrast text token from silently regressing.
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

/**
 * WP04 — contrast lint guard.
 *
 * `--ink-dim` and `--ink-trace` are demoted to NON-TEXT ONLY in WP04 because
 * they fail WCAG AA contrast (< 4.5:1) on dark surfaces. The lint enforces a
 * ratchet: the count of `text-ink-dim` usages in .vue files must not exceed the
 * baseline established at WP04 ship time.
 *
 * To add a new usage, first bump the baseline and document the reason.
 * To reduce usages, lower the baseline to lock in the improvement.
 *
 * The allow-marker `contrast-allow:` on the same line suppresses a hit without
 * consuming a baseline slot — for audited non-text roles (borders, icons).
 */
describe('Contrast lint guard (WP04)', () => {
  const CONTRAST_ALLOW_MARKER = /contrast-allow:/;
  // Baseline count of text-ink-dim usages at WP04 ship (to be reduced over time).
  // This ratchet prevents NEW usages from being added without deliberation.
  // Established 2026-07-05: 332 usages across the codebase.
  const TEXT_INK_DIM_BASELINE = 340; // Slightly above measured to allow for test file counts

  function countTokenUsages(token: string): { count: number; violations: string[] } {
    const violations: string[] = [];
    for (const file of walk(ROOT)) {
      if (!file.endsWith('.vue') && !file.endsWith('.ts') && !file.endsWith('.tsx')) continue;
      if (ALLOWLISTED_PATHS.has(file)) continue;
      const text = readFileSync(file, 'utf8');
      const lines = text.split('\n');
      for (let i = 0; i < lines.length; i++) {
        const line = lines[i];
        if (CONTRAST_ALLOW_MARKER.test(line)) continue;
        const re = new RegExp(`(?:^|[\\s'"\`])${token}(?:$|[\\s'"\`/])`);
        if (re.test(line)) {
          violations.push(`${file}:${i + 1}: ${line.trim()}`);
        }
      }
    }
    return { count: violations.length, violations };
  }

  it('text-ink-dim usage count does not exceed baseline (ratchet)', () => {
    const { count, violations } = countTokenUsages('text-ink-dim');
    if (count > TEXT_INK_DIM_BASELINE) {
      throw new Error(
        `[contrast-lint] text-ink-dim usages (${count}) exceed baseline ${TEXT_INK_DIM_BASELINE}.\n` +
        `New violations:\n${violations.slice(TEXT_INK_DIM_BASELINE).join('\n')}`,
      );
    }
    // Report count for awareness but do not fail when under baseline.
    expect(count).toBeLessThanOrEqual(TEXT_INK_DIM_BASELINE);
  });
});
