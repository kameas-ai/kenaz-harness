<script setup lang="ts">
/**
 * FilenameChip — pill rendering an `@filename` mention parsed out of
 * an assistant or user markdown message.
 *
 * Behaviour (per markdown-rendering-polish-01KQ8TDT FR-007, Q2=A):
 *   - Click → best-effort `Tools.OpenInEditor` RPC. When the RPC is
 *     unavailable on the wails surface we fall back to copying the
 *     path to the clipboard and toasting "Path copied to clipboard".
 *   - Missing target (`fs.exists` returns false) → disabled chip with
 *     a `file not found` tooltip; click is a no-op.
 *   - File icon: lucide `File`.
 *
 * The chip is mounted by MarkdownBlock's post-mount walker into
 * placeholder <span data-md-mention="file" data-path="..."> elements;
 * it's never used directly in templates.
 */
import { onMounted, ref } from 'vue';
import { File as FileIcon } from 'lucide-vue-next';

const props = defineProps<{
  path: string;
}>();

// Resolution state — start optimistic; flip to disabled iff fs.exists
// returns a definitive false.
const exists = ref(true);
const checked = ref(false);

interface ToolsClient {
  openInEditor?: (path: string) => Promise<void>;
}

interface FsClient {
  exists?: (path: string) => Promise<boolean>;
}

interface ProbeClient {
  tools?: ToolsClient;
  fs?: FsClient;
}

async function loadClient(): Promise<ProbeClient | null> {
  try {
    const mod = await import('@/lib/harnessClient');
    return mod.createHarnessClient() as unknown as ProbeClient;
  } catch {
    return null;
  }
}

onMounted(async () => {
  const client = await loadClient();
  if (!client?.fs?.exists) {
    // No fs.exists surface — leave optimistic.
    return;
  }
  try {
    const ok = await client.fs.exists(props.path);
    exists.value = ok;
  } catch {
    // Errors stay optimistic — the click path will surface failure.
  } finally {
    checked.value = true;
  }
});

function emitToast(message: string) {
  document.dispatchEvent(
    new CustomEvent('md-toast', {
      bubbles: true,
      detail: { message },
    }),
  );
}

async function onClick() {
  if (!exists.value) return;
  const client = await loadClient();
  if (client?.tools?.openInEditor) {
    try {
      await client.tools.openInEditor(props.path);
      return;
    } catch {
      // fall through to clipboard
    }
  }
  try {
    await navigator.clipboard.writeText(props.path);
    emitToast('Path copied to clipboard');
  } catch {
    emitToast(`Open failed: ${props.path}`);
  }
}
</script>

<template>
  <button
    type="button"
    class="md-mention-chip"
    :class="{ 'md-mention-chip--disabled': !exists }"
    :disabled="!exists"
    :title="!exists ? 'file not found' : path"
    :aria-label="!exists ? `${path} (not found)` : `Open ${path}`"
    :data-testid="`filename-chip-${path}`"
    @click="onClick"
  >
    <FileIcon class="md-mention-chip-icon" aria-hidden="true" />
    <span class="md-mention-chip-label">{{ path }}</span>
  </button>
</template>

<style scoped>
.md-mention-chip {
  display: inline-flex;
  align-items: center;
  gap: 0.3em;
  padding: 0.05em 0.45em;
  margin: 0 0.1em;
  background: var(--surface-2);
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
  font-size: 0.85em;
  color: var(--ink);
  cursor: pointer;
  transition: background var(--motion-fast), color var(--motion-fast);
  vertical-align: baseline;
  line-height: 1.3;
}

.md-mention-chip:hover:not(.md-mention-chip--disabled) {
  background: var(--surface-3);
  color: var(--accent);
}

.md-mention-chip--disabled {
  opacity: 0.55;
  cursor: not-allowed;
  text-decoration: line-through;
}

.md-mention-chip-icon {
  width: 0.85em;
  height: 0.85em;
  flex: 0 0 auto;
}

.md-mention-chip-label {
  white-space: nowrap;
  max-width: 28ch;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
