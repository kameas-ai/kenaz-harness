/**
 * Fixture loaders for the canvas tests.
 *
 * The bundled library graphs are read FROM THE GO TREE rather than
 * copied here. A copy would drift the moment someone edits
 * `chat_default.yaml`, and the whole point of the routed-chat_default
 * fixture is that it is the real topology the canvas has to render.
 */
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
/** frontend/src/lib/canvas/__fixtures__ → repo root. */
const REPO_ROOT = resolve(HERE, '../../../../..');

export const LIBRARY_DIR = resolve(REPO_ROOT, 'core/rpc/views/agentgraph/library');

export function libraryGraphYAML(id: string): string {
  return readFileSync(resolve(LIBRARY_DIR, `${id}.yaml`), 'utf8');
}

/**
 * A minimal decision diamond: one source fanning into a decision whose
 * two verdict ports reconverge on a single sink. This is the shape
 * dagre has to close cleanly for the canvas to be legible.
 */
export const DIAMOND_YAML = `spec_version: "1"
id: diamond
entrypoints: [start]
nodes:
  - id: start
    kind: plan
    attrs: {}
  - id: gate
    kind: decision
    attrs:
      condition: "x == 1"
      next_true: left
      next_false: right
  - id: left
    kind: transform
    attrs: {}
  - id: right
    kind: transform
    attrs: {}
  - id: join
    kind: session_write
    attrs: {}
edges:
  - from: { node: start, port: out }
    to: { node: gate, port: in }
  - from: { node: gate, port: "true" }
    to: { node: left, port: in }
  - from: { node: gate, port: "false" }
    to: { node: right, port: in }
  - from: { node: left, port: out }
    to: { node: join, port: in }
  - from: { node: right, port: out }
    to: { node: join, port: in }
`;

/** A synthetic N-node chain-with-fanout used for the scale check. */
export function syntheticGraphYAML(n: number): string {
  const lines: string[] = [
    'spec_version: "1"',
    'id: synthetic',
    'entrypoints: [n0]',
    'nodes:',
  ];
  for (let i = 0; i < n; i += 1) {
    lines.push(`  - id: n${i}`);
    lines.push('    kind: transform');
    lines.push('    attrs: {}');
  }
  lines.push('edges:');
  for (let i = 1; i < n; i += 1) {
    // Every node hangs off the one two behind it, giving a wide,
    // multi-rank DAG rather than a single line.
    const parent = i >= 2 ? i - 2 : 0;
    lines.push(`  - from: { node: n${parent}, port: out }`);
    lines.push(`    to: { node: n${i}, port: in }`);
  }
  return lines.join('\n') + '\n';
}
