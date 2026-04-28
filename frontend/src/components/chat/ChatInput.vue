<script setup lang="ts">
/**
 * ChatInput — multiline composition surface (multimodal-io WP04).
 *
 *   - Enter sends, Shift+Enter inserts a newline.
 *   - Disabled while a stream is in-flight (`streaming` prop).
 *   - Cancel button replaces Send while streaming; clicking emits
 *     `cancel` so the parent can call `client.llm.stopStream(subId)`.
 *   - Paperclip button + drag-drop accept image/PDF/text snapshots.
 *     Image+PDF round-trip through Attachments_AddMedia (CAS path);
 *     text snapshots ride the legacy Attachments_Add path.
 *   - @filepath shortcut: typing `@<path>` then Tab/Enter resolves
 *     via Shell_PathComplete + Shell_ReadFile, then attaches the
 *     resolved file. Deny-list errors surface in the banner.
 *
 * Sending pipeline (multimodal):
 *   1. Build `contentBlocks` array — one image/document block per
 *      pending multimodal attachment, then one text block at the end.
 *   2. emit('sendBlocks', contentBlocks, fallbackText) so the parent
 *      can call sessions.sendMessageWithBlocks then llm.startStream.
 *   3. emit('send', text) for backward-compat callers that don't
 *      know about blocks.
 *
 * Privacy CI invariant #4: every colour flows through tokens. Privacy
 * #3: image previews use URL.createObjectURL, revoked on remove + send.
 */

import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { Paperclip, X, FileText } from 'lucide-vue-next';
import { useDropZone } from '@vueuse/core';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type {
  ContentBlock,
  CostEstimate,
  SlashCommandInfo,
} from '@/lib/types';
import SlashAutocomplete from '@/components/chat/SlashAutocomplete.vue';

const props = defineProps<{
  modelValue?: string;
  streaming?: boolean;
  estimate?: CostEstimate | null;
  placeholder?: string;
  /** Optional disabled override (e.g. no-provider state). */
  disabled?: boolean;
  /**
   * Session id used as the scope for staged attachments. The chat
   * surface always passes its active session id; tests may pass a
   * fixed string.
   */
  sessionId?: string;
  /**
   * Optional async error banner override — when the parent receives a
   * server error (e.g. UnsupportedModalityError) it can pass the
   * `friendly()` text in via this prop and ChatInput renders it inline
   * without erasing the typed text or staged attachments.
   */
  errorBanner?: string | null;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void;
  (e: 'send', value: string): void;
  /**
   * Multimodal send. The parent calls
   *   sessions.sendMessageWithBlocks(sessionId, contentBlocks)
   *   then llm.startStream(sessionId, ...)
   * The text-only `send` event is still emitted for callers that
   * don't yet handle blocks; both fire on the same submit.
   */
  (e: 'sendBlocks', contentBlocks: ContentBlock[]): void;
  (e: 'cancel'): void;
  /**
   * Slash-command submit. Fires INSTEAD of `send` / `sendBlocks`
   * when the trimmed input begins with `/`. The parent dispatches
   * via `client.slash.execute(sessionId, raw)` and renders the
   * result inline.
   */
  (e: 'slashCommand', raw: string): void;
}>();

const client = useHarnessClient();

const internal = ref(props.modelValue ?? '');
watch(
  () => props.modelValue,
  (v) => {
    if (typeof v === 'string' && v !== internal.value) internal.value = v;
  },
);

const isDisabled = computed(() => props.disabled === true || props.streaming === true);
const trimmed = computed(() => internal.value.trim());

// ── pending attachments (staged before send) ──────────────────────────

interface PendingMedia {
  kind: 'image' | 'document';
  attachmentId: string;
  mediaType: string;
  filename: string;
  base64: string;
  /** Object URL for thumbnail preview; revoked on remove + send. */
  previewUrl: string;
}

interface PendingText {
  kind: 'text';
  attachmentId: string;
  filename: string;
  size: number;
}

type Pending = PendingMedia | PendingText;

const pending = ref<Pending[]>([]);
const localError = ref<string | null>(null);
const objectUrls: string[] = [];

function revokeAllObjectUrls() {
  for (const url of objectUrls.splice(0)) {
    try {
      URL.revokeObjectURL(url);
    } catch {
      /* ignore */
    }
  }
}

onBeforeUnmount(() => {
  revokeAllObjectUrls();
});

const canSend = computed(() => {
  if (isDisabled.value) return false;
  return trimmed.value.length > 0 || pending.value.length > 0;
});

const tokenLabel = computed(() => {
  const n = props.estimate?.tokens ?? 0;
  return `${n.toLocaleString()} tok`;
});
const costLabel = computed(() => {
  const usd = props.estimate?.usd ?? 0;
  return `$${usd.toFixed(4)}`;
});

// Per-file size caps (FR-011: 20 MiB image/PDF, 1 MiB text).
const IMAGE_PDF_CAP = 20 * 1024 * 1024;
const TEXT_CAP = 1 * 1024 * 1024;
// Hard total cap across all staged attachments (FR-011: 30 MiB).
const TOTAL_CAP = 30 * 1024 * 1024;

const ALLOWED_IMAGE_MIMES = new Set([
  'image/png',
  'image/jpeg',
  'image/gif',
  'image/webp',
]);

function approxSize(p: Pending): number {
  if (p.kind === 'text') return p.size;
  // base64-decoded byte size ≈ (len * 3 / 4)
  return Math.floor((p.base64.length * 3) / 4);
}

function totalStagedBytes(): number {
  let total = 0;
  for (const p of pending.value) total += approxSize(p);
  return total;
}

function inferMimeFromExtension(filename: string, browserType: string): string {
  if (browserType) return browserType;
  const ext = filename.toLowerCase().split('.').pop() ?? '';
  switch (ext) {
    case 'png':
      return 'image/png';
    case 'jpg':
    case 'jpeg':
      return 'image/jpeg';
    case 'gif':
      return 'image/gif';
    case 'webp':
      return 'image/webp';
    case 'pdf':
      return 'application/pdf';
    case 'json':
      return 'application/json';
    case 'md':
      return 'text/markdown';
    case 'txt':
      return 'text/plain';
    case 'go':
    case 'py':
    case 'js':
    case 'ts':
    case 'vue':
    case 'sh':
    case 'yaml':
    case 'yml':
    case 'toml':
      return 'text/plain';
    default:
      return '';
  }
}

async function readFileAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = String(reader.result ?? '');
      // Strip the `data:<mt>;base64,` prefix Wails Attachments_AddMedia
      // does NOT want — it expects the raw base64 only.
      const idx = result.indexOf(',');
      resolve(idx >= 0 ? result.slice(idx + 1) : result);
    };
    reader.onerror = () => reject(reader.error ?? new Error('FileReader error'));
    reader.readAsDataURL(file);
  });
}

async function readFileAsText(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ''));
    reader.onerror = () =>
      reject(reader.error ?? new Error('FileReader error'));
    reader.readAsText(file);
  });
}

function sha256Hex(input: string): Promise<string> {
  // SubtleCrypto isn't available in happy-dom test env consistently;
  // fall back to a length-prefixed pseudo-hash so the inline:
  // contentSource stays unique-ish without breaking tests.
  if (
    typeof crypto !== 'undefined' &&
    crypto.subtle &&
    typeof crypto.subtle.digest === 'function'
  ) {
    const enc = new TextEncoder().encode(input);
    return crypto.subtle.digest('SHA-256', enc).then((buf) => {
      const arr = Array.from(new Uint8Array(buf));
      return arr.map((b) => b.toString(16).padStart(2, '0')).join('');
    });
  }
  // Fallback: not cryptographically meaningful, just stable.
  let hash = 0;
  for (let i = 0; i < input.length; i++) {
    hash = ((hash << 5) - hash + input.charCodeAt(i)) | 0;
  }
  return Promise.resolve(
    `fallback-${Math.abs(hash).toString(16)}-${input.length}`,
  );
}

async function ingestImageOrPDF(
  file: File,
  mediaType: string,
): Promise<void> {
  if (file.size > IMAGE_PDF_CAP) {
    localError.value = `${file.name}: file too large (max 20 MiB)`;
    return;
  }
  const base64 = await readFileAsBase64(file);
  if (totalStagedBytes() + file.size > TOTAL_CAP) {
    localError.value = 'Total staged attachments exceed 30 MiB; remove one first.';
    return;
  }
  const sid = props.sessionId ?? '';
  const att = await client.attachments.addMedia(
    'session',
    sid,
    base64,
    mediaType,
    file.name,
  );
  const previewUrl = URL.createObjectURL(file);
  objectUrls.push(previewUrl);
  const kind: 'image' | 'document' = mediaType.startsWith('image/')
    ? 'image'
    : 'document';
  pending.value = [
    ...pending.value,
    {
      kind,
      attachmentId: att.id,
      mediaType,
      filename: file.name,
      base64,
      previewUrl,
    },
  ];
}

async function ingestText(file: File, _mediaType: string): Promise<void> {
  if (file.size > TEXT_CAP) {
    localError.value = `${file.name}: file too large; library is for reference snippets (max 1 MiB).`;
    return;
  }
  let content: string;
  try {
    content = await readFileAsText(file);
  } catch {
    localError.value =
      'Binary files are only supported as images or PDFs.';
    return;
  }
  // Reject binary-ish payloads: if it contains a NUL byte, treat as
  // binary (browser readAsText on a binary file otherwise silently
  // produces garbage UTF-16).
  if (content.indexOf('\0') >= 0) {
    localError.value =
      'Binary files are only supported as images or PDFs.';
    return;
  }
  if (totalStagedBytes() + content.length > TOTAL_CAP) {
    localError.value = 'Total staged attachments exceed 30 MiB; remove one first.';
    return;
  }
  const sha = await sha256Hex(content);
  const sid = props.sessionId ?? '';
  const att = await client.attachments.add({
    scopeKind: 'session',
    scopeId: sid,
    contentSource: `inline:${sha}`,
    content,
    kind: 'system',
  });
  pending.value = [
    ...pending.value,
    {
      kind: 'text',
      attachmentId: att.id,
      filename: file.name,
      size: content.length,
    },
  ];
}

async function onFileChosen(file: File): Promise<void> {
  localError.value = null;
  const mediaType = inferMimeFromExtension(file.name, file.type);
  if (ALLOWED_IMAGE_MIMES.has(mediaType)) {
    try {
      await ingestImageOrPDF(file, mediaType);
    } catch (e) {
      localError.value = e instanceof Error ? e.message : String(e);
    }
    return;
  }
  if (mediaType === 'application/pdf') {
    try {
      await ingestImageOrPDF(file, mediaType);
    } catch (e) {
      localError.value = e instanceof Error ? e.message : String(e);
    }
    return;
  }
  // Empty mediaType after extension lookup means the browser
  // didn't classify it AND we don't recognise the extension — bail
  // before attempting readAsText (which would silently produce
  // garbage on binary files).
  if (!mediaType) {
    localError.value = 'Binary files are only supported as images or PDFs.';
    return;
  }
  try {
    await ingestText(file, mediaType);
  } catch (e) {
    localError.value = e instanceof Error ? e.message : String(e);
  }
}

async function removePending(idx: number): Promise<void> {
  const item = pending.value[idx];
  if (!item) return;
  if ('previewUrl' in item) {
    try {
      URL.revokeObjectURL(item.previewUrl);
    } catch {
      /* ignore */
    }
    const i = objectUrls.indexOf(item.previewUrl);
    if (i >= 0) objectUrls.splice(i, 1);
  }
  pending.value = pending.value.filter((_, i) => i !== idx);
  // Best-effort backend cleanup; ignore failures (the row will be
  // dropped on session delete anyway).
  try {
    await client.attachments.remove(item.attachmentId);
  } catch {
    /* ignore */
  }
}

// ── paperclip <input type=file> ───────────────────────────────────────

const fileInput = ref<HTMLInputElement | null>(null);
function openFileDialog() {
  fileInput.value?.click();
}
async function onFileInputChange(ev: Event) {
  const t = ev.target as HTMLInputElement;
  const list = t.files;
  if (!list) return;
  for (const file of Array.from(list)) {
    await onFileChosen(file);
  }
  // Reset so the same file can be re-picked.
  t.value = '';
}

// ── drag-drop ─────────────────────────────────────────────────────────

const dropZoneRef = ref<HTMLElement | null>(null);
const isOverDrop = ref(false);
useDropZone(dropZoneRef, {
  onDrop: async (files) => {
    isOverDrop.value = false;
    if (!files) return;
    for (const f of files) {
      await onFileChosen(f);
    }
  },
  onEnter: () => {
    isOverDrop.value = true;
  },
  onLeave: () => {
    isOverDrop.value = false;
  },
});

// ── @filepath autocomplete ────────────────────────────────────────────

interface AtSuggestion {
  /** Token offset where the @<path> begins in the textarea. */
  start: number;
  /** Current candidates (capped to 32). */
  options: string[];
  /** Active-cursor index into options. */
  activeIndex: number;
}

const atState = ref<AtSuggestion | null>(null);

function computeAtTokenAtCursor(): { start: number; token: string } | null {
  const ta = textareaRef.value;
  if (!ta) return null;
  const value = internal.value;
  const cursor = ta.selectionStart ?? value.length;
  // Walk backwards to find an `@` that's at the start-of-input or
  // preceded by whitespace.
  let i = cursor - 1;
  while (i >= 0) {
    const ch = value.charAt(i);
    if (ch === '@') {
      if (i === 0 || /\s/.test(value.charAt(i - 1))) {
        return { start: i, token: value.slice(i + 1, cursor) };
      }
      return null;
    }
    if (/\s/.test(ch)) return null;
    i--;
  }
  return null;
}

let atDebounce: ReturnType<typeof setTimeout> | null = null;
async function refreshAtSuggestions() {
  const at = computeAtTokenAtCursor();
  if (!at) {
    atState.value = null;
    return;
  }
  if (atDebounce) clearTimeout(atDebounce);
  atDebounce = setTimeout(async () => {
    try {
      const opts = await client.shell.pathComplete(at.token);
      atState.value = {
        start: at.start,
        options: opts,
        activeIndex: 0,
      };
    } catch {
      atState.value = null;
    }
  }, 80);
}

async function commitAtPath(path: string): Promise<void> {
  // Replace the @<token> in the textarea with empty string (the
  // resolved file becomes a chip; the token is consumed).
  const at = computeAtTokenAtCursor();
  if (at) {
    const before = internal.value.slice(0, at.start);
    const ta = textareaRef.value;
    const cursor = ta?.selectionStart ?? internal.value.length;
    const after = internal.value.slice(cursor);
    internal.value = before + after;
    emit('update:modelValue', internal.value);
  }
  atState.value = null;

  // Resolve the file via Shell_ReadFile, then route to the matching
  // ingestion path.
  try {
    const res = await client.shell.readFile(path);
    const filename = path.split('/').pop() ?? path;
    if (res.mediaType === 'application/pdf' || res.mediaType.startsWith('image/')) {
      // We already have base64; bypass the FileReader round-trip.
      if (totalStagedBytes() > TOTAL_CAP) {
        localError.value = 'Total staged attachments exceed 30 MiB; remove one first.';
        return;
      }
      const sid = props.sessionId ?? '';
      const att = await client.attachments.addMedia(
        'session',
        sid,
        res.dataBase64,
        res.mediaType,
        filename,
      );
      // Build a thumbnail from the base64 by reconstituting a Blob.
      const binary = atob(res.dataBase64);
      const bytes = new Uint8Array(binary.length);
      for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
      const blob = new Blob([bytes], { type: res.mediaType });
      const previewUrl = URL.createObjectURL(blob);
      objectUrls.push(previewUrl);
      pending.value = [
        ...pending.value,
        {
          kind: res.mediaType.startsWith('image/') ? 'image' : 'document',
          attachmentId: att.id,
          mediaType: res.mediaType,
          filename,
          base64: res.dataBase64,
          previewUrl,
        },
      ];
    } else {
      // text snapshot
      const text = atob(res.dataBase64);
      if (text.length > TEXT_CAP) {
        localError.value = `${filename}: file too large; library is for reference snippets.`;
        return;
      }
      const sha = await sha256Hex(text);
      const sid = props.sessionId ?? '';
      const att = await client.attachments.add({
        scopeKind: 'session',
        scopeId: sid,
        contentSource: `inline:${sha}`,
        content: text,
        kind: 'system',
      });
      pending.value = [
        ...pending.value,
        {
          kind: 'text',
          attachmentId: att.id,
          filename,
          size: text.length,
        },
      ];
    }
  } catch (e) {
    localError.value = e instanceof Error ? e.message : String(e);
  }
}

// ── slash-command autocomplete ───────────────────────────────────────
//
// When the trimmed input starts with `/` AND the user is still typing
// the command name (no whitespace yet), we render the slash dropdown.
// The command list is fetched once on mount; filtering happens
// in-component (cheap O(n) over the static list).
//
// `slashFilteredCount` mirrors the autocomplete component's filtered
// length so the keydown handler can clamp activeIndex without poking
// at the child's exposed handle on every keystroke.

const slashCommands = ref<readonly SlashCommandInfo[]>([]);
const slashActiveIndex = ref(0);
const slashFilteredCount = ref(0);

const slashAutocompleteRef = ref<InstanceType<typeof SlashAutocomplete> | null>(
  null,
);

onMounted(() => {
  // Best-effort fetch — when the harness boots without a wired
  // slashcmd registry (test harness path), the surface returns an
  // empty list. The composer renders nothing and stays usable.
  void client.slash
    .list()
    .then((list) => {
      slashCommands.value = list;
    })
    .catch(() => {
      slashCommands.value = [];
    });
});

/**
 * slashState — derived computed: when non-null, the dropdown opens.
 *
 *   - Trimmed input must begin with `/`.
 *   - `query` is the post-`/` token (no whitespace); empty is fine
 *     (open dropdown shows every command).
 *   - As soon as the user types a space (passing into args land),
 *     the dropdown closes — args are command-specific and not
 *     covered by v1 autocomplete.
 */
interface SlashState {
  query: string;
}

const slashState = computed<SlashState | null>(() => {
  const trimmed = internal.value.trimStart();
  if (!trimmed.startsWith('/')) return null;
  const body = trimmed.slice(1);
  // Whitespace inside `body` means the user is now typing args; the
  // dropdown should close so it doesn't shadow the textarea.
  if (/\s/.test(body)) return null;
  return { query: body };
});

// Recompute the filtered length whenever query / commands change so
// the parent's keyboard nav has a clamp to compare against.
watch(
  [slashState, slashCommands],
  () => {
    const s = slashState.value;
    const list = slashCommands.value;
    if (!s || list.length === 0) {
      slashFilteredCount.value = 0;
      slashActiveIndex.value = 0;
      return;
    }
    const q = s.query.toLowerCase();
    const count =
      q === ''
        ? list.length
        : list.filter((c) => c.name.toLowerCase().startsWith(q)).length;
    slashFilteredCount.value = count;
    if (slashActiveIndex.value >= count) {
      slashActiveIndex.value = 0;
    }
  },
  { immediate: true },
);

function commitSlashCommand(name: string) {
  // Replace the typed prefix with the canonical "/<name> ".
  internal.value = `/${name} `;
  emit('update:modelValue', internal.value);
  slashActiveIndex.value = 0;
  // Re-focus the textarea so the user can keep typing args.
  const ta = textareaRef.value;
  if (ta) {
    ta.focus();
    // Move cursor to the end.
    queueMicrotask(() => {
      ta.selectionStart = ta.value.length;
      ta.selectionEnd = ta.value.length;
    });
  }
}

// ── textarea event handlers ──────────────────────────────────────────

const textareaRef = ref<HTMLTextAreaElement | null>(null);

function onInput(ev: Event) {
  const t = ev.target as HTMLTextAreaElement;
  internal.value = t.value;
  emit('update:modelValue', t.value);
  void refreshAtSuggestions();
}

function send() {
  if (!canSend.value) return;
  const text = trimmed.value;
  // Slash-command branch: when the trimmed input begins with `/`,
  // emit the slashCommand event INSTEAD of send / sendBlocks. The
  // parent dispatches via client.slash.execute and renders the
  // result inline. This branch fires before the multimodal blocks
  // are assembled — staged attachments are NOT consumed (they
  // remain pending so the user can send them as a normal message
  // afterwards).
  if (text.startsWith('/')) {
    emit('slashCommand', text);
    internal.value = '';
    emit('update:modelValue', '');
    slashActiveIndex.value = 0;
    return;
  }
  // Build content blocks: image + document blocks first, then a final
  // text block if any text was typed.
  const contentBlocks: ContentBlock[] = [];
  for (const p of pending.value) {
    if (p.kind === 'image') {
      contentBlocks.push({
        type: 'image',
        source: {
          kind: 'base64',
          media_type: p.mediaType,
          data: p.base64,
          original_name: p.filename,
        },
      });
    } else if (p.kind === 'document') {
      contentBlocks.push({
        type: 'document',
        source: {
          kind: 'base64',
          media_type: p.mediaType,
          data: p.base64,
          original_name: p.filename,
        },
      });
    }
    // text-snapshot pending items are already attachments; not added
    // as content blocks (they ride the resolved-context path).
  }
  if (text.length > 0) {
    contentBlocks.push({ type: 'text', text });
  }

  if (contentBlocks.length > 0) {
    emit('sendBlocks', contentBlocks);
  }
  emit('send', text);

  // Clear local state; revoke object URLs.
  internal.value = '';
  emit('update:modelValue', '');
  pending.value = [];
  revokeAllObjectUrls();
  atState.value = null;
}

function onKeydown(ev: KeyboardEvent) {
  // Slash command autocomplete — handle Up/Down/Tab/Enter/Escape
  // before the @filepath path so /foo and @bar can't fight each
  // other. Slash and at autocomplete are mutually exclusive in
  // practice (the leading-/ check excludes leading-@).
  if (slashState.value && slashFilteredCount.value > 0) {
    if (ev.key === 'ArrowDown') {
      ev.preventDefault();
      slashActiveIndex.value =
        (slashActiveIndex.value + 1) % slashFilteredCount.value;
      return;
    }
    if (ev.key === 'ArrowUp') {
      ev.preventDefault();
      const len = slashFilteredCount.value;
      slashActiveIndex.value = (slashActiveIndex.value - 1 + len) % len;
      return;
    }
    if (ev.key === 'Tab') {
      ev.preventDefault();
      const filtered = slashAutocompleteRef.value?.filtered ?? [];
      const choice = filtered[slashActiveIndex.value] ?? filtered[0];
      if (choice) {
        commitSlashCommand(choice.name);
      }
      return;
    }
    if (ev.key === 'Escape') {
      ev.preventDefault();
      // Closing the dropdown without committing; the user can
      // continue typing or backspace the leading slash to dismiss
      // permanently. We achieve "close" by clamping the count to 0
      // until the next input event recomputes.
      slashFilteredCount.value = 0;
      return;
    }
    // Enter falls through to the unified Enter handler below; it
    // checks slashState and commits the highlighted entry instead
    // of sending.
  }
  // @filepath: Tab or Enter on the first option commits it.
  if (atState.value && atState.value.options.length > 0) {
    if (ev.key === 'Tab') {
      ev.preventDefault();
      const choice = atState.value.options[atState.value.activeIndex] ?? atState.value.options[0];
      if (choice && !choice.endsWith('/')) {
        void commitAtPath(choice);
      }
      return;
    }
    if (ev.key === 'ArrowDown') {
      ev.preventDefault();
      atState.value = {
        ...atState.value,
        activeIndex:
          (atState.value.activeIndex + 1) % atState.value.options.length,
      };
      return;
    }
    if (ev.key === 'ArrowUp') {
      ev.preventDefault();
      const len = atState.value.options.length;
      atState.value = {
        ...atState.value,
        activeIndex: (atState.value.activeIndex - 1 + len) % len,
      };
      return;
    }
    if (ev.key === 'Escape') {
      ev.preventDefault();
      atState.value = null;
      return;
    }
  }
  if (ev.key === 'Enter' && !ev.shiftKey) {
    // Slash autocomplete: when the dropdown is open AND there are
    // filtered options, Enter commits the active one rather than
    // submitting the half-typed command.
    if (slashState.value && slashFilteredCount.value > 0) {
      const filtered = slashAutocompleteRef.value?.filtered ?? [];
      const choice = filtered[slashActiveIndex.value];
      // Only intercept Enter when the user has not yet typed the
      // full command name — i.e. there's still ambiguity to resolve.
      // Once the typed query EXACTLY matches a registered command,
      // Enter falls through to send so the command actually
      // executes.
      const typed = slashState.value.query.toLowerCase();
      const exact = filtered.find((c) => c.name.toLowerCase() === typed);
      if (!exact && choice) {
        ev.preventDefault();
        commitSlashCommand(choice.name);
        return;
      }
    }
    // If the user pressed Enter while an @<token> is staged AND the
    // first option is an exact file (no trailing /), commit it
    // instead of sending. Otherwise, send.
    if (atState.value && atState.value.options.length > 0) {
      const choice = atState.value.options[atState.value.activeIndex];
      if (choice && !choice.endsWith('/')) {
        ev.preventDefault();
        void commitAtPath(choice);
        return;
      }
    }
    ev.preventDefault();
    send();
  }
}

function cancel() {
  emit('cancel');
}

const acceptedTypes =
  'image/*,application/pdf,text/*,application/json,.go,.py,.js,.ts,.vue,.md,.yaml,.yml,.toml,.sh';
</script>

<template>
  <form
    ref="dropZoneRef"
    class="border-t border-border-muted bg-surface-1 px-4 py-3 relative"
    :class="isOverDrop ? 'ring-2 ring-accent' : ''"
    role="group"
    aria-label="Compose message"
    data-testid="chat-input-form"
    @submit.prevent="send"
  >
    <!-- pending attachments preview row -->
    <div
      v-if="pending.length > 0"
      class="flex flex-wrap gap-2 mb-2"
      data-testid="chat-input-pending"
    >
      <div
        v-for="(p, idx) in pending"
        :key="p.attachmentId"
        class="relative flex items-center gap-2 px-2 py-1 rounded-md border border-border-muted bg-surface-2 font-ui text-[12px] text-ink"
        :data-testid="`pending-attachment-${idx}`"
      >
        <img
          v-if="p.kind === 'image'"
          :src="p.previewUrl"
          :alt="p.filename"
          class="block w-12 h-12 object-cover rounded-sm"
          :data-testid="`pending-thumb-${idx}`"
        />
        <FileText
          v-else
          class="w-4 h-4 text-ink-dim"
        />
        <span class="truncate max-w-[20ch]">{{ p.filename }}</span>
        <button
          type="button"
          class="text-ink-dim hover:text-signal-danger"
          :aria-label="`Remove ${p.filename}`"
          :data-testid="`pending-remove-${idx}`"
          @click="removePending(idx)"
        >
          <X class="w-3.5 h-3.5" />
        </button>
      </div>
    </div>

    <!-- error banner -->
    <div
      v-if="localError || errorBanner"
      class="mb-2 rounded-md border border-signal-warn bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-warn"
      role="alert"
      data-testid="chat-input-error"
    >
      {{ localError || errorBanner }}
    </div>

    <label class="sr-only" for="chat-input">Message</label>
    <div class="relative">
      <textarea
        id="chat-input"
        ref="textareaRef"
        :value="internal"
        :disabled="isDisabled"
        :placeholder="placeholder ?? 'Send a message — Enter to send, Shift+Enter for newline. Drop files or use @path.'"
        class="block w-full resize-none bg-surface-2 text-ink font-ui text-sm leading-relaxed rounded-md border border-border-muted px-3 py-2 pr-10 outline-none focus:border-accent disabled:opacity-60 disabled:cursor-not-allowed"
        rows="3"
        aria-label="Message"
        aria-multiline="true"
        @input="onInput"
        @keydown="onKeydown"
      ></textarea>
      <button
        type="button"
        class="absolute right-2 top-2 text-ink-dim hover:text-accent"
        :aria-label="'Attach file'"
        :disabled="isDisabled"
        data-testid="chat-input-paperclip"
        @click="openFileDialog"
      >
        <Paperclip class="w-4 h-4" />
      </button>
      <input
        ref="fileInput"
        type="file"
        multiple
        :accept="acceptedTypes"
        class="hidden"
        data-testid="chat-input-file"
        @change="onFileInputChange"
      />

      <!-- slash-command autocomplete dropdown -->
      <SlashAutocomplete
        v-if="slashState"
        ref="slashAutocompleteRef"
        :commands="slashCommands"
        :query="slashState.query"
        :active-index="slashActiveIndex"
        @select="commitSlashCommand"
      />

      <!-- @filepath autocomplete dropdown -->
      <ul
        v-if="atState && atState.options.length > 0"
        class="absolute left-0 right-0 bottom-full mb-1 z-30 max-h-60 overflow-y-auto rounded-md border border-border-muted bg-surface-1 shadow-lg"
        data-testid="at-filepath-suggestions"
      >
        <li
          v-for="(opt, i) in atState.options"
          :key="opt"
          class="px-3 py-1 font-mono text-[12px] cursor-pointer"
          :class="i === atState.activeIndex ? 'bg-surface-2 text-accent' : 'text-ink hover:bg-surface-2'"
          :data-testid="`at-filepath-option-${i}`"
          @click="commitAtPath(opt)"
        >
          {{ opt }}
        </li>
      </ul>
    </div>

    <div class="mt-2 flex items-center justify-between font-mono text-[11px] text-ink-subtle">
      <div aria-live="polite">
        <span>{{ tokenLabel }}</span>
        <span aria-hidden="true" class="px-2 text-ink-dim">·</span>
        <span>{{ costLabel }}</span>
        <span
          v-if="streaming"
          class="ml-3 text-accent uppercase tracking-[0.18em]"
          aria-live="polite"
        >
          streaming…
        </span>
      </div>
      <div class="flex items-center gap-2">
        <button
          v-if="streaming"
          type="button"
          class="px-3 py-1 rounded-md border border-signal-danger text-signal-danger font-ui text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2"
          aria-label="Cancel stream"
          @click="cancel"
        >
          cancel
        </button>
        <button
          v-else
          type="submit"
          class="px-3 py-1 rounded-md border border-accent text-accent font-ui text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2 disabled:opacity-50 disabled:cursor-not-allowed"
          :disabled="!canSend"
          aria-label="Send message"
        >
          send
        </button>
      </div>
    </div>
  </form>
</template>
