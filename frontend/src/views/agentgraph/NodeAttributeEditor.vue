<script setup lang="ts">
/**
 * NodeAttributeEditor — manifest-driven attribute form for one node.
 *
 * Renders one form field per `manifest.attrs` entry, choosing the input
 * widget by `AttrSpec.type` (FR-026). Inherited attrs (provenance layer
 * starts with `archetype`) appear in a section above the kind-specific
 * attrs (FR-028), separated by a divider showing the contributing
 * archetype.
 *
 * Provenance chips (FR-024) sit beside each field's effective default
 * and label `shipped` / `archetype:<id>` / `user-override`.
 *
 * Emits `update:attrs` on every change so the parent can re-serialize
 * back into the YAML pane.
 */
import { computed, ref, watch } from 'vue';
import type { NodeAttrSpec, NodeManifestDetail } from '@/lib/types';

interface Props {
  /** The unique node id in the graph (for ref pickers / labelling). */
  nodeId: string;
  /** The kind id (e.g. `planner`, `model`); empty when no node is selected. */
  kindId: string;
  /** Current attr values; the editor mutates a local copy and emits up. */
  attrs: Record<string, unknown>;
  /** Resolved manifest with provenance. Null when still loading or empty kind. */
  manifest: NodeManifestDetail | null;
  /** Other node ids in the same graph (for node_id_ref / port_ref pickers). */
  graphNodeIds?: string[];
  /**
   * Optional reference data so model_ref / tool_ref / activity_ref
   * pickers list real values. Tests pass minimal stubs; production
   * GraphEditor wires these from useHarnessAPI when available.
   */
  modelOptions?: Array<{ id: string; label: string }>;
  toolOptions?: Array<{ id: string; label: string }>;
  activityOptions?: Array<{ id: string; label: string }>;
  corpusOptions?: Array<{ id: string; label: string }>;
  attachmentOptions?: Array<{ id: string; label: string }>;
}

const props = withDefaults(defineProps<Props>(), {
  graphNodeIds: () => [],
  modelOptions: () => [],
  toolOptions: () => [],
  activityOptions: () => [],
  corpusOptions: () => [],
  attachmentOptions: () => [],
});

const emit = defineEmits<{
  (e: 'update:attrs', next: Record<string, unknown>): void;
}>();

// Local working copy. We mirror props.attrs into `local` so the form
// can hold transient strings (e.g. partly-typed JSON) without polluting
// the canonical model. Whenever a field is committed we emit upward.
const local = ref<Record<string, unknown>>({ ...(props.attrs ?? {}) });
const jsonText = ref<Record<string, string>>({});
const jsonErrors = ref<Record<string, string>>({});

watch(
  () => props.attrs,
  (next) => {
    local.value = { ...(next ?? {}) };
    // Reset JSON shadow state for object/array attrs when the parent
    // round-trips a fresh value.
    jsonText.value = {};
    jsonErrors.value = {};
  },
  { deep: true },
);

// ── provenance helpers ──────────────────────────────────────────────

const provenanceMap = computed<Map<string, string>>(() => {
  const m = new Map<string, string>();
  if (!props.manifest) return m;
  for (const p of props.manifest.provenance ?? []) {
    m.set(p.fieldPath, p.layer);
  }
  return m;
});

function provenanceFor(name: string): string {
  // Try the most-specific path first.
  const pm = provenanceMap.value;
  return (
    pm.get(`attrs.${name}`) ??
    pm.get(`attrs.${name}.default`) ??
    pm.get(`defaults.${name}`) ??
    pm.get(name) ??
    'shipped'
  );
}

interface AttrPartition {
  archetypeLayer: string; // e.g. "archetype:compute" or ""
  attrs: NodeAttrSpec[];
}

/**
 * Partition attrs into "inherited from archetype X" buckets followed
 * by a final bucket for attrs whose provenance is `shipped` (the kind
 * itself) or `user-override`. Buckets are emitted in chain order so
 * the UI reads top-down: most-abstract archetype first, leaf kind last.
 */
const partitioned = computed<AttrPartition[]>(() => {
  if (!props.manifest) return [];
  const attrs = props.manifest.attrs ?? [];
  const buckets = new Map<string, NodeAttrSpec[]>();

  for (const attr of attrs) {
    const layer = provenanceFor(attr.name);
    // Inherited archetype layers use the wire prefix `archetype-<id>`
    // (see `core/rpc/views/nodes/api.go`'s LayerArchetypePrefix).
    // Anything else (`shipped`, `user-override`) is the leaf bucket.
    const key = layer.startsWith('archetype-') ? layer : '__leaf__';
    const arr = buckets.get(key) ?? [];
    arr.push(attr);
    buckets.set(key, arr);
  }

  const out: AttrPartition[] = [];
  // Walk the inheritance chain in root→leaf order so the most-abstract
  // archetype's attrs render first (FR-028). The wire chain is already
  // root-first (see resolver.resolveOne).
  const chain = props.manifest.chain ?? [];
  for (const ancestor of chain) {
    const key = `archetype-${ancestor}`;
    if (buckets.has(key)) {
      out.push({ archetypeLayer: key, attrs: buckets.get(key) ?? [] });
      buckets.delete(key);
    }
  }
  // Any archetype layer the resolver labelled but that isn't in the
  // chain — emit anyway, in declaration order.
  for (const [key, list] of buckets.entries()) {
    if (key === '__leaf__') continue;
    out.push({ archetypeLayer: key, attrs: list });
  }
  // Leaf bucket last.
  const leaf = buckets.get('__leaf__');
  if (leaf && leaf.length > 0) {
    out.push({ archetypeLayer: '', attrs: leaf });
  }
  return out;
});

// ── widget helpers ──────────────────────────────────────────────────

function effectiveDefault(attr: NodeAttrSpec): unknown {
  if (attr.default !== undefined) return attr.default;
  const d = props.manifest?.defaults?.[attr.name];
  return d;
}

function valueOf(attr: NodeAttrSpec): unknown {
  if (attr.name in local.value) return local.value[attr.name];
  return effectiveDefault(attr);
}

function setValue(name: string, v: unknown) {
  local.value = { ...local.value, [name]: v };
  emit('update:attrs', { ...local.value });
}

function unsetValue(name: string) {
  const next = { ...local.value };
  delete next[name];
  local.value = next;
  emit('update:attrs', next);
}

function isRequiredMissing(attr: NodeAttrSpec): boolean {
  if (!attr.required) return false;
  const v = valueOf(attr);
  if (v === undefined || v === null) return true;
  if (typeof v === 'string' && v.trim() === '') return true;
  if (Array.isArray(v) && v.length === 0) return true;
  return false;
}

function onString(attr: NodeAttrSpec, ev: Event) {
  const target = ev.target as HTMLInputElement | HTMLTextAreaElement;
  setValue(attr.name, target.value);
}

function onBool(attr: NodeAttrSpec, ev: Event) {
  const target = ev.target as HTMLInputElement;
  setValue(attr.name, target.checked);
}

function onNumber(attr: NodeAttrSpec, ev: Event) {
  const target = ev.target as HTMLInputElement;
  const raw = target.value;
  if (raw === '') {
    unsetValue(attr.name);
    return;
  }
  const parsed = attr.type === 'int' ? parseInt(raw, 10) : parseFloat(raw);
  if (Number.isNaN(parsed)) return;
  // Clamp to bounds if specified.
  let clamped = parsed;
  if (typeof attr.min === 'number' && clamped < attr.min) clamped = attr.min;
  if (typeof attr.max === 'number' && clamped > attr.max) clamped = attr.max;
  setValue(attr.name, clamped);
}

function onEnum(attr: NodeAttrSpec, ev: Event) {
  const target = ev.target as HTMLSelectElement;
  setValue(attr.name, target.value);
}

function onSelectRef(attr: NodeAttrSpec, ev: Event) {
  const target = ev.target as HTMLSelectElement;
  setValue(attr.name, target.value);
}

function jsonDisplayValue(attr: NodeAttrSpec): string {
  if (attr.name in jsonText.value) return jsonText.value[attr.name];
  const raw = valueOf(attr);
  if (raw === undefined) return '';
  try {
    return JSON.stringify(raw, null, 2);
  } catch {
    return '';
  }
}

function onJsonInput(attr: NodeAttrSpec, ev: Event) {
  const target = ev.target as HTMLTextAreaElement;
  jsonText.value = { ...jsonText.value, [attr.name]: target.value };
}

function commitJson(attr: NodeAttrSpec) {
  const raw = jsonText.value[attr.name];
  if (raw === undefined) return;
  if (raw.trim() === '') {
    unsetValue(attr.name);
    const errs = { ...jsonErrors.value };
    delete errs[attr.name];
    jsonErrors.value = errs;
    return;
  }
  try {
    const parsed = JSON.parse(raw);
    setValue(attr.name, parsed);
    const errs = { ...jsonErrors.value };
    delete errs[attr.name];
    jsonErrors.value = errs;
    // Re-format with stable indent.
    jsonText.value = {
      ...jsonText.value,
      [attr.name]: JSON.stringify(parsed, null, 2),
    };
  } catch (err) {
    jsonErrors.value = {
      ...jsonErrors.value,
      [attr.name]: err instanceof Error ? err.message : String(err),
    };
  }
}

function formatJson(attr: NodeAttrSpec) {
  const raw = jsonText.value[attr.name] ?? jsonDisplayValue(attr);
  try {
    const parsed = JSON.parse(raw);
    jsonText.value = {
      ...jsonText.value,
      [attr.name]: JSON.stringify(parsed, null, 2),
    };
    setValue(attr.name, parsed);
    const errs = { ...jsonErrors.value };
    delete errs[attr.name];
    jsonErrors.value = errs;
  } catch (err) {
    jsonErrors.value = {
      ...jsonErrors.value,
      [attr.name]: err instanceof Error ? err.message : String(err),
    };
  }
}

function provenanceLabel(layer: string): string {
  if (!layer) return 'shipped';
  if (layer === 'shipped') return 'shipped';
  if (layer === 'user-override') return 'user override';
  if (layer.startsWith('archetype-'))
    return `from ${layer.slice('archetype-'.length)}`;
  return layer;
}

function provenanceClass(layer: string): string {
  if (layer === 'user-override')
    return 'border-signal-warn text-signal-warn';
  if (layer.startsWith('archetype-'))
    return 'border-accent text-accent';
  return 'border-border-muted text-ink-muted';
}

function archetypeChainLabel(layer: string): string {
  if (layer.startsWith('archetype-')) return layer.slice('archetype-'.length);
  return layer;
}

function refOptionsFor(
  type: string,
): Array<{ id: string; label: string }> | null {
  switch (type) {
    case 'model_ref':
      return props.modelOptions;
    case 'tool_ref':
      return props.toolOptions;
    case 'activity_ref':
      return props.activityOptions;
    case 'corpus_ref':
      return props.corpusOptions;
    case 'attachment_ref':
      return props.attachmentOptions;
    case 'node_id_ref':
    case 'port_ref':
    case 'messages_ref':
      return props.graphNodeIds.map((id) => ({ id, label: id }));
    default:
      return null;
  }
}

function isJsonType(type: string): boolean {
  return type === 'object' || type === 'map' || type === 'array';
}

function isRefType(type: string): boolean {
  return (
    type === 'model_ref' ||
    type === 'tool_ref' ||
    type === 'activity_ref' ||
    type === 'corpus_ref' ||
    type === 'attachment_ref' ||
    type === 'node_id_ref' ||
    type === 'port_ref' ||
    type === 'messages_ref'
  );
}

defineExpose({ local, partitioned });
</script>

<template>
  <div
    class="flex h-full flex-col gap-2 bg-surface-1 px-3 py-3"
    data-testid="node-attr-editor"
  >
    <div
      v-if="!kindId"
      class="rounded-md border border-border-muted bg-surface-1 px-3 py-2 font-ui text-[12px] text-ink-dim"
      data-testid="node-attr-empty"
    >
      Select a node to edit.
    </div>

    <template v-else-if="!manifest">
      <div
        class="rounded-md border border-border-muted bg-surface-1 px-3 py-2 font-ui text-[12px] text-ink-dim"
        data-testid="node-attr-loading"
      >
        Loading manifest for <code class="text-ink">{{ kindId }}</code>…
      </div>
    </template>

    <template v-else>
      <header class="border-b border-border-muted pb-2">
        <div
          class="font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim"
        >
          Node {{ nodeId || '—' }}
        </div>
        <div class="font-mono text-[14px] text-ink">
          {{ manifest.summary.displayName || manifest.summary.id }}
        </div>
        <div class="font-ui text-[11px] text-ink-dim">
          kind: <code>{{ kindId }}</code>
          <span v-if="manifest.summary.description"
            > — {{ manifest.summary.description }}</span
          >
        </div>
      </header>

      <div
        v-if="partitioned.length === 0"
        class="px-2 py-2 font-ui text-[11px] text-ink-dim"
        data-testid="node-attr-no-fields"
      >
        This kind declares no editable attributes.
      </div>

      <section
        v-for="(part, partIdx) in partitioned"
        :key="part.archetypeLayer || '__leaf__'"
        :data-testid="
          part.archetypeLayer
            ? `attr-section-${archetypeChainLabel(part.archetypeLayer)}`
            : 'attr-section-leaf'
        "
        class="space-y-2"
      >
        <div
          v-if="part.archetypeLayer"
          class="flex items-center gap-2 border-b border-dashed border-border-muted pb-1"
          :data-testid="`attr-divider-${archetypeChainLabel(part.archetypeLayer)}`"
        >
          <span
            class="rounded-sm border border-accent px-1 py-0.5 font-ui text-[9px] uppercase tracking-[0.18em] text-accent"
            >inherited</span
          >
          <span class="font-ui text-[11px] text-ink-dim">
            from archetype
            <code class="text-ink">{{
              archetypeChainLabel(part.archetypeLayer)
            }}</code>
          </span>
        </div>
        <div
          v-else-if="partIdx > 0"
          class="flex items-center gap-2 border-b border-dashed border-border-muted pb-1"
          data-testid="attr-divider-leaf"
        >
          <span
            class="rounded-sm border border-border-muted px-1 py-0.5 font-ui text-[9px] uppercase tracking-[0.18em] text-ink-dim"
            >this kind</span
          >
        </div>

        <div
          v-for="attr in part.attrs"
          :key="attr.name"
          class="space-y-1"
          :data-testid="`attr-row-${attr.name}`"
        >
          <label
            class="flex items-center gap-2 font-ui text-[12px] text-ink"
            :for="`attr-${attr.name}`"
          >
            <span>
              {{ attr.name
              }}<span
                v-if="attr.required"
                class="text-signal-danger"
                aria-label="required"
                >*</span
              >
            </span>
            <span
              class="rounded-sm border px-1 font-ui text-[9px] uppercase tracking-[0.18em]"
              :class="provenanceClass(provenanceFor(attr.name))"
              :data-testid="`attr-provenance-${attr.name}`"
              :title="`Provenance layer: ${provenanceFor(attr.name)}`"
            >
              {{ provenanceLabel(provenanceFor(attr.name)) }}
            </span>
            <span class="ml-auto font-mono text-[10px] text-ink-dim">{{
              attr.type
            }}</span>
          </label>

          <p
            v-if="attr.description"
            class="font-ui text-[11px] text-ink-dim"
          >
            {{ attr.description }}
          </p>

          <!-- string -->
          <input
            v-if="attr.type === 'string'"
            :id="`attr-${attr.name}`"
            type="text"
            :value="(valueOf(attr) as string | undefined) ?? ''"
            :minlength="attr.minLength"
            :maxlength="attr.maxLength"
            :required="attr.required"
            class="w-full rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[12px] text-ink"
            :data-testid="`attr-input-${attr.name}`"
            @input="onString(attr, $event)"
          />

          <!-- int / float -->
          <input
            v-else-if="attr.type === 'int' || attr.type === 'float'"
            :id="`attr-${attr.name}`"
            type="number"
            :value="(valueOf(attr) as number | undefined) ?? ''"
            :min="attr.min"
            :max="attr.max"
            :step="attr.type === 'int' ? 1 : 'any'"
            :required="attr.required"
            class="w-full rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[12px] text-ink"
            :data-testid="`attr-input-${attr.name}`"
            @input="onNumber(attr, $event)"
          />

          <!-- bool -->
          <input
            v-else-if="attr.type === 'bool'"
            :id="`attr-${attr.name}`"
            type="checkbox"
            :checked="!!valueOf(attr)"
            class="h-4 w-4 rounded border-border-muted bg-surface-0"
            :data-testid="`attr-input-${attr.name}`"
            @change="onBool(attr, $event)"
          />

          <!-- enum -->
          <select
            v-else-if="attr.type === 'enum'"
            :id="`attr-${attr.name}`"
            :value="(valueOf(attr) as string | undefined) ?? ''"
            class="w-full rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[12px] text-ink"
            :data-testid="`attr-input-${attr.name}`"
            @change="onEnum(attr, $event)"
          >
            <option v-if="!attr.required" value="">—</option>
            <option v-for="opt in attr.enum ?? []" :key="opt" :value="opt">
              {{ opt }}
            </option>
          </select>

          <!-- ref pickers -->
          <select
            v-else-if="isRefType(attr.type)"
            :id="`attr-${attr.name}`"
            :value="(valueOf(attr) as string | undefined) ?? ''"
            class="w-full rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[12px] text-ink"
            :data-testid="`attr-input-${attr.name}`"
            @change="onSelectRef(attr, $event)"
          >
            <option value="">—</option>
            <option
              v-for="opt in refOptionsFor(attr.type) ?? []"
              :key="opt.id"
              :value="opt.id"
            >
              {{ opt.label }}
            </option>
          </select>

          <!-- json fallback -->
          <div v-else-if="isJsonType(attr.type)" class="space-y-1">
            <textarea
              :id="`attr-${attr.name}`"
              rows="4"
              :value="jsonDisplayValue(attr)"
              class="w-full rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-mono text-[11px] text-ink"
              spellcheck="false"
              :data-testid="`attr-input-${attr.name}`"
              @input="onJsonInput(attr, $event)"
              @blur="commitJson(attr)"
            />
            <div class="flex items-center justify-between">
              <button
                type="button"
                class="rounded-sm border border-border-muted px-1.5 py-0.5 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
                :data-testid="`attr-format-${attr.name}`"
                @click="formatJson(attr)"
              >
                Format JSON
              </button>
              <span
                v-if="jsonErrors[attr.name]"
                class="font-ui text-[11px] text-signal-danger"
                :data-testid="`attr-json-error-${attr.name}`"
              >
                {{ jsonErrors[attr.name] }}
              </span>
            </div>
          </div>

          <!-- catch-all: render as string fallback -->
          <input
            v-else
            :id="`attr-${attr.name}`"
            type="text"
            :value="(valueOf(attr) as string | undefined) ?? ''"
            class="w-full rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[12px] text-ink"
            :data-testid="`attr-input-${attr.name}`"
            @input="onString(attr, $event)"
          />

          <p
            v-if="isRequiredMissing(attr)"
            class="font-ui text-[11px] text-signal-danger"
            :data-testid="`attr-required-${attr.name}`"
          >
            {{ attr.name }} is required.
          </p>
        </div>
      </section>
    </template>
  </div>
</template>
