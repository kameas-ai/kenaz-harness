/**
 * mergeTransientByTime — place transient (never-persisted) transcript
 * entries, e.g. a slash command's result bubble, among the persisted
 * messages by time.
 *
 * Persisted order is authoritative and never changed; each transient
 * entry goes before the first persisted message created strictly after
 * it, keeping transient entries in their own order. An entry or message
 * whose createdAt does not parse is treated as "now", so a transient
 * entry with no usable time lands at the end.
 */
export interface Timed {
  createdAt: string;
}

function timeOf(iso: string): number {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? Number.POSITIVE_INFINITY : t;
}

export function mergeTransientByTime<T extends Timed>(
  persisted: readonly T[],
  transient: readonly T[],
): T[] {
  if (transient.length === 0) return [...persisted];
  const out: T[] = [];
  let ti = 0;
  for (const p of persisted) {
    const pt = timeOf(p.createdAt);
    while (ti < transient.length && timeOf(transient[ti].createdAt) < pt) {
      out.push(transient[ti]);
      ti++;
    }
    out.push(p);
  }
  while (ti < transient.length) {
    out.push(transient[ti]);
    ti++;
  }
  return out;
}
