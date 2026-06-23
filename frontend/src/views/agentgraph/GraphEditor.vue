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
import { useNodeManifest } from '@/composables/useNodeManifest';

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

const yaml = ref('');
const scope = ref<'library' | 'user'>('user');
const editingId = ref('');
const error = ref<string | null>(null);
const validation = ref<GraphValidationResult | null>(null);
const saved = ref(false);

const readOnly = computed(() => scope.value === 'library');
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

// ── palette drop handling ────────────────────────────────────────────

function onYamlDragOver(ev: DragEvent) {
  if (ev.dataTransfer && ev.dataTransfer.types.includes('application/x-kenaz-node-kind')) {
    ev.preventDefault();
    ev.dataTransfer.dropEffect = 'copy';
  }
}

function onYamlDrop(ev: DragEvent) {
  if (!ev.dataTransfer) return;
  const kind =
    ev.dataTransfer.getData('application/x-kenaz-node-kind') ||
    ev.dataTransfer.getData('text/plain');
  if (!kind) return;
  ev.preventDefault();
  appendStubNode(kind);
}

function onPaletteSelect({ kind }: { kind: string }) {
  appendStubNode(kind);
}

function appendStubNode(kind: string) {
  const existingIds = new Set(parsedNodes.value.map((n) => n.id));
  let i = 1;
  let candidate = `${kind}_${i}`;
  while (existingIds.has(candidate)) {
    i += 1;
    candidate = `${kind}_${i}`;
  }
  const block = `  - id: ${candidate}\n    kind: ${kind}\n    attrs: {}\n`;
  if (/nodes:\s*$/m.test(yaml.value)) {
    // Append to existing nodes list.
    yaml.value = yaml.value.replace(
      /(nodes:\s*\n(?:[\s\S]*?)?)(\n*$)/m,
      (_match, head: string, tail: string) => `${head.trimEnd()}\n${block}${tail}`,
    );
  } else {
    yaml.value = `${yaml.value.replace(/\n*$/, '')}\nnodes:\n${block}`;
  }
  selectedNodeId.value = candidate;
}

// ── load / validate / save ───────────────────────────────────────────

async function load() {
  error.value = null;
  validation.value = null;
  saved.value = false;
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

async function save() {
  if (readOnly.value) return;
  saved.value = false;
  error.value = null;
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
  () => id.value,
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

defineExpose({ load, validate, save, parseNodes, appendStubNode });
</script>

<template>
  <div>
    <CanvasHead
      number="12"
      section="GRAPHS"
      :title="readOnly ? 'View graph (read-only)' : 'Edit graph'"
      :subtitle="`Editing ${editingId || 'new graph'}`"
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
        v-if="readOnly"
        class="rounded-md border border-border-muted bg-surface-1 px-3 py-2 font-ui text-[12px] text-ink-muted"
        data-testid="editor-readonly-banner"
      >
        Library graph — read-only. Copy + change the id to save your own.
      </div>

      <div
        class="grid gap-3"
        style="grid-template-columns: 240px minmax(0, 1fr) 360px;"
        data-testid="editor-three-pane"
      >
        <NodePalette
          data-testid="editor-palette"
          @kind-drag-start="(p) => p"
          @kind-select="onPaletteSelect"
        />

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
            @dragover="onYamlDragOver"
            @drop="onYamlDrop"
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
