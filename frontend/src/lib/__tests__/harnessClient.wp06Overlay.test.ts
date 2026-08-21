/**
 * harnessClient.wp06Overlay.test.ts — served-mode-is-a-real-mode-01PMZ707
 * WP06. Pins the two "wrapValue holes" the 2026-08-18 closing sweep found
 * uncovered (spec.md §1.7 item 2, §5.6):
 *
 *  1. AC-715 — no non-function, non-object value survives
 *     createUnsupportedServedClient() unwrapped.
 *  2. G-703 / AC-716 — SERVED_STREAM_TOPICS (TS) and passthroughTopics
 *     (Go, core/serve/wsstream.go) agree. The Go side already has its own
 *     cross-check (wsstream_topics_parity_test.go, which parses this
 *     file's array literal out of the TS source); this is the TS side's
 *     own local assertion so frontend CI catches drift in the same job a
 *     SERVED_STREAM_TOPICS edit lands in, not only in the separate Go job.
 *
 * Also documents `research/served-client-overlay.md`'s finding: today
 * every leaf of createFakeHarnessClient()'s output is a function or a
 * sub-client object — zero arrays, zero primitives — so
 * createUnsupportedServedClient() must construct without throwing.
 */
import { describe, it, expect } from 'vitest';
import {
  createUnsupportedServedClient,
  createFakeHarnessClient,
  wrapUnsupportedValue,
  SERVED_STREAM_TOPICS,
} from '@/lib/harnessClient';

describe('wrapUnsupportedValue (WP06, AC-715)', () => {
  it('wraps a function into a rejector', async () => {
    const wrapped = wrapUnsupportedValue(() => 'real result', 'x.y') as (
      ...args: unknown[]
    ) => Promise<unknown>;
    await expect(wrapped()).rejects.toThrow(/x\.y/);
  });

  it('recurses into a plain object (sub-client boundary)', () => {
    const wrapped = wrapUnsupportedValue({ inner: () => 1 }, 'x') as Record<
      string,
      (...args: unknown[]) => Promise<unknown>
    >;
    expect(typeof wrapped.inner).toBe('function');
  });

  // *Falsify* AC-715 directly: a string, a number, a boolean, an array,
  // and null must all be rejected at the wrap boundary rather than
  // silently returned — the exact shape the old `return val;` fall-through
  // let leak as fake data.
  it.each([
    ['string', 'fake-token'],
    ['number', 42],
    ['boolean', true],
    ['array', ['a', 'b']],
    ['null', null],
  ])('throws instead of passing a bare %s through unwrapped', (_label, value) => {
    expect(() => wrapUnsupportedValue(value, 'some.leaked.path')).toThrow(
      /some\.leaked\.path/,
    );
  });

  it('createUnsupportedServedClient() constructs cleanly today (zero holes)', () => {
    // If this throws, HarnessClient has grown a plain-data field somewhere
    // and it needs an explicit case (see research/served-client-overlay.md).
    expect(() => createUnsupportedServedClient()).not.toThrow();
  });
});

describe('confirm overlay is total (WP06)', () => {
  it('carries every ConfirmClient key the rejecting base declares', () => {
    const base = createUnsupportedServedClient();
    const baseConfirmKeys = Object.keys(
      (createFakeHarnessClient() as unknown as { confirm: object }).confirm,
    ).sort();
    const servedConfirmKeys = Object.keys(
      (base as unknown as { confirm: object }).confirm,
    ).sort();
    // *Falsify*: drop `...base.confirm` from the served client's confirm
    // sub-client and add a sixth ConfirmClient method with no explicit
    // overlay entry → this assertion goes red (servedConfirmKeys would be
    // missing the new key, which would otherwise be `undefined` instead
    // of a rejector).
    expect(servedConfirmKeys).toEqual(
      expect.arrayContaining(baseConfirmKeys),
    );
  });
});

describe('SERVED_STREAM_TOPICS ↔ passthroughTopics parity (G-703, AC-716)', () => {
  // Sourced by running `go test ./core/serve/... -run TestPrintPassthroughTopics`
  // against core/serve/wsstream.go's passthroughTopics (RAN, not assumed —
  // see the WP06 commit message / mission report for the exact command).
  // core/serve/wsstream_topics_parity_test.go is the authoritative
  // cross-check; this list is a maintained mirror so this file's own test
  // run catches drift too.
  const EXPECTED_GO_PASSTHROUGH_TOPICS = [
    'llm:stream-chunk',
    'llm:stream-closed',
    'llm:fallback-attempted',
    'session.usage.updated',
    'cost.threshold.crossed',
    'bash:permission-pending',
    'cred:permission-pending',
    'fs:permission-pending',
    'tool:permission-pending',
    'elicit:pending',
    'chat:overflow-recovery',
    'tool:confirm-pending',
    'storage.migration.drift-detected',
  ].sort();

  it('SERVED_STREAM_TOPICS matches the Go-side passthroughTopics set exactly', () => {
    // *Falsify*: add a topic to either list without the other → this goes
    // red (either an extra or a missing entry in the diff).
    expect([...SERVED_STREAM_TOPICS].sort()).toEqual(
      EXPECTED_GO_PASSTHROUGH_TOPICS,
    );
  });
});
