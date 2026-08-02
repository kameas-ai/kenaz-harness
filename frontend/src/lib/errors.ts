/**
 * errors.ts — friendly() mappings for typed backend errors surfaced over
 * the Wails RPC boundary.
 *
 * Go-side typed errors are serialised to plain strings by the Wails error
 * bridge. We match on the error message prefix/pattern and return a
 * localised, action-oriented message that the UI can render directly.
 *
 * (multimodal-io-01KQ8TDF WP04 / FR-021)
 */

/**
 * AttachmentErrorKind discriminates the six attachment pre-flight errors.
 * Mirrors the Go typed error taxonomy in core/llm/errors.go.
 */
export type AttachmentErrorKind =
  | 'too_large'
  | 'mime_unsupported'
  | 'count_exceeded'
  | 'dimension_exceeded'
  | 'encrypted'
  | 'audio_unsupported'
  | 'unknown';

/** Parsed attachment error with enough context for UI rendering. */
export interface ParsedAttachmentError {
  kind: AttachmentErrorKind;
  /** Human-readable message suitable for inline display. */
  friendly: string;
  /** Raw error string from the backend. */
  raw: string;
}

/**
 * parseAttachmentError inspects the raw error string returned by a Wails
 * RPC call and maps it to a ParsedAttachmentError with a localised message.
 *
 * Matching is done against the stable error message prefixes emitted by the
 * Go Error() methods — these are part of the public error contract and must
 * not be changed without updating this function.
 *
 * Returns null when the error is not a recognised attachment error.
 */
export function parseAttachmentError(
  err: unknown,
): ParsedAttachmentError | null {
  const raw = toErrorString(err);
  if (!raw) return null;

  // ErrAttachmentTooLarge: "llm: attachment too large for … (mime=… given=… cap=…)"
  if (raw.startsWith('llm: attachment too large for ')) {
    const provider = extractBetween(raw, 'for ', ' (mime=') ?? '';
    const mime = extractBetween(raw, 'mime=', ' given=') ?? '';
    const given = extractBetween(raw, 'given=', ' cap=') ?? '0';
    const cap = extractBetween(raw, 'cap=', ')') ?? '0';
    const givenMiB = (parseInt(given, 10) / (1024 * 1024)).toFixed(1);
    const capMiB = Math.round(parseInt(cap, 10) / (1024 * 1024));
    const mimeLabel = mime === 'application/pdf' ? 'PDF' : 'attachment';
    return {
      kind: 'too_large',
      friendly: `${mimeLabel} is too large (${givenMiB} MiB). Provider "${provider}" accepts at most ${capMiB} MiB per attachment.`,
      raw,
    };
  }

  // ErrAttachmentMimeUnsupported: "llm: attachment MIME type … not supported by …"
  if (raw.startsWith('llm: attachment MIME type ')) {
    const mime =
      extractBetween(raw, 'llm: attachment MIME type "', '" not supported') ??
      extractBetween(raw, 'MIME type ', ' not supported') ??
      '';
    const provider =
      extractBetween(raw, 'not supported by ', '') ??
      extractAfter(raw, 'not supported by ') ??
      '';
    if (mime === 'application/pdf') {
      return {
        kind: 'mime_unsupported',
        friendly: `Provider "${provider}" does not accept PDF attachments via this API. Switch to Anthropic, Bedrock-Claude, or convert the PDF pages to images.`,
        raw,
      };
    }
    return {
      kind: 'mime_unsupported',
      friendly: `Provider "${provider}" does not support attachment type "${mime}".`,
      raw,
    };
  }

  // ErrAttachmentCountExceeded: "llm: too many image attachments for … (given=… cap=…)"
  if (raw.startsWith('llm: too many image attachments for ')) {
    const provider = extractBetween(raw, 'for ', ' (given=') ?? '';
    const given = extractBetween(raw, 'given=', ' cap=') ?? '0';
    const cap = extractBetween(raw, 'cap=', ')') ?? '0';
    return {
      kind: 'count_exceeded',
      friendly: `Too many images in one message (you have ${given}; provider "${provider}" allows at most ${cap}).`,
      raw,
    };
  }

  // ErrAttachmentDimensionExceeded: "llm: image pixels exceed limit for … (given=… cap=…)"
  if (raw.startsWith('llm: image pixels exceed limit for ')) {
    const provider = extractBetween(raw, 'for ', ' (given=') ?? '';
    const given = extractBetween(raw, 'given=', ' cap=') ?? '0';
    const cap = extractBetween(raw, 'cap=', ')') ?? '0';
    const givenMP = (parseInt(given, 10) / 1_000_000).toFixed(1);
    const capMP = (parseInt(cap, 10) / 1_000_000).toFixed(1);
    return {
      kind: 'dimension_exceeded',
      friendly: `Image is too large (${givenMP} MP). Provider "${provider}" accepts at most ${capMP} MP. Resize the image before attaching.`,
      raw,
    };
  }

  // ErrAttachmentEncrypted: "llm: PDF attachment is password-protected"
  if (raw.startsWith('llm: PDF attachment is password-protected')) {
    return {
      kind: 'encrypted',
      friendly: 'PDF is password-protected. Remove the password and re-attach.',
      raw,
    };
  }

  // ErrAttachmentAudioUnsupported: "llm: audio attachment type … is not supported"
  if (raw.startsWith('llm: audio attachment type ')) {
    return {
      kind: 'audio_unsupported',
      friendly: 'Audio input is not yet supported. Audio is planned for a future release.',
      raw,
    };
  }

  return null;
}

/**
 * friendlyAttachmentError returns a user-facing string for any error that
 * looks like an attachment pre-flight rejection, or null otherwise.
 * Convenience wrapper for `parseAttachmentError`.
 */
export function friendlyAttachmentError(err: unknown): string | null {
  return parseAttachmentError(err)?.friendly ?? null;
}

// ── ErrUnsupportedFeature (provider-implementation-uniformity-01KQ8V4F WP08) ─

/**
 * ParsedUnsupportedFeatureError carries the structured fields from a
 * Go ErrUnsupportedFeature — "llm: feature %q not supported by model %q: %s".
 */
export interface ParsedUnsupportedFeatureError {
  /** The model ID that does not support the feature. */
  modelId: string;
  /** The feature identifier, e.g. "reasoning.openai_effort". */
  feature: string;
  /** Optional hint for the user, e.g. "use /effort 16000 instead". */
  hint: string;
  /** Raw error string from the backend. */
  raw: string;
}

/**
 * parseUnsupportedFeatureError inspects a raw error string and extracts
 * the structured fields when it matches ErrUnsupportedFeature.
 *
 * Matches: "llm: feature %q not supported by model %q[: hint]"
 * Returns null for unrelated errors.
 */
export function parseUnsupportedFeatureError(
  err: unknown,
): ParsedUnsupportedFeatureError | null {
  const raw = toErrorString(err);
  if (!raw) return null;
  // Prefix: 'llm: feature "X" not supported by model "Y"'
  if (!raw.startsWith('llm: feature ')) return null;
  const featureMatch = raw.match(/^llm: feature "([^"]+)" not supported by model "([^"]+)"(?:: (.+))?$/);
  if (!featureMatch) return null;
  return {
    feature: featureMatch[1] ?? '',
    modelId: featureMatch[2] ?? '',
    hint: featureMatch[3] ?? '',
    raw,
  };
}

/**
 * friendlyUnsupportedFeatureError returns a user-facing string for any
 * ErrUnsupportedFeature error, or null for unrelated errors.
 */
export function friendlyUnsupportedFeatureError(err: unknown): string | null {
  const parsed = parseUnsupportedFeatureError(err);
  if (!parsed) return null;
  if (parsed.hint) {
    return `${parsed.hint}`;
  }
  return `Feature "${parsed.feature}" is not supported by model "${parsed.modelId}".`;
}

// ── Structured RPC error envelope (agent-loop-robustness-parity WP08) ────

/**
 * RPCErrorEnvelope mirrors core/rpc.RPCError. When the backend returns one
 * of the typed LLM errors it maps it to this envelope and embeds it in
 * the broker event payload (future) or as a JSON-encoded Wails error body.
 *
 * Code vocabulary:
 *   "auth"              — authentication / API-key failure
 *   "transient"         — recoverable transient failure
 *   "budget_exhausted"  — retry budget consumed
 *   "context_overflow"  — context window exceeded
 *   "invalid_request"   — malformed request
 *   "internal"          — unexpected internal error
 */
export interface RPCErrorEnvelope {
  code: string;
  message: string;
  hint?: string;
  retryable: boolean;
}

/**
 * parseRPCError attempts to parse a structured RPCErrorEnvelope from the
 * given value. Returns null when the value does not conform to the envelope
 * shape (e.g. a plain string error from an older binding or a non-LLM path).
 *
 * The envelope can arrive as:
 *   - an RPCErrorEnvelope object (when the binding returns one directly)
 *   - a JSON string encoding of RPCErrorEnvelope (when Wails serialises it
 *     as the error body of a rejected promise)
 */
export function parseRPCError(err: unknown): RPCErrorEnvelope | null {
  if (!err) return null;

  // Direct object: check for the required fields.
  if (typeof err === 'object' && err !== null) {
    const e = err as Record<string, unknown>;
    if (typeof e['code'] === 'string' && typeof e['message'] === 'string' && typeof e['retryable'] === 'boolean') {
      return {
        code: e['code'] as string,
        message: e['message'] as string,
        hint: typeof e['hint'] === 'string' ? (e['hint'] as string) : undefined,
        retryable: e['retryable'] as boolean,
      };
    }
  }

  // JSON string: try to parse.
  //
  // Wails rejects with a bare string, but the served HTTP transport
  // rejects with an Error whose message carries the same envelope. Read
  // through Error.message so both transports surface the same hint —
  // otherwise served mode would show a raw Go JSON blob where the desktop
  // shows "Rotate the key in Kenaz".
  const raw = err instanceof Error ? err.message : err;
  if (typeof raw === 'string') {
    try {
      const parsed = JSON.parse(raw) as Record<string, unknown>;
      if (typeof parsed['code'] === 'string' && typeof parsed['message'] === 'string' && typeof parsed['retryable'] === 'boolean') {
        return {
          code: parsed['code'] as string,
          message: parsed['message'] as string,
          hint: typeof parsed['hint'] === 'string' ? (parsed['hint'] as string) : undefined,
          retryable: parsed['retryable'] as boolean,
        };
      }
    } catch {
      // Not JSON — fall through.
    }
  }

  return null;
}

/**
 * friendlyRPCError returns a user-facing string from a structured envelope,
 * preferring the Hint when present. Returns null when err is not a recognised
 * RPCErrorEnvelope (callers should fall back to their existing error parsers).
 */
export function friendlyRPCError(err: unknown): string | null {
  const envelope = parseRPCError(err);
  if (!envelope) return null;
  return envelope.hint || envelope.message;
}

// ── ServedUnsupportedError (served-mode-honesty-WZQR1ZJE WP01) ───────────

/**
 * ServedUnsupportedError is thrown by createUnsupportedServedClient() for
 * every RPC method that is NOT wired to the real HTTP/WS transport in served
 * mode.  Components catch this error and render an honest "not available in
 * served mode" state instead of fabricated data.
 *
 * Use isServedUnsupportedError() to narrow an unknown catch value.
 */
export class ServedUnsupportedError extends Error {
  /** The RPC method name that was called. */
  readonly method: string;

  constructor(method: string) {
    super(`served mode: "${method}" is not available in served mode`);
    this.name = 'ServedUnsupportedError';
    this.method = method;
    // Maintain proper prototype chain in transpiled envs.
    Object.setPrototypeOf(this, new.target.prototype);
  }

  /**
   * friendly returns a short, user-facing message suitable for display in a
   * "not available" badge or empty-state panel.
   *
   * It names the method, because the reader is usually someone who just
   * clicked something and needs to know WHAT failed, and it never tells
   * them to "run the desktop app": this harness is the default app inside
   * a Kenaz workbench, and its user frequently has no desktop harness to
   * run. Advice you cannot act on is worse than no advice.
   */
  friendly(): string {
    return `"${this.method}" is not wired into the in-workbench harness yet — this is a gap in the served build, not something you can enable from here. Sessions do work: create a conversation, send a message, watch it stream, and stop it.`;
  }
}

/**
 * isServedUnsupportedError narrows an unknown catch value to ServedUnsupportedError.
 */
export function isServedUnsupportedError(err: unknown): err is ServedUnsupportedError {
  return err instanceof ServedUnsupportedError;
}

// ── General-purpose friendly() helper ────────────────────────────────────

/**
 * friendly — converts any unknown caught error into a user-displayable string.
 *
 * Priority:
 *   1. Structured RPC error envelope (agent-loop-robustness-parity WP08).
 *   2. Known typed errors (attachment, unsupported-feature, served-mode).
 *   3. Short raw errors (< 200 chars): show verbatim.
 *   4. Long errors: truncate + append "… (check logs for details)".
 *
 * Use this everywhere you would otherwise write:
 *   `err instanceof Error ? err.message : String(err)`
 *
 * Components should NOT render `err.message` directly in templates.
 * Always call `friendly(err)` so Go-internal error strings are humanised.
 */
export function friendly(err: unknown): string {
  const rpc = friendlyRPCError(err);
  if (rpc) return rpc;
  const attachment = friendlyAttachmentError(err);
  if (attachment) return attachment;
  const unsupported = friendlyUnsupportedFeatureError(err);
  if (unsupported) return unsupported;
  if (isServedUnsupportedError(err)) return err.friendly();
  const raw = toErrorString(err);
  if (!raw) return 'An unexpected error occurred.';
  if (raw.length <= 200) return raw;
  return raw.slice(0, 197) + '…';
}

// ── helpers ──────────────────────────────────────────────────────────────

function toErrorString(err: unknown): string {
  if (!err) return '';
  if (typeof err === 'string') return err;
  if (err instanceof Error) return err.message;
  return String(err);
}

function extractBetween(s: string, start: string, end: string): string | null {
  const i = s.indexOf(start);
  if (i === -1) return null;
  const from = i + start.length;
  if (!end) return s.slice(from);
  const j = s.indexOf(end, from);
  if (j === -1) return null;
  return s.slice(from, j);
}

function extractAfter(s: string, prefix: string): string | null {
  const i = s.indexOf(prefix);
  if (i === -1) return null;
  return s.slice(i + prefix.length);
}
