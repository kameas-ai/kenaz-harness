#!/usr/bin/env node
/**
 * Privacy CI invariant #4 (plan §4.3): no raw color literal outside
 * frontend/src/styles/tokens.css. Tailwind utilities and var(--token)
 * references are the only allowed paths.
 *
 * Usage:
 *   node scripts/ci/check-css-tokens.mjs        (from the repo root, as CI runs it)
 *   npm run check:css-tokens                    (from frontend/)
 *
 * Exit 0 = clean, exit 1 = violation. Allowlist via the marker
 * `// css-tokens-allow: <reason+ticket>` on the violating line.
 *
 * PATHS ARE ANCHORED TO THIS FILE, NOT process.cwd()
 * --------------------------------------------------
 * The scan root used to be `resolve(process.cwd(), 'src')`. That resolves
 * correctly under `npm run check:css-tokens` (cwd = frontend/) and to a
 * nonexistent `<repo>/src` under pr.yml, which runs the script with
 * `working-directory: ${{ github.workspace }}`. So on every CI run this
 * script threw an uncaught ENOENT from readdirSync and died with a Node
 * stack trace — swallowed whole by the step's `continue-on-error: true`.
 * It never inspected a single line of CSS in CI, and the step was green
 * every time.
 *
 * Same root cause as the bundle-size gate fixed in #279, same remedy:
 * derive the repo root from import.meta.url so the gate cannot be
 * re-silenced by being invoked from a different directory. Do not
 * reintroduce a cwd-relative path here.
 */

import { readFileSync, statSync, readdirSync, existsSync } from 'node:fs';
import { resolve, join, relative, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const ROOT = resolve(REPO_ROOT, 'frontend/src');
const TOKENS_FILE = resolve(ROOT, 'styles/tokens.css');
const ALLOWLISTED_PATHS = new Set([
  TOKENS_FILE,
  resolve(ROOT, 'assets/fonts/OFL.txt'),
]);
const SKIP_DIRS = new Set(['node_modules', 'dist', '.git']);
const FILE_EXT = /\.(vue|ts|tsx|js|jsx|css|html)$/;

const GHA = Boolean(process.env['GITHUB_ACTIONS']);

/** Single-line GitHub Actions annotation, so the number reaches the Checks UI. */
function annotate(level, message) {
  if (GHA) console.log(`::${level}::${message.replace(/\n/g, ' ')}`);
}

/**
 * A missing scan root is a broken gate, not a clean tree. Fail loudly and
 * say which path was expected — the previous behaviour was an ENOENT stack
 * trace, which reads as noise rather than as a finding.
 */
if (!existsSync(ROOT)) {
  const msg =
    `[css-tokens] FAIL: scan root ${ROOT} does not exist. ` +
    `This gate resolves frontend/src from the script's own location; if the ` +
    `frontend moved, update REPO_ROOT/ROOT here in the same commit.`;
  console.error(msg);
  annotate('error', msg);
  process.exit(1);
}

const FORBIDDEN_PATTERNS = [
  { name: 'hex literal', re: /#[0-9a-fA-F]{3,8}\b/ },
  { name: 'rgb()/rgba()', re: /\brgba?\s*\(/ },
  { name: 'hsl()/hsla()', re: /\bhsla?\s*\(/ },
  { name: 'oklch()', re: /\boklch\s*\(/ },
  // Hard-coded font-family stack — anything that is not a var(--font-*)
  // reference or a CSS-wide keyword.
  //
  // The previous pattern was /font-family\s*:\s*(?!var\()/i, which flagged
  // every `font-family: var(--font-ui)` line in the codebase — 33 of the 41
  // violations it reported. The trailing `\s*` before the lookahead is
  // backtrackable: the engine matches `font-family:`, consumes the space,
  // fails `(?!var\()` against `var(`, then backtracks `\s*` to zero width
  // and re-tests the lookahead against ` var(` — which is not `var(`, so it
  // "succeeds" and the line is reported. Putting the whitespace INSIDE the
  // lookahead removes the backtracking position, so the negation actually
  // negates. Keep it that way.
  {
    name: 'font-family stack',
    re: /font-family\s*:(?!\s*(?:var\(|inherit\b|initial\b|unset\b|revert\b))/i,
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
let scanned = 0;

for (const file of walk(ROOT)) {
  if (ALLOWLISTED_PATHS.has(file)) continue;
  if (!FILE_EXT.test(file)) continue;

  let text;
  try {
    text = readFileSync(file, 'utf8');
  } catch {
    continue;
  }
  scanned++;
  const lines = text.split('\n');
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (ALLOW_MARKER.test(line)) continue;
    for (const { name, re } of FORBIDDEN_PATTERNS) {
      if (re.test(line)) {
        console.error(
          `[css-tokens] ${relative(REPO_ROOT, file)}:${i + 1}  ${name}: ${line.trim()}`,
        );
        violations++;
        break;
      }
    }
  }
}

/**
 * Scanning zero files means the walk found nothing to look at — a directory
 * full of unmatched extensions, or a pruned tree. Treat it the same as a
 * missing root: this gate's failure mode is passing vacuously, so every path
 * that could produce a vacuous pass is made loud.
 */
if (scanned === 0) {
  const msg = `[css-tokens] FAIL: walked ${ROOT} and matched 0 source files — the gate inspected nothing.`;
  console.error(msg);
  annotate('error', msg);
  process.exit(1);
}

if (violations > 0) {
  const msg = `[css-tokens] ${violations} violation(s) across ${scanned} files — invariant #4 (plan §4.3) failed.`;
  console.error(`\n${msg}`);
  console.error(
    `Add a "// css-tokens-allow: <reason+ticket>" marker to allowlist a justified line, or move the literal into frontend/src/styles/tokens.css.`,
  );
  annotate('warning', msg);
  process.exit(1);
}

console.log(`[css-tokens] clean across ${scanned} files — invariant #4 (plan §4.3) passes.`);
