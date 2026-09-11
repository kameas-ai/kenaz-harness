/**
 * harnessClient.wp06Overlay.test.ts — served-mode-is-a-real-mode-01PMZ707
 * WP06. Pins the two "wrapValue holes" the 2026-08-18 closing sweep found
 * uncovered (spec.md §1.7 item 2, §5.6):
 *
 *  1. AC-715 — no non-function, non-object value survives
 *     createUnsupportedServedClient() unwrapped.
 *  2. G-703 / AC-716 — SERVED_STREAM_TOPICS (TS) and passthroughTopics
 *     (Go, core/serve/wsstream.go) agree.
 *
 *     Findings #63/#62 (served-topic-single-source, 2026-09) collapsed
 *     what this describe block used to check by hand: SERVED_STREAM_TOPICS
 *     is no longer an independently-authored array here — it is
 *     re-exported from frontend/src/lib/servedStreamTopics.gen.ts, which
 *     `go generate ./core/serve/...` derives directly from
 *     passthroughTopics (via serve.PassthroughTopics()). The hand-copied
 *     `EXPECTED_GO_PASSTHROUGH_TOPICS` mirror this block used to carry —
 *     a THIRD hand-maintained copy of the same list, alongside
 *     passthroughTopics and SERVED_STREAM_TOPICS itself — is deleted:
 *     comparing a generated value against a second hand-copy would have
 *     reintroduced exactly the drift class this finding closes. Parity
 *     is now enforced by scripts/ci/check-codegen.sh regenerating and
 *     byte-diffing servedStreamTopics.gen.ts, which is strictly stronger
 *     than this test's old regex-based set-equality check (it catches
 *     ordering/comment/format drift too, not just membership). The
 *     surviving assertion below is a cheap same-job sanity check that
 *     the re-export actually resolved to real content, not a parity
 *     check — that job now belongs entirely to check-codegen.sh.
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
  // Parity itself is scripts/ci/check-codegen.sh's job now (see the
  // module doc comment above) — it regenerates
  // servedStreamTopics.gen.ts from the live Go source and byte-diffs it
  // against what's committed, which is strictly stronger than a
  // hand-maintained expected-list comparison. This is a same-job sanity
  // check that the generated re-export actually has content, catching
  // the failure mode where the import path is wrong or the generated
  // file was accidentally emptied.
  it('SERVED_STREAM_TOPICS is a non-empty generated set with no duplicates', () => {
    expect(SERVED_STREAM_TOPICS.length).toBeGreaterThan(0);
    expect(new Set(SERVED_STREAM_TOPICS).size).toBe(SERVED_STREAM_TOPICS.length);
  });
});
