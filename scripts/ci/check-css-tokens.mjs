#!/usr/bin/env node
/**
 * Privacy CI invariant #4 (plan §4.3): no raw color literal outside
 * frontend/src/styles/tokens.css. Tailwind utilities and var(--token)
 * references are the only allowed paths.
 *
 * Usage: `node scripts/ci/check-css-tokens.mjs` (npm script:
 * `npm run check:css-tokens`).
 *
 * Exit 0 = clean, exit 1 = violation. Allowlist via the marker
 * `// css-tokens-allow: <reason+ticket>` on the violating line.
 */

import { readFileSync, statSync, readdirSync } from 'node:fs';
import { resolve, join, relative } from 'node:path';

const ROOT = resolve(process.cwd(), 'src');
const TOKENS_FILE = resolve(ROOT, 'styles/tokens.css');
const ALLOWLISTED_PATHS = new Set([
  TOKENS_FILE,
  resolve(ROOT, 'assets/fonts/OFL.txt'),
]);
const SKIP_DIRS = new Set(['node_modules', 'dist', '.git']);
const FILE_EXT = /\.(vue|ts|tsx|js|jsx|css|html)$/;

const FORBIDDEN_PATTERNS = [
  { name: 'hex literal', re: /#[0-9a-fA-F]{3,8}\b/ },
  { name: 'rgb()/rgba()', re: /\brgba?\s*\(/ },
  { name: 'hsl()/hsla()', re: /\bhsla?\s*\(/ },
  { name: 'oklch()', re: /\boklch\s*\(/ },
  // hard-coded font-family stack (anything other than var(--font-...))
  {
    name: 'font-family stack',
    re: /font-family\s*:\s*(?!var\()/i,
  },
];

const ALLOW_MARKER = /css-tokens-allow:/;

function* walk(dir) {
  for (const entry of readdirSync(dir)) {
    if (SKIP_DIRS.has(entry)) continue;
    const full = join(dir, entry);
    const s = statSync(full);
    if (s.isDirectory()) yield* walk(full);
    else yield full;
  }
}

let violations = 0;

for (const file of walk(ROOT)) {
  if (ALLOWLISTED_PATHS.has(file)) continue;
  if (!FILE_EXT.test(file)) continue;

  let text;
  try {
    text = readFileSync(file, 'utf8');
  } catch {
    continue;
  }
  const lines = text.split('\n');
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (ALLOW_MARKER.test(line)) continue;
    for (const { name, re } of FORBIDDEN_PATTERNS) {
      if (re.test(line)) {
        // Skip false positives where the hex sits inside a CSS variable
        // value declaration in a <style> scoped block — these only happen
        // in tokens.css which is allowlisted. For everything else, fail.
        console.error(
          `[css-tokens] ${relative(process.cwd(), file)}:${i + 1}  ${name}: ${line.trim()}`,
        );
        violations++;
        break;
      }
    }
  }
}

if (violations > 0) {
  console.error(
    `\n[css-tokens] ${violations} violation(s) — invariant #4 (plan §4.3) failed.`,
  );
  console.error(
    `Add a "// css-tokens-allow: <reason+ticket>" marker to allowlist a justified line, or move the literal into frontend/src/styles/tokens.css.`,
  );
  process.exit(1);
}

console.log('[css-tokens] clean — invariant #4 (plan §4.3) passes.');
