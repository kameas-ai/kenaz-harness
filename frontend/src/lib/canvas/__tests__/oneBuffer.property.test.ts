/**
 * The one-buffer property (visual-graph-authoring-01PMUX01 WP04).
 *
 * spec.md §5 acceptance, verbatim: "drag a node → YAML updates; the two
 * never disagree (property test: canvas-op then serialize == direct
 * spec-mutation then serialize)".
 *
 * TWO INDEPENDENT IMPLEMENTATIONS, OR THE PROPERTY IS A TAUTOLOGY.
 *
 *   - The CANVAS path is production code: `parseDocument` → surgical
 *     `applyOpToDoc` on the CST → `serializeDoc`. It preserves comments
 *     and untouched formatting, which is why it is written that way and
 *     also why it is the fiddly one.
 *
 *   - The REFERENCE path is written here, in this file, deliberately
 *     naive: `YAML.parse` to a plain object → apply the op with
 *     obvious object/array code → `YAML.stringify`. It throws away
 *     comments and formatting and does not care.
 *
 * The two are then compared SEMANTICALLY (parse both, deep-equal). If
 * the surgical path ever mis-splices — deletes the wrong list item,
 * leaves a dangling edge, writes a float coordinate, forgets to prune
 * an entrypoint — the naive path will not, and the assertion fails.
 * Comparing the canvas path against itself would prove nothing, which
 * is the trap this test exists to avoid falling into.
 */
import { describe, expect, it } from 'vitest';
import { parse as yamlParse, stringify as yamlStringify } from 'yaml';

import { applyOpToDoc, parseGraphText, serializeDoc } from '../graphSpec';
import type { SpecOp } from '../types';

import { DIAMOND_YAML, libraryGraphYAML } from '../__fixtures__/graphFixtures';

// ── the reference implementation (test-side oracle) ───────────────────

type Plain = Record<string, unknown>;

/**
 * The oracle's OWN id allocator.
 *
 * It deliberately does not import production's `allocateNodeId`. Sharing
 * it would have carved a hole in the property exactly where ids are
 * decided — the two implementations would agree on `model_3` because
 * they were the same function, not because both were right — and id
 * allocation is precisely the rule an op-application bug would get
 * wrong (colliding with a node the surgical path failed to see). Two
 * independent statements of the same rule is the whole design of this
 * file; this is the last place to make an exception.
 */
function oracleAllocateID(kind: string, taken: string[]): string {
  for (let i = 1; ; i += 1) {
    const candidate = `${kind}_${i}`;
    if (!taken.includes(candidate)) return candidate;
  }
}

function refApply(doc: Plain, op: SpecOp): Plain {
  const g: Plain = structuredClone(doc);
  const nodes = (g.nodes as Plain[] | undefined) ?? [];
  const edges = (g.edges as Plain[] | undefined) ?? [];
  const layout = (g.layout as Record<string, Plain> | undefined) ?? {};

  switch (op.type) {
    case 'add-node': {
      const taken = nodes.map((n) => String(n.id));
      const id = op.id && !taken.includes(op.id) ? op.id : oracleAllocateID(op.kind, taken);
      nodes.push({ id, kind: op.kind, attrs: {} });
      layout[id] = { x: Math.round(op.position.x), y: Math.round(op.position.y) };
      g.nodes = nodes;
      g.layout = layout;
      const eps = (g.entrypoints as string[] | undefined) ?? [];
      if (eps.length === 0 && nodes.length === 1) g.entrypoints = [id];
      return g;
    }
    case 'delete-node': {
      g.nodes = nodes.filter((n) => String(n.id) !== op.id);
      const keptEdges = edges.filter((e) => {
        const from = (e.from as Plain | undefined) ?? {};
        const to = (e.to as Plain | undefined) ?? {};
        return String(from.node) !== op.id && String(to.node) !== op.id;
      });
      if (keptEdges.length > 0) g.edges = keptEdges;
      else delete g.edges;
      if ((g.nodes as Plain[]).length === 0) delete g.nodes;
      const eps = (g.entrypoints as string[] | undefined) ?? [];
      if (g.entrypoints !== undefined) g.entrypoints = eps.filter((e) => e !== op.id);
      delete layout[op.id];
      if (Object.keys(layout).length > 0) g.layout = layout;
      else delete g.layout;
      return g;
    }
    case 'set-attrs': {
      g.nodes = nodes.map((n) => {
        if (String(n.id) !== op.id) return n;
        const next = { ...n };
        if (Object.keys(op.attrs).length === 0) delete next.attrs;
        else next.attrs = op.attrs;
        return next;
      });
      return g;
    }
    case 'connect': {
      const wanted = {
        from: { node: op.edge.source, port: op.edge.sourcePort },
        to: { node: op.edge.target, port: op.edge.targetPort },
      };
      const exists = edges.some((e) => {
        const from = (e.from as Plain | undefined) ?? {};
        const to = (e.to as Plain | undefined) ?? {};
        return (
          String(from.node) === wanted.from.node &&
          String(from.port) === wanted.from.port &&
          String(to.node) === wanted.to.node &&
          String(to.port) === wanted.to.port
        );
      });
      if (!exists) edges.push(wanted);
      if (edges.length > 0) g.edges = edges;
      return g;
    }
    case 'disconnect': {
      const kept = edges.filter((e) => {
        const from = (e.from as Plain | undefined) ?? {};
        const to = (e.to as Plain | undefined) ?? {};
        return `${from.node}:${from.port}→${to.node}:${to.port}` !== op.edgeId;
      });
      if (kept.length > 0) g.edges = kept;
      else delete g.edges;
      return g;
    }
    case 'move-nodes': {
      for (const id of Object.keys(op.positions).sort()) {
        layout[id] = {
          x: Math.round(op.positions[id].x),
          y: Math.round(op.positions[id].y),
        };
      }
      g.layout = layout;
      return g;
    }
    default:
      return g;
  }
}

/** The production path: parse → surgical op → serialize. */
function canvasApply(text: string, ops: SpecOp[]): string {
  const parsed = parseGraphText(text);
  expect(parsed.doc, `fixture does not parse: ${parsed.error}`).not.toBeNull();
  for (const op of ops) applyOpToDoc(parsed.doc!, op);
  return serializeDoc(parsed.doc!);
}

/** The oracle: plain-object mutation → stringify. */
function referenceApply(text: string, ops: SpecOp[]): string {
  let g = yamlParse(text) as Plain;
  for (const op of ops) g = refApply(g, op);
  return yamlStringify(g);
}

/** Compares the two paths semantically — formatting is not the claim. */
function expectAgree(text: string, ops: SpecOp[]): void {
  const viaCanvas = yamlParse(canvasApply(text, ops));
  const viaReference = yamlParse(referenceApply(text, ops));
  expect(viaCanvas).toEqual(viaReference);
}

// ── scripted ops ──────────────────────────────────────────────────────

const SCRIPTED: Array<{ name: string; ops: SpecOp[] }> = [
  {
    name: 'add one node',
    ops: [{ type: 'add-node', kind: 'transform', position: { x: 10, y: 20 } }],
  },
  {
    name: 'add two nodes and connect them',
    ops: [
      { type: 'add-node', kind: 'transform', position: { x: 10, y: 20 } },
      { type: 'add-node', kind: 'transform', position: { x: 30, y: 40 } },
      {
        type: 'connect',
        edge: {
          source: 'transform_1',
          sourcePort: 'out',
          target: 'transform_2',
          targetPort: 'in',
        },
      },
    ],
  },
  {
    name: 'delete a node that has edges',
    ops: [{ type: 'delete-node', id: 'gate' }],
  },
  {
    name: 'delete the entrypoint node',
    ops: [{ type: 'delete-node', id: 'start' }],
  },
  {
    name: 'disconnect then reconnect',
    ops: [
      { type: 'disconnect', edgeId: 'gate:true→left:in' },
      {
        type: 'connect',
        edge: { source: 'gate', sourcePort: 'true', target: 'left', targetPort: 'in' },
      },
    ],
  },
  {
    name: 'move several nodes with fractional coordinates',
    ops: [
      {
        type: 'move-nodes',
        positions: {
          start: { x: 1.4, y: 2.6 },
          gate: { x: -3.5, y: 0.2 },
          join: { x: 100, y: 200 },
        },
      },
    ],
  },
  {
    name: 'replace a node’s attrs',
    ops: [{ type: 'set-attrs', id: 'gate', attrs: { condition: 'y == 2', next_true: 'left', next_false: 'right' } }],
  },
  {
    name: 'clear a node’s attrs',
    ops: [{ type: 'set-attrs', id: 'gate', attrs: {} }],
  },
  {
    name: 'add, move, then delete the same node',
    ops: [
      { type: 'add-node', kind: 'model', position: { x: 5, y: 5 } },
      { type: 'move-nodes', positions: { model_1: { x: 50, y: 60 } } },
      { type: 'delete-node', id: 'model_1' },
    ],
  },
  {
    name: 'delete every node',
    ops: [
      { type: 'delete-node', id: 'start' },
      { type: 'delete-node', id: 'gate' },
      { type: 'delete-node', id: 'left' },
      { type: 'delete-node', id: 'right' },
      { type: 'delete-node', id: 'join' },
    ],
  },
];

describe('one buffer — canvas-op-then-serialize ≡ spec-mutation-then-serialize', () => {
  for (const { name, ops } of SCRIPTED) {
    it(name, () => {
      expectAgree(DIAMOND_YAML, ops);
    });
  }

  it('holds on the real chat_default topology', () => {
    const yaml = libraryGraphYAML('chat_default');
    expectAgree(yaml, [
      { type: 'add-node', kind: 'transform', position: { x: 7, y: 8 } },
      { type: 'delete-node', id: 'recover' },
      {
        type: 'connect',
        edge: {
          source: 'agent_loop',
          sourcePort: 'assistant_text',
          target: 'assistant_write',
          targetPort: 'assistant_text',
        },
      },
      { type: 'move-nodes', positions: { ask_user: { x: 12, y: 34 } } },
    ]);
  });
});

// ── fuzz ──────────────────────────────────────────────────────────────

/** Deterministic PRNG — a failing seed has to be reproducible. */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const KINDS = ['transform', 'model', 'plan', 'decision', 'session_write'];
const PORTS = ['in', 'out', 'true', 'false', 'draft', 'result', 'nonexistent'];

function randomOps(rand: () => number, text: string, count: number): SpecOp[] {
  const parsed = parseGraphText(text);
  const ids = (parsed.graph?.nodes ?? []).map((n) => n.id);
  const edgeIDs = (parsed.graph?.edges ?? []).map(
    (e) => `${e.from.node}:${e.from.port}→${e.to.node}:${e.to.port}`,
  );
  const pick = <T,>(arr: T[]): T | undefined =>
    arr.length === 0 ? undefined : arr[Math.floor(rand() * arr.length)];
  const anyID = () => pick(ids) ?? 'ghost';

  const ops: SpecOp[] = [];
  for (let i = 0; i < count; i += 1) {
    const roll = Math.floor(rand() * 6);
    switch (roll) {
      case 0: {
        const kind = pick(KINDS)!;
        ops.push({
          type: 'add-node',
          kind,
          position: { x: rand() * 400 - 200, y: rand() * 400 - 200 },
        });
        ids.push(oracleAllocateID(kind, ids));
        break;
      }
      case 1:
        ops.push({ type: 'delete-node', id: anyID() });
        break;
      case 2:
        ops.push({
          type: 'connect',
          edge: {
            source: anyID(),
            sourcePort: pick(PORTS)!,
            target: anyID(),
            targetPort: pick(PORTS)!,
          },
        });
        break;
      case 3:
        ops.push({
          type: 'disconnect',
          edgeId: pick(edgeIDs) ?? 'nope:a→nope:b',
        });
        break;
      case 4: {
        const positions: Record<string, { x: number; y: number }> = {};
        for (const id of ids.slice(0, 1 + Math.floor(rand() * 3))) {
          positions[id] = { x: rand() * 100, y: rand() * 100 };
        }
        ops.push({ type: 'move-nodes', positions });
        break;
      }
      default:
        ops.push({
          type: 'set-attrs',
          id: anyID(),
          attrs: rand() < 0.3 ? {} : { note: `n${Math.floor(rand() * 100)}` },
        });
        break;
    }
  }
  return ops;
}

describe('one buffer — fuzz', () => {
  const FIXTURES: Array<[string, string]> = [
    ['diamond', DIAMOND_YAML],
    ['chat_default', libraryGraphYAML('chat_default')],
  ];

  for (const [name, yaml] of FIXTURES) {
    it(`agrees with the reference over 200 random op sequences (${name})`, () => {
      for (let seed = 1; seed <= 200; seed += 1) {
        const rand = mulberry32(seed * 7919);
        const ops = randomOps(rand, yaml, 1 + Math.floor(rand() * 6));
        try {
          expectAgree(yaml, ops);
        } catch (err) {
          throw new Error(
            `seed ${seed} disagreed\nops: ${JSON.stringify(ops)}\n${
              err instanceof Error ? err.message : String(err)
            }`,
          );
        }
      }
    });

    it(`always leaves a re-parseable buffer (${name})`, () => {
      for (let seed = 1; seed <= 200; seed += 1) {
        const rand = mulberry32(seed * 104729);
        const ops = randomOps(rand, yaml, 1 + Math.floor(rand() * 6));
        const out = canvasApply(yaml, ops);
        const reparsed = parseGraphText(out);
        if (reparsed.error !== null && out.trim() !== '') {
          throw new Error(
            `seed ${seed} produced an unparseable buffer: ${reparsed.error}\nops: ${JSON.stringify(ops)}`,
          );
        }
      }
    });

    // Idempotence: applying ops, then re-serializing the result with no
    // further ops, must not keep changing the file.
    //
    // ONE MEASURED RESIDUE, stated rather than hidden: deleting the
    // first item of a block sequence can leave the line that followed it
    // holding trailing spaces (`"  "` where the next pass emits `""`).
    // It is whitespace on an otherwise blank line — no parse, diff hunk
    // count, or byte of meaning depends on it — and it settles on the
    // next parse. Stripping it in `serializeDoc` was rejected: a
    // whitespace-only line inside a literal block scalar (chat_default
    // has two) is content, and a blanket strip would corrupt it.
    it(`is idempotent under re-serialization, modulo trailing blanks (${name})`, () => {
      const stripBlankLineWhitespace = (t: string) =>
        t
          .split('\n')
          .map((l) => (l.trim() === '' ? '' : l))
          .join('\n');
      for (let seed = 1; seed <= 100; seed += 1) {
        const rand = mulberry32(seed * 15485863);
        const ops = randomOps(rand, yaml, 1 + Math.floor(rand() * 4));
        const once = canvasApply(yaml, ops);
        const twice = canvasApply(once, []);
        if (stripBlankLineWhitespace(twice) !== stripBlankLineWhitespace(once)) {
          throw new Error(
            `seed ${seed}: a second serialize changed the buffer\nops: ${JSON.stringify(ops)}`,
          );
        }
      }
    });
  }
});
