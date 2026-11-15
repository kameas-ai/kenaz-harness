/**
 * audience — a pulled team/org entry's audience label as Fleet serves it
 * (kenaz-fleet spec 018 FR-6 / H-2): "team", "org", and admin-governed org
 * content. A label for people, never an instruction.
 */
import type { ContextNode } from '@/lib/types';

/**
 * audienceLabel — a pulled entry's audience as Fleet serves it, for the
 * tree badge. Empty for local files.
 */
export function audienceLabel(node: Pick<ContextNode, 'audience' | 'governed'>): string {
  switch (node.audience) {
    case 'team':
      return 'Team';
    case 'org':
      return node.governed ? 'Organisation · admin-governed' : 'Organisation';
    case undefined:
    case '':
      return '';
    default:
      // A value this build does not know is shown as served.
      return node.audience;
  }
}
