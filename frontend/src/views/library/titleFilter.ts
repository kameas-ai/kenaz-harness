/**
 * Shared Library chrome (artifacts-as-units-01DOGF0C WP06, FR-7): the one
 * search box above both views filters each view's list by title. Kept as a
 * plain function so Captured (ArtifactsView) and Authored (DocumentsView)
 * apply exactly the same rule.
 */

/** Case-insensitive substring match; an empty/blank query matches all. */
export function matchesTitle(title: string, query: string | undefined): boolean {
  const q = (query ?? '').trim().toLowerCase();
  if (q === '') return true;
  return title.toLowerCase().includes(q);
}

export function filterByTitle<T extends { title: string }>(
  items: readonly T[],
  query: string | undefined,
): T[] {
  return items.filter((it) => matchesTitle(it.title, query));
}
