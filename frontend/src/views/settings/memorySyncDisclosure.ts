/**
 * The memory-sync disclosure points (corrected in v0.106.2, revision
 * "2026-11-14" — fleet.MemoryDisclosureRevision). ONE source for both places
 * that show them: the off-state disclosure in MemorySyncPanel and the one-time
 * "What changed about memory sync" notice shown to members who turned sync on
 * under the earlier text. Change the wording here only, and bump
 * MemoryDisclosureRevision in core/fleet/memory_sync.go when it changes in
 * substance so members with sync on are asked to read it again.
 */
export interface MemorySyncDisclosurePoint {
  /** Stable id, used in data-testid attributes. */
  id: 'view' | 'storage' | 'org' | 'connector' | 'leave';
  text: string;
}

export const MEMORY_SYNC_DISCLOSURE_POINTS: readonly MemorySyncDisclosurePoint[] = [
  {
    id: 'view',
    text:
      'Only you can view your synced notes in Fleet today. Kameas staff have no view of note ' +
      'content in the product.',
  },
  {
    id: 'storage',
    text:
      "They're stored on Kameas servers in a form Kameas can read. They're encrypted in transit " +
      'and at rest, but not end-to-end, and our systems read them to keep your devices in sync.',
  },
  {
    id: 'org',
    text:
      "Your organization's admins and team leads can't read them unless your organization turns " +
      'that on and you first acknowledge a notice. Only notes synced after that point would be ' +
      "readable, and you'd be able to see who read them.",
  },
  {
    id: 'connector',
    text:
      'If your organization enables the Kameas connector and you opt in, apps you connect to it ' +
      'can read and update these notes.',
  },
  {
    id: 'leave',
    text:
      'If you leave or are removed from your organization, your synced notes are deleted from ' +
      'Fleet.',
  },
];
