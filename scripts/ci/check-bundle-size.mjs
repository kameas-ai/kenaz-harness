#!/usr/bin/env node
/**
 * Bundle-size CI gate (NFR-006: 1.5 MB gzipped).
 * Walks frontend/dist/assets and asserts the gzipped sum stays under
 * the budget. Trivial pass on an empty scaffold.
 */

import { readdirSync, statSync, readFileSync, existsSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { gzipSync } from 'node:zlib';

const DIST = resolve(process.cwd(), 'dist/assets');
const BUDGET_BYTES = 1_500_000;

if (!existsSync(DIST)) {
  console.log('[bundle-size] no dist/assets — pass (run `npm run build`).');
  process.exit(0);
}

let total = 0;
for (const f of readdirSync(DIST)) {
  if (!f.endsWith('.js') && !f.endsWith('.css')) continue;
  const full = join(DIST, f);
  const s = statSync(full);
  if (!s.isFile()) continue;
  const gz = gzipSync(readFileSync(full));
  total += gz.length;
}

console.log(`[bundle-size] gzipped JS+CSS = ${total} bytes (budget ${BUDGET_BYTES})`);
if (total > BUDGET_BYTES) {
  console.error('[bundle-size] FAIL: bundle exceeds NFR-006 budget');
  process.exit(1);
}

console.log('[bundle-size] within NFR-006 budget.');
