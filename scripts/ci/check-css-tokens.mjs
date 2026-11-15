#!/usr/bin/env node
/**
 * Privacy CI invariant #4 (plan §4.3): no raw color literal outside
 * frontend/src/styles/tokens.css. Tailwind utilities and var(--token)
 * references are the only allowed paths.
 *
 * DS v0.13.0 extension (design-system adoption): the same discipline now
 * covers the design system's SPACE / TYPE / RADIUS token groups. Every
 * font size, radius and padding/margin/gap step must resolve to a
 * `--type-*` / `--radius-*` / `--space-*` token — through the Tailwind
 * theme (frontend/tailwind.config.ts maps `text-caption`, `p-3`,
 * `rounded-sm`, … onto the DS variables) or a `var(--…)` in CSS. Flagged:
 *   - Tailwind arbitrary values for those groups: `text-[11px]`,
 *     `rounded-[3px]`, `p-[7px]`, `tracking-[0.18em]`;
 *   - Tailwind sizes that are off the DS scale: `text-base|lg|xl|2xl…`
 *     and padding/margin/gap steps with no DS step (`p-7`, `gap-10`, …);
 *   - CSS declarations with a px/rem literal for font-size, border-radius,
 *     padding, margin or gap (hairlines ≤ 1px and `0` stay legal);
 *   - the retired serif register (`font-serif`, `var(--font-serif)`):
 *     DS 0.12 removed Newsreader; headings use `font-heading`.
 * The DS rules skip test files (__tests__/, *.test.*, *.spec.*), whose
 * fixtures hold arbitrary strings; the colour rules still cover them.
 *
 * Usage:
 *   node scripts/ci/check-css-tokens.mjs        (from the repo root, as CI runs it)
 *   npm run check:css-tokens                    (from frontend/)
 *
 * Exit 0 = clean, exit 1 = violation. Allowlist via the marker
 * `// css-tokens-allow: <reason+ticket>` on the violating line, or
 * `css-tokens-allow-next-line: <reason>` on the line above it (for
 * template attributes, where a same-line comment is impossible).
 * `font-family` inside an `@font-face` block names a face, not a stack,
 * and is exempt.
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

// ── DS v0.13.0 space / type / radius groups ─────────────────────────────
// A Tailwind class token is matched when preceded by start, whitespace,
// a quote, a backtick, `:` (variants) or `!` (important), so `text-[11px]`
// inside class="…", :class="{ … }" and template literals are all seen.
const TW = String.raw`(?:^|[\s"'\x60:!{])`;
const SPACE_UTIL = String.raw`-?(?:p|px|py|pt|pb|pl|pr|ps|pe|m|mx|my|mt|mb|ml|mr|ms|me|gap|gap-x|gap-y|space-x|space-y)`;
// Tailwind spacing keys that have NO DS --space-* step (the config maps
// 0.5,1,1.5,2,2.5,3,3.5,4,5,6,8,12 onto the DS; 0 and px are literal-free).
const OFF_SCALE_SPACE = String.raw`(?:7|9|10|11|14|16|20|24|28|32|36|40|44|48|52|56|60|64|72|80|96)`;
const DS_PATTERNS = [
  { name: 'arbitrary font size (use text-caption|small|ui|body|h3|…)', re: new RegExp(TW + String.raw`text-\[[0-9.]+(?:px|rem|em)\]`) },
  { name: 'off-scale Tailwind font size (no DS --type-* step)', re: new RegExp(TW + String.raw`text-(?:base|lg|xl|[2-9]xl)(?![\w-])`) },
  { name: 'arbitrary radius (use rounded-xs|sm|md|lg|pill)', re: new RegExp(TW + String.raw`rounded(?:-[trblse]{1,2})?-\[`) },
  { name: 'arbitrary spacing (use a --space-* step)', re: new RegExp(TW + SPACE_UTIL + String.raw`-\[(?!0\b|1px\]|0\.5px\])`) },
  { name: 'off-scale spacing step (no DS --space-* step)', re: new RegExp(TW + SPACE_UTIL + '-' + OFF_SCALE_SPACE + String.raw`(?![\w.-])`) },
  { name: 'arbitrary letter-spacing (use tracking-eyebrow|meta|…)', re: new RegExp(TW + String.raw`tracking-\[`) },
  { name: 'retired serif register (use font-heading)', re: /(?:^|[\s"'`:!])font-serif(?![\w-])|var\(--font-serif\)/ },
  { name: 'font-size literal (use var(--type-*-size))', re: /font-size\s*:\s*[0-9.]+(?:px|rem)\b/ },
  { name: 'border-radius literal (use var(--radius-*))', re: /border-radius\s*:\s*(?:[^;]*\s)?(?:[2-9]|[1-9][0-9]+|0?\.[0-9]+)(?:px|rem)\b/ },
  {
    name: 'spacing literal (use var(--space-*))',
    re: /(?:^|[\s;{])(?:padding|margin|gap|row-gap|column-gap)(?:-[a-z-]+)?\s*:[^;]*?(?:\b(?:[2-9]|[1-9][0-9]+)(?:\.[0-9]+)?px|\b[0-9]*\.?[0-9]+rem)\b/,
  },
];

const ALLOW_MARKER = /css-tokens-allow:/;
const ALLOW_NEXT_MARKER = /css-tokens-allow-next-line:/;
const FONT_FACE_OPEN = /@font-face\s*\{/;

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
  // The DS space/type/radius rules apply to shipped source only: test
  // fixtures carry arbitrary strings ('mr-7' as an id) that read like
  // utility classes. Colour literals stay forbidden everywhere.
  const isTestFile = /[\\/]__tests__[\\/]|\.(?:test|spec)\.[jt]sx?$/.test(file);
  let inFontFace = false;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (FONT_FACE_OPEN.test(line)) inFontFace = true;
    const fontFaceLine = inFontFace;
    if (inFontFace && line.includes('}')) inFontFace = false;
    if (ALLOW_MARKER.test(line)) continue;
    if (i > 0 && ALLOW_NEXT_MARKER.test(lines[i - 1])) continue;
    for (const { name, re } of isTestFile ? FORBIDDEN_PATTERNS : [...FORBIDDEN_PATTERNS, ...DS_PATTERNS]) {
      if (fontFaceLine && name === 'font-family stack') continue;
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
