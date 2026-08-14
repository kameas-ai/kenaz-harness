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

/**
 * A materialized run, as `Graph_MaterializeRun` projects one
 * (visual-graph-authoring-01PMUX01 WP05).
 *
 * The shape is what makes it useful rather than the size: it carries
 * one node of EVERY status the materializer can emit — `completed`,
 * `error`, `skipped`, `not_reached`, `incomplete` — plus the `@N`
 * iteration-instance ids the unroller produces, plus `start_seq` on each
 * so the trace click-through has a real join key to follow. Statuses the
 * overlay claims to render but that no fixture exercises are statuses
 * nobody has actually seen render.
 *
 * `spec_provenance` is deliberately ABSENT here: the degraded case gets
 * its own fixture below so the healthy path pins the badge's absence.
 */
export const MATERIALIZED_RUN_YAML = `spec_version: "1"
id: chat_default__run_r1
name: Default chat graph — run r1
entrypoints: [history_in@1]
nodes:
  - id: history_in@1
    kind: session_read
    attrs: {}
    materialized:
      source_node: history_in
      instance: 1
      status: completed
      start_seq: 2
      end_seq: 3
  - id: assistant_turn@1
    kind: model
    attrs: {}
    materialized:
      source_node: assistant_turn
      instance: 1
      iteration: 1
      status: completed
      start_seq: 4
      end_seq: 9
  - id: assistant_turn@2
    kind: model
    attrs: {}
    materialized:
      source_node: assistant_turn
      instance: 2
      iteration: 2
      status: incomplete
      start_seq: 10
  - id: tool_leg@1
    kind: transform
    attrs: {}
    materialized:
      source_node: tool_leg
      instance: 1
      status: error
      start_seq: 6
      end_seq: 7
  - id: skipped_leg@1
    kind: transform
    attrs: {}
    materialized:
      source_node: skipped_leg
      instance: 1
      status: skipped
      start_seq: 8
  - id: never_ran
    kind: transform
    attrs: {}
    materialized:
      source_node: never_ran
      status: not_reached
edges:
  - from: { node: history_in@1, port: out }
    to: { node: assistant_turn@1, port: in }
  - from: { node: assistant_turn@1, port: out }
    to: { node: tool_leg@1, port: in }
  - from: { node: assistant_turn@1, port: out }
    to: { node: skipped_leg@1, port: in }
  - from: { node: assistant_turn@1, port: out }
    to: { node: assistant_turn@2, port: in }
`;

/**
 * The same run, projected against the library file because the resolved
 * spec it executed had been evicted. The ONLY difference is the
 * `spec_provenance` marker — which is the point: the badge has to come
 * from that one scalar and not from anything about the topology.
 */
export const MATERIALIZED_DEGRADED_YAML =
  MATERIALIZED_RUN_YAML + 'spec_provenance: library_fallback\n';

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
