/**
 * useSession.streamTruncatedCopy.test.ts — served-mode-is-a-real-mode-01PMZ707
 * WP08 (SD-15, AC-719's sibling finding for wsstream.go's
 * StreamTruncatedPayload.Reason). Pins the "give it a consumer" disposition:
 * `reason` now selects UI copy, with a fallback to the server's own
 * `message` for any reason value this table does not recognise.
 */
import { describe, it, expect } from 'vitest';
import { streamTruncatedCopy, STREAM_TRUNCATED_COPY } from '@/lib/useSession';

describe('streamTruncatedCopy', () => {
  it('returns the mapped copy for a known reason', () => {
    const copy = streamTruncatedCopy({
      dropped: 3,
      reason: 'slow-consumer',
      message: 'server prose that should NOT be shown for this reason',
    });
    expect(copy).toBe(STREAM_TRUNCATED_COPY['slow-consumer']);
    expect(copy).not.toBe('server prose that should NOT be shown for this reason');
  });

  // *Falsify*: delete the 'slow-consumer' entry from STREAM_TRUNCATED_COPY
  // → the assertion above (copy !== server message) goes red because the
  // fallback below would kick in for a reason that should be mapped.

  it('falls back to the server message for an unrecognised reason', () => {
    const copy = streamTruncatedCopy({
      dropped: 1,
      reason: 'some-future-reason-not-yet-mapped',
      message: 'a fresh server-composed sentence',
    });
    expect(copy).toBe('a fresh server-composed sentence');
  });
});
