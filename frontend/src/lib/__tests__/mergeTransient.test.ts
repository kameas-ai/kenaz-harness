import { describe, it, expect } from 'vitest';
import { mergeTransientByTime } from '@/lib/mergeTransient';

const m = (id: string, createdAt: string) => ({ id, createdAt });

describe('mergeTransientByTime (dogfood 2026-10-08 round 2)', () => {
  it('a slash result sits where it happened, not pinned below newer turns', () => {
    const persisted = [
      m('u1', '2026-10-08T21:00:00Z'),
      m('a1', '2026-10-08T21:00:05Z'),
      m('u2', '2026-10-08T21:02:00Z'),
      m('a2', '2026-10-08T21:02:09Z'),
    ];
    const transient = [m('slash-err', '2026-10-08T21:01:00Z')];
    expect(mergeTransientByTime(persisted, transient).map((x) => x.id)).toEqual([
      'u1', 'a1', 'slash-err', 'u2', 'a2',
    ]);
  });

  it('keeps persisted order and transient order, and appends the freshest last', () => {
    const persisted = [m('u1', '2026-10-08T21:00:00Z'), m('a1', '2026-10-08T21:00:05Z')];
    const transient = [m('t1', '2026-10-08T21:03:00Z'), m('t2', '2026-10-08T21:03:01Z')];
    expect(mergeTransientByTime(persisted, transient).map((x) => x.id)).toEqual(['u1', 'a1', 't1', 't2']);
  });

  it('an unparseable time falls back to the end (the previous behaviour)', () => {
    const persisted = [m('u1', '2026-10-08T21:00:00Z')];
    expect(mergeTransientByTime(persisted, [m('t', 'nope')]).map((x) => x.id)).toEqual(['u1', 't']);
  });
});
