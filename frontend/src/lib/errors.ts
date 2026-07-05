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
   */
  friendly(): string {
    return 'This feature is not available in served mode. Run the harness as a desktop app to use it.';
  }
}

/**
 * isServedUnsupportedError narrows an unknown catch value to ServedUnsupportedError.
 */
export function isServedUnsupportedError(err: unknown): err is ServedUnsupportedError {
  return err instanceof ServedUnsupportedError;
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
