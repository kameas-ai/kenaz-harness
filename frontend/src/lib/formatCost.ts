/**
 * formatCost — the one USD rendering for token-cost figures (footer
 * CostCell, per-message TokenMeterChip, scheduled-run outcomes).
 * Precision scales with magnitude so small per-call costs stay legible.
 */
export function formatCost(usd: number): string {
  if (usd < 0.0001) return '<$0.01';
  if (usd < 0.01) return '$' + usd.toFixed(4);
  if (usd < 1) return '$' + usd.toFixed(3);
  if (usd < 10) return '$' + usd.toFixed(2);
  return '$' + usd.toFixed(1);
}
