/**
 * documentsErrors.ts — parse the Documents_* error contract.
 *
 * contracts/documents-rpc.md §4: every Documents_* failure carries
 * "documents: <code>: <message>". The desktop transport hands that string
 * over untouched; the served transport prefixes it with
 * "servedTransport: <method>: ". Messages are fixed per code and never carry
 * document text, so they are safe to show verbatim.
 */

export type DocumentsErrorCode =
  | 'document_not_found'
  | 'invalid_title'
  | 'empty_body'
  | 'no_session'
  | 'version_conflict'
  | 'content_too_large'
  | 'content_too_nested'
  | 'content_not_utf8'
  | 'invalid_site_slug'
  | 'no_documents'
  | 'too_many_documents'
  | 'export_target_in_use'
  | 'bad_params'
  | 'unavailable'
  | 'internal_error';

export interface DocumentsError {
  code: DocumentsErrorCode | string;
  message: string;
}

const PATTERN = /documents: ([a-z_]+): (.*)$/s;

/**
 * parseDocumentsError returns the contract code and message, or null when
 * the failure did not come from the Documents surface (a network error, a
 * served-mode refusal) — callers then show the raw message.
 */
export function parseDocumentsError(err: unknown): DocumentsError | null {
  const raw = err instanceof Error ? err.message : typeof err === 'string' ? err : '';
  const m = PATTERN.exec(raw);
  if (!m) return null;
  return { code: m[1], message: m[2] };
}

/** documentsErrorMessage is the text to show for any documents failure. */
export function documentsErrorMessage(err: unknown): string {
  const parsed = parseDocumentsError(err);
  if (parsed) return parsed.message;
  if (err instanceof Error) return err.message;
  return String(err);
}
