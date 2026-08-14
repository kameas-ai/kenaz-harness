/**
 * The adapter boundary defends itself
 * (visual-graph-authoring-01PMUX01 WP02, FR-001).
 *
 * `types.ts` closes with a claim in prose: "Nothing in this file imports
 * from `@/lib/types`' graph shapes or from a workflow type. If a future
 * edit needs one, the adapter boundary has been broken." Prose does not
 * fail CI. This does.
 *
 * The rule is deliberately absolute — NO imports at all, not "no
 * spec-family imports". A whitelist would need maintaining and would
 * invite the first exception; a flat zero is unambiguous and the file
 * has no legitimate need for one (it is types plus three pure helper
 * functions). The moment WP06's workflows adapter tempts someone to
 * reach into a `Step` type from here, this test says no.
 */
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

const TYPES_FILE = resolve(
  dirname(fileURLToPath(import.meta.url)),
  '../types.ts',
);

describe('CanvasAdapter boundary', () => {
  it('types.ts imports nothing — the canvas contract is spec-family-agnostic', () => {
    const source = readFileSync(TYPES_FILE, 'utf8');
    const offenders = source
      .split('\n')
      .map((line, i) => [i + 1, line] as const)
      .filter(([, line]) => /^\s*import\s/.test(line) || /^\s*export\s+.*\sfrom\s/.test(line))
      .map(([n, line]) => `${n}: ${line.trim()}`);

    expect(
      offenders,
      'types.ts must not import anything — see the paper check in its header. ' +
        'An adapter contract that reaches into a spec family is no longer a contract.',
    ).toEqual([]);
  });

  it('names no spec-family type, in an import or anywhere else', () => {
    const source = readFileSync(TYPES_FILE, 'utf8');
    // Identifiers that would mean the boundary leaked. Checked against
    // code only — the header's paper check discusses both families in
    // prose on purpose, and that is the point of it.
    const code = source
      .split('\n')
      .filter((line) => !/^\s*(\*|\/\*|\/\/)/.test(line))
      .join('\n');
    for (const banned of ['GraphSpec', 'NodeManifest', 'ParsedGraph', 'StepKind', 'Workflow']) {
      expect(code, `types.ts names ${banned}`).not.toContain(banned);
    }
  });
});
