<script setup lang="ts">
/**
 * GraphEditor — Bundle A WP06 / mission agent-kernel-graph-node-catalog
 * WP06 frontend palette + manifest-driven attribute editor surface.
 *
 * Three-pane layout:
 *   - Left: NodePalette (Category → Archetype → Kind tree + filter)
 *   - Middle: existing YAML textarea + validation pane (unchanged)
 *   - Right: NodeAttributeEditor for the selected node
 *
 * The parser is intentionally tiny — we only parse the `nodes:` list at
 * top-level, capturing each entry's id, kind, and attrs. The YAML
 * package is not a dependency, so we operate via a pragmatic regex /
 * string-builder. Round-tripping through a real YAML parser is a v2
 * follow-up; for v1, attr edits rewrite the targeted node block.
 */
import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { GraphSpec, GraphValidationResult } from '@/lib/types';
import NodePalette from './NodePalette.vue';
import NodeAttributeEditor from './NodeAttributeEditor.vue';
import GraphCanvas from '@/components/canvas/GraphCanvas.vue';
import {
  useManifestStore,
  useNodeManifest,
  useNodeManifests,
} from '@/composables/useNodeManifest';
import { buildGraphAdapter, defaultAttrsForKind } from '@/lib/canvas/graphAdapter';
import {
  applyOpToDoc,
  parseGraphText,
  pruneStaleLayout,
  serializeDoc,
  type ParsedGraph,
} from '@/lib/canvas/graphSpec';
import type { SpecOp } from '@/lib/canvas/types';

const client = useHarnessClient();
const route = useRoute();
const router = useRouter();

const newGraphTemplate = `spec_version: "1"
id: my_graph
name: My Graph
entrypoints: [start]
nodes:
  - id: start
    kind: plan
    attrs:
      verbosity: standard
`;

const id = computed(() => String(route.params.id ?? ''));
// Set on the /agentgraph/run/:runId/graph route: this editor instance is
// showing an EXECUTED run projected back into a graph, not a library
// file (agentgraph-total-convergence-01PMGX01 WP12).
const materializedRunId = computed(() => String(route.params.runId ?? ''));

const yaml = ref('');
const scope = ref<'library' | 'user' | 'materialized'>('user');
const editingId = ref('');
const error = ref<string | null>(null);
const validation = ref<GraphValidationResult | null>(null);
const saved = ref(false);

// Anything that is not a user graph is read-only: bundled library
// graphs because they ship with the binary, materialized runs because
// they are records of something that already happened.
const readOnly = computed(() => scope.value !== 'user');
const isMaterialized = computed(() => scope.value === 'materialized');
/**
 * A materialized run whose resolved spec was no longer available is
 * projected against the library file instead, and the backend stamps
 * `spec_provenance: library_fallback` on the graph
 * (agentgraph-total-convergence-01PMGX01 WP12, review F2). The topology
 * shown may then differ from the topology that ran, so it is badged
 * rather than served as if it were faithful.
 */
/*
 * LATENT HAZARD, routed to Phase 8 (WP12 review N3): the node palette
 * and the attribute editor mutate `yaml` directly (appendStubNode /
 * onPaletteSelect) without consulting `readOnly`. Nothing can be saved
 * from a read-only buffer — `save()` returns early and no Save control
 * renders — so the worst case today is a viewer editing a scratch copy
 * of a materialized run and losing it on navigation. Gating the palette
 * itself is the real fix and it touches the whole three-pane editor, so
 * it is not smuggled into this WP.
 */
const isDegraded = computed(() =>
  /^spec_provenance:\s*"?library_fallback"?\s*$/m.test(yaml.value),
);
const canSave = computed(() => !readOnly.value && !!yaml.value.trim());

// ── lightweight YAML "node block" parser ──────────────────────────────

interface ParsedNode {
  id: string;
  kind: string;
  attrs: Record<string, unknown>;
  /** char offsets of the entire `- id: …` block within the YAML text. */
  start: number;
  end: number;
}

/**
 * Walks the YAML text line by line, looking for the top-level `nodes:`
 * key, then iterates each `  - id: <id>` block under it. Returns the
 * node entries with their character offsets so the editor can rewrite
 * a single block in place. Tolerates quoted ids and inline `attrs:`
 * maps; falls back to leaving the block untouched if the structure is
 * unrecognised.
 */
function parseNodes(text: string): ParsedNode[] {
  const lines = text.split('\n');
  // Compute char offsets for line starts.
  const offsets: number[] = [];
  let off = 0;
  for (const l of lines) {
    offsets.push(off);
    off += l.length + 1;
  }
  const nodesIdx = lines.findIndex((l) => /^nodes\s*:\s*$/.test(l));
  if (nodesIdx < 0) return [];
  // Determine sub-indent: first non-blank child line.
  const nodes: ParsedNode[] = [];
  let i = nodesIdx + 1;
  let curStart = -1;
  let curId = '';
  let curKind = '';
  let curAttrs: Record<string, unknown> = {};
  let inAttrs = false;
  let attrIndent = 0;

  function flush(endLine: number) {
    if (curStart >= 0) {
      nodes.push({
        id: curId,
        kind: curKind,
        attrs: curAttrs,
        start: curStart,
        end: offsets[endLine] ?? text.length,
      });
    }
    curStart = -1;
    curId = '';
    curKind = '';
    curAttrs = {};
    inAttrs = false;
    attrIndent = 0;
  }

  for (; i < lines.length; i++) {
    const line = lines[i];
    // Top-level key (no leading space, ends with `:`) ends the nodes block.
    if (/^[A-Za-z_][\w-]*\s*:/.test(line)) {
      flush(i);
      break;
    }
    // Skip blank lines.
    if (line.trim() === '') continue;

    const dashMatch = /^(\s*)-\s+id\s*:\s*"?([^"#\s]+)"?\s*$/.exec(line);
    if (dashMatch) {
      flush(i);
      curStart = offsets[i];
      curId = dashMatch[2];
      continue;
    }
    // Sub-key inside the current node entry.
    if (curStart < 0) continue;
    const kindMatch = /^\s+kind\s*:\s*"?([^"#\s]+)"?\s*$/.exec(line);
    if (kindMatch) {
      curKind = kindMatch[1];
      inAttrs = false;
      continue;
    }
    const attrsHeader = /^(\s+)attrs\s*:\s*$/.exec(line);
    if (attrsHeader) {
      inAttrs = true;
      attrIndent = attrsHeader[1].length + 2;
      continue;
    }
    if (inAttrs) {
      const m = new RegExp(
        `^\\s{${attrIndent},}([A-Za-z_][\\w-]*)\\s*:\\s*(.*)$`,
      ).exec(line);
      if (m) {
        const k = m[1];
        const raw = m[2];
        curAttrs[k] = parseScalar(raw);
        continue;
      }
      // De-dent ends the attrs block.
      const indent = (line.match(/^\s*/) ?? [''])[0].length;
      if (indent < attrIndent) {
        inAttrs = false;
      }
    }
  }
  // Flush the last block at end-of-list.
  if (curStart >= 0) {
    flush(lines.length);
  }
  return nodes;
}

function parseScalar(raw: string): unknown {
  const t = raw.trim();
  if (t === '') return '';
  if (t === 'true') return true;
  if (t === 'false') return false;
  if (t === 'null' || t === '~') return null;
  if (/^-?\d+$/.test(t)) return parseInt(t, 10);
  if (/^-?\d+\.\d+$/.test(t)) return parseFloat(t);
  // Strip surrounding quotes.
  if (
    (t.startsWith('"') && t.endsWith('"')) ||
    (t.startsWith("'") && t.endsWith("'"))
  ) {
    return t.slice(1, -1);
  }
  // Inline list `[a, b, c]` — best-effort split.
  if (t.startsWith('[') && t.endsWith(']')) {
    const inner = t.slice(1, -1).trim();
    if (inner === '') return [];
    return inner.split(',').map((s) => parseScalar(s));
  }
  return t;
}

function serializeAttrs(attrs: Record<string, unknown>, indent: string): string {
  const keys = Object.keys(attrs);
  if (keys.length === 0) return '';
  const lines: string[] = [`${indent}attrs:`];
  for (const k of keys) {
    lines.push(`${indent}  ${k}: ${serializeScalar(attrs[k])}`);
  }
  return lines.join('\n') + '\n';
}

function serializeScalar(v: unknown): string {
  if (v === null) return 'null';
  if (typeof v === 'boolean') return v ? 'true' : 'false';
  if (typeof v === 'number') return String(v);
  if (Array.isArray(v)) {
    return `[${v.map((x) => serializeScalar(x)).join(', ')}]`;
  }
  if (typeof v === 'object') {
    return JSON.stringify(v);
  }
  const s = String(v);
  // Quote if it looks ambiguous.
  if (s === '' || /[:#\s]/.test(s)) return JSON.stringify(s);
  return s;
}

function rewriteNodeAttrs(
  text: string,
  node: ParsedNode,
  nextAttrs: Record<string, unknown>,
): string {
  // The block starts at offset node.start and ends at node.end. We
  // rebuild the block preserving the leading dash-line and the kind
  // line, then emit a fresh attrs map.
  const block = text.slice(node.start, node.end);
  const blockLines = block.split('\n');
  // Detect the dash-indent (the `-` on the first line) and child indent.
  const dashLine = blockLines[0];
  const dashIndent = (dashLine.match(/^\s*/) ?? [''])[0];
  const childIndent = `${dashIndent}  `;

  const out: string[] = [];
  let attrsAdded = false;
  for (const line of blockLines) {
    if (/^\s+attrs\s*:\s*$/.test(line)) {
      if (!attrsAdded) {
        out.push(serializeAttrs(nextAttrs, childIndent).trimEnd());
        attrsAdded = true;
      }
      // Skip subsequent attr child lines.
      continue;
    }
    // Drop attr child lines if we're past the attrs header.
    if (
      attrsAdded &&
      line.startsWith(`${childIndent}  `) &&
      /^\s+[A-Za-z_]/.test(line)
    ) {
      continue;
    }
    out.push(line);
  }
  if (!attrsAdded && Object.keys(nextAttrs).length > 0) {
    out.splice(out.length - 1, 0, serializeAttrs(nextAttrs, childIndent).trimEnd());
  }
  const rebuilt = out.join('\n');
  return text.slice(0, node.start) + rebuilt + text.slice(node.end);
}

// ── reactive parsed nodes & selection ────────────────────────────────

const parsedNodes = computed<ParsedNode[]>(() => parseNodes(yaml.value));
const selectedNodeId = ref('');
const selectedNode = computed<ParsedNode | null>(() => {
  if (!selectedNodeId.value) return null;
  return (
    parsedNodes.value.find((n) => n.id === selectedNodeId.value) ?? null
  );
});
const selectedKindId = computed<string>(() => selectedNode.value?.kind ?? '');

// useNodeManifest fetches the resolved manifest for the selected kind.
const { manifest: selectedManifest, loading: manifestLoading } =
  useNodeManifest(selectedKindId);

const graphNodeIds = computed(() => parsedNodes.value.map((n) => n.id));

function onAttrUpdate(next: Record<string, unknown>) {
  const node = selectedNode.value;
  if (!node) return;
  yaml.value = rewriteNodeAttrs(yaml.value, node, next);
}

// ── canvas (visual-graph-authoring-01PMUX01 WP02 render, WP03 author) ─
//
// The canvas edits the SAME buffer the textarea edits. Every canvas op
// is applied to the parsed YAML Document and serialised straight back
// into `yaml`, so there is one buffer, one dirty state, and one save
// path — no second in-memory model that can drift (spec §4).

/**
 * Last graph the buffer parsed to. A parse failure keeps the previous
 * value, so a half-typed line does not blank the canvas: it holds its
 * last good render while the validation pane reports the error (spec
 * FR-003).
 */
const lastGoodGraph = ref<ParsedGraph | null>(null);
const canvasParseError = ref<string | null>(null);

watch(
  yaml,
  (text) => {
    const parsed = parseGraphText(text);
    canvasParseError.value = parsed.error;
    if (parsed.graph) lastGoodGraph.value = parsed.graph;
  },
  { immediate: true },
);

const manifestStore = useManifestStore();
const canvasKinds = computed(() => (lastGoodGraph.value?.nodes ?? []).map((n) => n.kind));
const { details: kindDetails, ensure: ensureKindDetail } =
  useNodeManifests(canvasKinds);

const canvasRef = ref<InstanceType<typeof GraphCanvas> | null>(null);

/**
 * Applies one canvas op to the buffer. The Document API is what keeps
 * the YAML pane readable: a full parse/stringify round-trip would erase
 * every comment in the file on the first node drag.
 */
async function applyCanvasOp(op: SpecOp) {
  if (readOnly.value) return;
  // A kind dropped from the palette is by definition not in the graph
  // yet, so its manifest was never fetched — and the manifest is where
  // the node's default attrs come from. Resolve it before writing, or
  // the new node lands with an empty `attrs: {}` and the author has to
  // discover every required field by failing validation.
  if (op.type === 'add-node') await ensureKindDetail(op.kind);
  const parsed = parseGraphText(yaml.value);
  if (!parsed.doc) return; // unparseable buffer: the textarea owns the fix
  const newID = applyOpToDoc(parsed.doc, op, {
    attrsFor: (kind) => defaultAttrsForKind(kindDetails.value[kind]),
  });
  yaml.value = serializeDoc(parsed.doc);
  if (newID) selectedNodeId.value = newID;
  if (op.type === 'delete-node' && selectedNodeId.value === op.id) {
    selectedNodeId.value = '';
  }
}

/**
 * Drag-time edge legality. The graph goes over as JSON — the buffer the
 * canvas is showing, parsed once — and the verdict comes back from the
 * same Go rules `Validate()` runs (`Graph_CheckEdge`). One rule source,
 * so the canvas can never accept an edge the save then refuses.
 */
async function checkCanvasEdge(edge: {
  source: string;
  sourcePort: string;
  target: string;
  targetPort: string;
}) {
  const parsed = parseGraphText(yaml.value);
  if (!parsed.doc) {
    return { ok: false, reason: parsed.error ?? 'The graph does not parse.' };
  }
  try {
    return await client.graph.checkEdge(JSON.stringify(parsed.doc.toJS()), {
      from: { node: edge.source, port: edge.sourcePort },
      to: { node: edge.target, port: edge.targetPort },
    });
  } catch (err) {
    return { ok: false, reason: err instanceof Error ? err.message : String(err) };
  }
}

const canvasAdapter = computed(() =>
  buildGraphAdapter({
    graph: lastGoodGraph.value,
    manifests: manifestStore.manifests.value ?? [],
    details: kindDetails.value,
    readOnly: readOnly.value,
    checkEdge: checkCanvasEdge,
    applyOp: applyCanvasOp,
  }),
);

function onCanvasSelect(id: string) {
  selectedNodeId.value = id;
}

// ── palette ──────────────────────────────────────────────────────────
//
// WP03 DELETED the palette's drop-into-the-text-buffer path. Dropping a
// kind onto the YAML textarea used to append a stub block; the drop
// target is the canvas now, and a palette CLICK routes through the same
// op the drop does. That also closes half of the WP12-review N3 hazard
// in passing: the palette used to mutate `yaml` without consulting
// `readOnly`, and `onSpecOp` on a read-only adapter is a no-op by
// construction. (The attribute editor is the other half; WP05 finishes
// it.)

function onPaletteSelect({ kind }: { kind: string }) {
  void applyCanvasOp({ type: 'add-node', kind, position: { x: 0, y: 0 } });
}

// ── load / validate / save ───────────────────────────────────────────

async function load() {
  error.value = null;
  validation.value = null;
  saved.value = false;
  if (materializedRunId.value) {
    try {
      const spec = await client.graph.materializeRun(materializedRunId.value);
      yaml.value = spec.yaml;
      scope.value = spec.scope;
      editingId.value = spec.id;
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err);
    }
    return;
  }
  if (id.value === '__new__') {
    yaml.value = newGraphTemplate;
    scope.value = 'user';
    editingId.value = 'my_graph';
    return;
  }
  try {
    const spec = await client.graph.loadGraph(id.value);
    yaml.value = spec.yaml;
    scope.value = spec.scope;
    editingId.value = spec.id;
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

async function validate() {
  error.value = null;
  try {
    validation.value = await client.graph.validate(yaml.value);
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

/**
 * Writes the canvas arrangement into the buffer's `layout:` block.
 *
 * Called ONLY from save(). Persisting on every drag would turn each
 * twitch of the mouse into a YAML diff (plan.md, "Layout churn in
 * diffs"), so live positions stay in the canvas component until the
 * author commits. Stale entries — nodes renamed or deleted by a hand
 * edit — are pruned in the same pass, which is the moment spec.go's
 * Layout doc comment nominates for it.
 */
function persistCanvasLayout() {
  const canvas = canvasRef.value;
  if (!canvas) return;
  const positions = canvas.pendingLayout();
  const parsed = parseGraphText(yaml.value);
  if (!parsed.doc) return;
  if (Object.keys(positions).length > 0) {
    applyOpToDoc(parsed.doc, { type: 'move-nodes', positions });
  }
  pruneStaleLayout(parsed.doc);
  const next = serializeDoc(parsed.doc);
  if (next !== yaml.value) yaml.value = next;
}

async function save() {
  if (readOnly.value) return;
  saved.value = false;
  error.value = null;
  persistCanvasLayout();
  let v: GraphValidationResult;
  try {
    v = await client.graph.validate(yaml.value);
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    return;
  }
  validation.value = v;
  if (!v.ok) {
    error.value = 'Resolve validation errors before saving.';
    return;
  }
  const matched = /^id:\s*"?(.+?)"?\s*$/m.exec(yaml.value);
  if (matched) {
    editingId.value = matched[1].trim();
  }
  const spec: GraphSpec = {
    id: editingId.value,
    scope: 'user',
    yaml: yaml.value,
  };
  try {
    await client.graph.saveGraph(spec);
    saved.value = true;
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

function backToList() {
  void router.push({ name: 'graphs' });
}

onMounted(() => {
  void load();
});

watch(
  () => [id.value, materializedRunId.value],
  () => {
    void load();
  },
);

// Re-validate live whenever YAML changes (mirrors the existing peer
// behaviour: validation refreshes on click; we keep the explicit click
// path for parity but also allow a fast follow-up for live feedback).
watch(yaml, () => {
  saved.value = false;
});

// Auto-clear selection if the user deletes the selected node from YAML.
watch(parsedNodes, (next) => {
  if (
    selectedNodeId.value &&
    !next.some((n) => n.id === selectedNodeId.value)
  ) {
    selectedNodeId.value = '';
  }
});

defineExpose({
  load,
  validate,
  save,
  parseNodes,
  // WP03: `appendStubNode` is gone — dropping a kind into the TEXT
  // buffer was the pre-canvas authoring path and the canvas replaces
  // it. Palette clicks and canvas drops both emit the same add-node op.
  applyCanvasOp,
  persistCanvasLayout,
});
</script>

<template>
  <div>
    <CanvasHead
      number="12"
      section="GRAPHS"
      :title="
        isMaterialized
          ? 'Run as graph (read-only)'
          : readOnly
            ? 'View graph (read-only)'
            : 'Edit graph'
      "
      :subtitle="
        isMaterialized
          ? `Run ${materializedRunId} — every action the run took, as nodes`
          : `Editing ${editingId || 'new graph'}`
      "
    >
      <template #trailing>
        <div class="flex gap-2">
          <button
            type="button"
            class="rounded-sm border border-border-muted px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
            @click="backToList"
          >
            Back
          </button>
          <button
            type="button"
            class="rounded-sm border border-border-muted px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
            data-testid="editor-validate"
            @click="validate"
          >
            Validate
          </button>
          <button
            v-if="!readOnly"
            type="button"
            :disabled="!canSave"
            class="rounded-sm border border-accent bg-surface-2 px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-accent disabled:opacity-50"
            data-testid="editor-save"
            @click="save"
          >
            Save
          </button>
        </div>
      </template>
    </CanvasHead>

    <div class="px-3 py-3 space-y-3">
      <div
        v-if="error"
        class="rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        {{ error }}
      </div>
      <div
        v-if="saved"
        class="rounded-md border border-signal-ok bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-ok"
        data-testid="editor-saved"
      >
        Graph saved.
      </div>

      <div
        v-if="isMaterialized"
        class="rounded-md border border-border-muted bg-surface-1 px-3 py-2 font-ui text-[12px] text-ink-muted"
        data-testid="editor-materialized-banner"
      >
        Materialized run — read-only. This graph was projected from the run's
        event log: one node per action taken, the agent loop unrolled per
        iteration, and each tool call its own node. Tool arguments are
        summarised and errors are classified — neither is reproduced. Open the
        run's trace for the full detail.
      </div>
      <div
        v-else-if="readOnly"
        class="rounded-md border border-border-muted bg-surface-1 px-3 py-2 font-ui text-[12px] text-ink-muted"
        data-testid="editor-readonly-banner"
      >
        Library graph — read-only. Copy + change the id to save your own.
      </div>
      <div
        v-if="isDegraded"
        class="rounded-md border border-signal-warn bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-warn"
        data-testid="editor-materialized-degraded"
        role="status"
      >
        Degraded projection — the resolved spec this run executed was no longer
        available, so it was projected against the library graph instead.
        Per-run dial overrides and any gate rewrite are unrecoverable: the
        topology shown may differ from the one that ran.
      </div>

      <div
        class="grid gap-3"
        style="grid-template-columns: 240px minmax(0, 1.1fr) minmax(340px, 0.9fr) 360px;"
        data-testid="editor-three-pane"
      >
        <NodePalette
          data-testid="editor-palette"
          @kind-drag-start="(p) => p"
          @kind-select="onPaletteSelect"
        />

        <div class="flex min-w-0 flex-col gap-2">
          <div
            class="flex items-center gap-2 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-muted"
          >
            <span>Canvas</span>
            <span
              v-if="canvasParseError"
              class="ml-auto normal-case tracking-normal text-signal-warn"
              data-testid="canvas-stale"
              >Showing last valid parse</span
            >
          </div>
          <GraphCanvas
            ref="canvasRef"
            :adapter="canvasAdapter"
            :selected-node-id="selectedNodeId"
            @select-node="onCanvasSelect"
          />
        </div>

        <div class="flex flex-col gap-2 min-w-0">
          <div class="flex items-center gap-2">
            <label
              class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim"
              for="editor-node-select"
              >Selected node</label
            >
            <select
              id="editor-node-select"
              v-model="selectedNodeId"
              class="rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[12px] text-ink"
              data-testid="editor-node-select"
            >
              <option value="">—</option>
              <option v-for="n in parsedNodes" :key="n.id" :value="n.id">
                {{ n.id }} ({{ n.kind }})
              </option>
            </select>
            <span
              v-if="manifestLoading"
              class="font-ui text-[11px] text-ink-dim"
              >Loading…</span
            >
          </div>
          <textarea
            v-model="yaml"
            :readonly="readOnly"
            spellcheck="false"
            aria-label="Graph YAML definition"
            class="h-[480px] w-full rounded-md border border-border-muted bg-surface-0 px-3 py-2 font-mono text-[12px] text-ink"
            data-testid="editor-yaml"
          />

          <div
            v-if="validation"
            class="rounded-md border border-border-muted bg-surface-1 px-3 py-2"
            data-testid="editor-validation"
          >
            <div
              v-if="validation.ok"
              class="font-ui text-[12px] text-signal-ok"
              data-testid="editor-validation-ok"
            >
              Validation passed.
            </div>
            <ul v-else class="space-y-1 font-ui text-[12px]">
              <li
                v-for="(issue, idx) in validation.issues"
                :key="idx"
                class="text-signal-danger"
                :data-testid="`editor-validation-issue-${idx}`"
              >
                <span
                  class="inline-block rounded-sm border border-signal-danger px-1 mr-1 text-[10px] uppercase tracking-[0.18em]"
                >
                  {{ issue.rule }}
                </span>
                {{ issue.message }}
              </li>
            </ul>
          </div>
        </div>

        <NodeAttributeEditor
          data-testid="editor-attr-editor"
          :node-id="selectedNodeId"
          :kind-id="selectedKindId"
          :attrs="selectedNode?.attrs ?? {}"
          :manifest="selectedManifest"
          :graph-node-ids="graphNodeIds"
          :activity-options="[
            { id: 'plan', label: 'plan' },
            { id: 'decompose', label: 'decompose' },
            { id: 'retrieve', label: 'retrieve' },
            { id: 'reflect', label: 'reflect' },
            { id: 'review', label: 'review' },
            { id: 'summarize', label: 'summarize' },
            { id: 'validate', label: 'validate' },
            { id: 'ask', label: 'ask' },
          ]"
          @update:attrs="onAttrUpdate"
        />
      </div>
    </div>
  </div>
</template>
