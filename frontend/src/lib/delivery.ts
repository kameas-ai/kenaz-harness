/**
 * delivery — did a user message actually reach the model?
 * (undelivered-message-retry, owner dogfood 2026-10-07.)
 *
 * The owner's OpenRouter account ran out of credits; the provider answered
 * 402 before a single token; the chat showed a transient "Send failed"
 * banner and nothing on the message. The message WAS persisted (the append
 * precedes the run), so after adding credits the only way forward was to
 * retype it — a duplicate user row — because nothing said it had never
 * reached the model and nothing offered to re-run it.
 *
 * The backend now records, per chat run, whether the model accepted the
 * request (`delivered`) and a classified failure
 * (core/llm.ClassifyFailure) — live on `llm:stream-closed`, durably on
 * `Sessions_TurnRuns` (migration sessions/0344). This module turns either
 * into the per-message NOT DELIVERED state the transcript renders, and
 * holds the copy and the auto-retry schedule.
 */

import type { TurnRun } from './types';

/** llm.FailureClass. */
export type FailureClass = 'user_actionable' | 'transient' | 'unknown' | '';

/**
 * Why a message did not reach the model. `turnSpanId` is the persisted
 * user message id (a run's turn span IS its user row's id).
 */
export interface DeliveryFailure {
  turnSpanId: string;
  /** '' for a run the user stopped themselves (code 'stopped'). */
  failureClass: FailureClass;
  /** Stable discriminator — key behaviour off this, never off text. */
  code: string;
  status?: number;
  provider?: string;
  /** One-line copy, e.g. "Out of credits with OpenRouter". */
  summary: string;
  /** The provider's own explanation, already sanitized server-side. */
  message?: string;
}

/** The code the surface uses for a run the user stopped before any output. */
export const STOPPED_CODE = 'stopped';

/**
 * The `llm:stream-closed` reason for a run the user stopped (the chat
 * runner's explicit Stop arm). useSession commits a partial bubble with
 * this as its streamingError; MessageBubble renders it as "Stopped by
 * you", never as a connection loss.
 */
export const STOP_CALLED_REASON = 'stop-called';

/**
 * session_full is NOT a delivery failure the surface reports here: the
 * conversation no longer fits the context window, a Retry cannot work,
 * and MessageList already has its own session-full banner with the way
 * out. Reporting it as NOT DELIVERED too said the same thing three ways
 * and held the send queue for nothing.
 */
export const SESSION_FULL_CODE = 'session_full';

/**
 * Auto-retry backoff for TRANSIENT failures: three attempts after 2s, 8s
 * and 30s, then the message is surfaced as NOT DELIVERED. A
 * user_actionable failure (out of credits, bad key, unknown model) is
 * NEVER auto-retried — retrying unchanged cannot succeed and would only
 * burn requests.
 */
export const AUTO_RETRY_DELAYS_MS: readonly number[] = [2_000, 8_000, 30_000];

/** True when a failure may be retried automatically. */
export function isAutoRetryable(f: DeliveryFailure | null | undefined): boolean {
  return !!f && f.failureClass === 'transient';
}

/** Key/permission problems — the surface offers "Open settings". */
export function needsSettings(f: DeliveryFailure | null | undefined): boolean {
  return !!f && (f.code === 'auth_invalid' || f.code === 'forbidden');
}

/** The remedy sentence that follows the summary. */
function remedy(code: string): string {
  switch (code) {
    case 'payment_required':
      return 'Add credits, then retry.';
    case 'auth_invalid':
    case 'forbidden':
      return 'Check the API key in provider settings, then retry.';
    case 'model_not_found':
      return 'Pick another model, then retry.';
    case 'invalid_request':
    case 'unsupported':
      return 'Change the request or the model, then retry.';
    case 'rate_limited':
    case 'provider_unavailable':
    case 'network':
    case 'timeout':
      return 'Retry once the provider recovers.';
    case 'session_full':
      return 'Start a new session or compact this one, then retry.';
    case STOPPED_CODE:
      return 'Retry to send it.';
    default:
      return 'Retry to send it again.';
  }
}

/**
 * The badge copy, e.g.
 *   "Not delivered — Out of credits with OpenRouter. Add credits, then retry."
 */
export function deliveryCopy(f: DeliveryFailure): string {
  const summary = f.summary || 'The model request failed';
  return `Not delivered — ${summary}. ${remedy(f.code)}`;
}

/** Wire shape of the delivery fields on `llm:stream-closed`. */
export interface WireClosedDelivery {
  reason?: string;
  turn_span_id?: string;
  delivered?: boolean;
  failure_class?: string;
  failure_code?: string;
  failure_status?: number;
  failure_provider?: string;
  failure_summary?: string;
  failure_message?: string;
}

/**
 * The NOT DELIVERED failure a live close describes, or null when the
 * close does not describe one (delivered, completed, or a backend that
 * predates the field — `delivered` absent is NOT "false").
 */
export function failureFromClosed(p: WireClosedDelivery): DeliveryFailure | null {
  if (p.delivered !== false || !p.turn_span_id) return null;
  if (p.reason === 'completed') return null;
  if (p.failure_code === SESSION_FULL_CODE) return null;
  if (p.reason === STOP_CALLED_REASON) {
    return {
      turnSpanId: p.turn_span_id,
      failureClass: '',
      code: STOPPED_CODE,
      summary: 'Stopped before the model responded',
    };
  }
  return {
    turnSpanId: p.turn_span_id,
    failureClass: (p.failure_class as FailureClass) || 'unknown',
    code: p.failure_code || 'unknown',
    status: p.failure_status || undefined,
    provider: p.failure_provider || undefined,
    summary: p.failure_summary || 'The model request failed',
    message: p.failure_message || undefined,
  };
}

/**
 * undeliveredFromRuns derives the per-message NOT DELIVERED state from the
 * persisted run list (oldest first, as Sessions_TurnRuns returns it).
 *
 * A message (turn span) is undelivered when its NEWEST run ended failed or
 * stopped with delivered=false, AND no run created after that one was
 * delivered — a later delivered run carried the message to the model in
 * its history, so it did arrive. A run with an empty outcome (in flight,
 * or recorded before migration 0344) says nothing, so it never marks a
 * message — an old turn must not suddenly claim it failed to send.
 */
export function undeliveredFromRuns(
  runs: readonly TurnRun[],
): Map<string, DeliveryFailure> {
  const latestBySpan = new Map<string, { run: TurnRun; index: number }>();
  let lastDeliveredIndex = -1;
  runs.forEach((r, index) => {
    if (!r.turnSpanId) return;
    latestBySpan.set(r.turnSpanId, { run: r, index });
    if (r.outcome && r.delivered) lastDeliveredIndex = index;
  });
  const out = new Map<string, DeliveryFailure>();
  for (const [span, { run, index }] of latestBySpan) {
    if (index < lastDeliveredIndex) continue;
    if (run.delivered) continue;
    if (run.failureCode === SESSION_FULL_CODE) continue;
    if (run.outcome === 'failed') {
      out.set(span, {
        turnSpanId: span,
        failureClass: (run.failureClass as FailureClass) || 'unknown',
        code: run.failureCode || 'unknown',
        status: run.failureStatus || undefined,
        provider: run.failureProvider || undefined,
        summary: run.failureSummary || 'The model request failed',
        message: run.failureMessage || undefined,
      });
    } else if (run.outcome === 'stopped') {
      out.set(span, {
        turnSpanId: span,
        failureClass: '',
        code: STOPPED_CODE,
        summary: 'Stopped before the model responded',
      });
    }
  }
  return out;
}
