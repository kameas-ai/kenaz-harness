/**
 * undelivered-message-retry (owner dogfood 2026-10-07): the pure half —
 * deriving NOT DELIVERED from persisted run outcomes and live closes, and
 * the copy the badge/banner show.
 */
import { describe, it, expect } from 'vitest';
import {
  deliveryCopy,
  failureFromClosed,
  isAutoRetryable,
  needsSettings,
  undeliveredFromRuns,
} from '@/lib/delivery';
import type { TurnRun } from '@/lib/types';

function run(o: Partial<TurnRun>): TurnRun {
  return {
    runId: 'r',
    turnSpanId: 'u',
    graphId: 'chat_default',
    specDigest: 'sha256:x',
    createdAt: '2026-10-07T00:00:00Z',
    ...o,
  };
}

const PAY = {
  outcome: 'failed',
  delivered: false,
  failureClass: 'user_actionable',
  failureCode: 'payment_required',
  failureStatus: 402,
  failureProvider: 'openrouter',
  failureSummary: 'Out of credits with OpenRouter',
  failureMessage: 'This request requires more credits.',
};

describe('undeliveredFromRuns', () => {
  it('marks a message whose newest run failed before the model accepted it', () => {
    const m = undeliveredFromRuns([run({ runId: 'r1', turnSpanId: 'u1', ...PAY })]);
    expect(m.get('u1')?.code).toBe('payment_required');
    expect(m.get('u1')?.summary).toBe('Out of credits with OpenRouter');
  });

  it('a later delivered run of the same message (the Retry) clears it', () => {
    const m = undeliveredFromRuns([
      run({ runId: 'r1', turnSpanId: 'u1', ...PAY }),
      run({ runId: 'r2', turnSpanId: 'u1', outcome: 'completed', delivered: true }),
    ]);
    expect(m.size).toBe(0);
  });

  it('a later delivered run of ANOTHER message clears it too — it carried it in history', () => {
    const m = undeliveredFromRuns([
      run({ runId: 'r1', turnSpanId: 'u1', ...PAY }),
      run({ runId: 'r2', turnSpanId: 'u2', outcome: 'completed', delivered: true }),
    ]);
    expect(m.size).toBe(0);
  });

  it('a delivered run BEFORE the failure does not clear it', () => {
    const m = undeliveredFromRuns([
      run({ runId: 'r0', turnSpanId: 'u0', outcome: 'completed', delivered: true }),
      run({ runId: 'r1', turnSpanId: 'u1', ...PAY }),
    ]);
    expect([...m.keys()]).toEqual(['u1']);
  });

  it('runs with no outcome (in flight / pre-0344) never mark a message', () => {
    expect(undeliveredFromRuns([run({ runId: 'old', turnSpanId: 'u1' })]).size).toBe(0);
    expect(undeliveredFromRuns([run({ runId: 'x', turnSpanId: 'u1', outcome: '' })]).size).toBe(0);
  });

  it('a mid-stream failure (delivered) is not "not delivered"', () => {
    const m = undeliveredFromRuns([
      run({ turnSpanId: 'u1', outcome: 'failed', delivered: true, failureClass: 'transient' }),
    ]);
    expect(m.size).toBe(0);
  });
});

describe('failureFromClosed', () => {
  it('reads the classified failure off a not-delivered close', () => {
    const f = failureFromClosed({
      reason: 'backend-error',
      turn_span_id: 'u1',
      delivered: false,
      failure_class: 'transient',
      failure_code: 'rate_limited',
      failure_status: 429,
      failure_summary: 'OpenRouter is rate-limiting requests',
    });
    expect(f).toMatchObject({ turnSpanId: 'u1', failureClass: 'transient', code: 'rate_limited', status: 429 });
    expect(isAutoRetryable(f)).toBe(true);
  });

  it('delivered, completed, or a backend without the field is not a failure', () => {
    expect(failureFromClosed({ reason: 'backend-error', turn_span_id: 'u1', delivered: true })).toBeNull();
    expect(failureFromClosed({ reason: 'completed', turn_span_id: 'u1', delivered: false })).toBeNull();
    expect(failureFromClosed({ reason: 'backend-error', turn_span_id: 'u1' })).toBeNull();
  });

  it('user_actionable is never auto-retryable', () => {
    const f = failureFromClosed({
      reason: 'backend-error', turn_span_id: 'u1', delivered: false,
      failure_class: 'user_actionable', failure_code: 'payment_required',
    });
    expect(isAutoRetryable(f)).toBe(false);
  });
});

describe('copy', () => {
  it('names the provider and the remedy', () => {
    expect(
      deliveryCopy({
        turnSpanId: 'u', failureClass: 'user_actionable', code: 'payment_required',
        summary: 'Out of credits with OpenRouter',
      }),
    ).toBe('Not delivered — Out of credits with OpenRouter. Add credits, then retry.');
  });

  it('offers settings only for key/permission problems', () => {
    const base = { turnSpanId: 'u', failureClass: 'user_actionable' as const, summary: 's' };
    expect(needsSettings({ ...base, code: 'auth_invalid' })).toBe(true);
    expect(needsSettings({ ...base, code: 'forbidden' })).toBe(true);
    expect(needsSettings({ ...base, code: 'payment_required' })).toBe(false);
  });
});
