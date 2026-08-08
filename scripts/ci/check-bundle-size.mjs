#!/usr/bin/env node
/**
 * Bundle-size CI gate (NFR-006: 1.5 MB gzipped, per shipped bundle).
 *
 * Walks the gzipped JS+CSS of every bundle frontend/vite.config.ts can
 * produce and asserts each stays under the budget.
 *
 * Paths resolve against the repo root derived from THIS FILE's location, not
 * process.cwd(). That matters: pr.yml runs the script with
 * `working-directory: ${{ github.workspace }}` (the repo root) while
 * `npm run check:bundle-size` runs it from frontend/. The old
 * `resolve(process.cwd(), 'dist/assets')` matched neither layout in CI — it
 * looked for <repo>/dist/assets, which never exists, took the "no dist"
 * early-exit and printed a friendly pass without ever gzipping a byte. Keep
 * these paths anchored to import.meta.url so the gate cannot be silenced by
 * being invoked from a different directory.
 */

import { readdirSync, statSync, readFileSync, existsSync } from 'node:fs';
import { resolve, join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../..');

const BUDGET_BYTES = 1_500_000;
// Below this much remaining headroom a passing bundle is still worth an
// annotation — "just barely under" is a heads-up, not a clean bill of health.
const NEAR_MISS_BYTES = 150_000;

/**
 * Two bundles are shipped, so two bundles are measured. vite.config.ts keys
 * the outDir off BUILD_TARGET: the default Wails desktop bundle lands in
 * frontend/dist, and BUILD_TARGET=served lands in frontend/dist-served (the
 * SPA release.yml packages as kenaz-harness-served-spa.tar.gz and the
 * workbench image fetches). Measuring only whichever directory happens to be
 * present lets a regression in the other ship unseen.
 */
const BUNDLES = [
  { name: 'desktop', dir: 'frontend/dist/assets', build: 'npm run build' },
  { name: 'served', dir: 'frontend/dist-served/assets', build: 'npm run build:served' },
];

// The budget is a JS+CSS budget, matching how NFR-006 was measured. Fonts and
// the lazily-fetched pdf.worker .mjs are reported separately below rather than
// folded in silently — see the "uncounted" line in the output.
const MEASURED = /\.(js|css)$/;

const GHA = Boolean(process.env['GITHUB_ACTIONS']);

/**
 * Emit a GitHub Actions annotation so the number lands in the PR "Checks" UI
 * instead of only in log text nobody scrolls. Messages must stay single-line
 * (GHA needs newlines percent-encoded); the plain-text mirror is what a local
 * run reads.
 */
function annotate(level, title, message) {
  if (GHA) {
    console.log(`::${level} title=${title}::${message}`);
  }
  const line = `[bundle-size] ${level.toUpperCase()}: ${message}`;
  if (level === 'error') {
    console.error(line);
  } else {
    console.log(line);
  }
}

function kb(bytes) {
  return `${(bytes / 1024).toFixed(1)} KB`;
}

/** Gzipped total of files under `dir` matching `re`. */
function gzipSum(dir, re) {
  const out = [];
  for (const f of readdirSync(dir)) {
    if (!re.test(f)) continue;
    const full = join(dir, f);
    if (!statSync(full).isFile()) continue;
    out.push({ name: f, gz: gzipSync(readFileSync(full)).length });
  }
  return out;
}

let failed = false;
let measuredAny = false;

for (const bundle of BUNDLES) {
  const dir = resolve(REPO_ROOT, bundle.dir);

  // "Not built" and "built but empty" are different facts, and conflating
  // them is precisely what hid this gate's no-op. Absent → skipped, loudly.
  if (!existsSync(dir)) {
    annotate(
      'warning',
      `bundle-size: ${bundle.name} not measured`,
      `${bundle.name} bundle skipped — ${bundle.dir} is absent. Run \`${bundle.build}\` in frontend/ before this step to cover it.`,
    );
    continue;
  }

  const files = gzipSum(dir, MEASURED);

  // Present but with nothing to weigh means the build produced no JS/CSS.
  // That is a broken build, not a pass.
  if (files.length === 0) {
    failed = true;
    annotate(
      'error',
      `bundle-size: ${bundle.name} produced no assets`,
      `${bundle.dir} exists but contains no .js/.css files — the ${bundle.name} build produced no measurable output.`,
    );
    continue;
  }

  measuredAny = true;

  const total = files.reduce((sum, f) => sum + f.gz, 0);
  const delta = total - BUDGET_BYTES;
  console.log(
    `[bundle-size] ${bundle.name}: ${files.length} files, gzipped JS+CSS = ` +
      `${total.toLocaleString('en-US')} bytes (${kb(total)}) against a budget of ` +
      `${BUDGET_BYTES.toLocaleString('en-US')} bytes.`,
  );

  // Informational: real shipped bytes the JS+CSS budget deliberately excludes.
  // Surfaced so "the bundle is 1.4 MB" is never mistaken for "we serve 1.4 MB".
  const uncounted = gzipSum(dir, /\.(mjs|woff2?|ttf|svg|png|jpe?g|webp|wasm)$/);
  if (uncounted.length > 0) {
    const uncountedTotal = uncounted.reduce((sum, f) => sum + f.gz, 0);
    console.log(
      `[bundle-size] ${bundle.name}: not counted against NFR-006 — ` +
        `${uncounted.length} font/worker/image assets, ${kb(uncountedTotal)} gzipped.`,
    );
  }

  if (delta > 0) {
    failed = true;
    annotate(
      'error',
      `bundle-size: ${bundle.name} over budget`,
      `${bundle.name} bundle is ${total.toLocaleString('en-US')} bytes gzipped — ` +
        `${delta.toLocaleString('en-US')} bytes (${kb(delta)}) OVER the ` +
        `${BUDGET_BYTES.toLocaleString('en-US')}-byte NFR-006 budget.`,
    );
    const top = [...files].sort((a, b) => b.gz - a.gz).slice(0, 5);
    console.log(`[bundle-size] ${bundle.name}: largest contributors —`);
    for (const f of top) {
      console.log(`[bundle-size]   ${kb(f.gz).padStart(9)}  ${f.name}`);
    }
    continue;
  }

  const headroom = -delta;
  if (headroom < NEAR_MISS_BYTES) {
    annotate(
      'warning',
      `bundle-size: ${bundle.name} near budget`,
      `${bundle.name} bundle is ${total.toLocaleString('en-US')} bytes gzipped — within NFR-006 but only ` +
        `${headroom.toLocaleString('en-US')} bytes (${kb(headroom)}) of headroom left.`,
    );
  } else {
    annotate(
      'notice',
      `bundle-size: ${bundle.name} within budget`,
      `${bundle.name} bundle is ${total.toLocaleString('en-US')} bytes gzipped — ` +
        `${headroom.toLocaleString('en-US')} bytes (${kb(headroom)}) of headroom.`,
    );
  }
}

// Measuring nothing at all is the exact state this gate sat in for months
// while reporting success. It is now a failure, so the no-op cannot return.
if (!measuredAny) {
  annotate(
    'error',
    'bundle-size: nothing measured',
    'no bundle was measured — build the frontend before this step. A run that ' +
      'weighs zero bytes is a broken gate, not a pass.',
  );
  process.exit(1);
}

if (failed) {
  console.error('[bundle-size] FAIL: at least one bundle breaches NFR-006.');
  process.exit(1);
}

console.log('[bundle-size] all measured bundles are within the NFR-006 budget.');
