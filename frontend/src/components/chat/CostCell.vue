<script setup lang="ts">
/**
 * CostCell renders the per-session cumulative cost pill in the footer
 * status bar (token-cost-telemetry-01KQ8TD7 WP04).
 *
 * Display rules (FR-005b "we don't lie"):
 *   - costSource === 'unknown': show token count only, no dollar figure
 *   - costSource === 'provider': show exact "$X.XX · Nk tokens"
 *   - costSource === 'derived' | 'mixed': show "~$X.XX · Nk tokens"
 */
import type { SessionUsage } from '../../lib/types';

const props = withDefaults(
  defineProps<{
    usage: SessionUsage | null;
    loading?: boolean;
  }>(),
  { loading: false },
);

function formatTokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'k';
  return String(n);
}

function formatCost(usd: number): string {
  if (usd < 0.0001) return '<$0.01';
  if (usd < 0.01) return '$' + usd.toFixed(4);
  if (usd < 1) return '$' + usd.toFixed(3);
  if (usd < 10) return '$' + usd.toFixed(2);
  return '$' + usd.toFixed(1);
}

const label = (() => {
  const u = props.usage;
  if (!u || u.messageCount === 0) return null;
  const tokens = formatTokens(u.totalTokens);
  if (u.costSource === 'unknown' || u.costUsd === 0) return tokens;
  const prefix = u.costSource === 'provider' ? '' : '~';
  return `${prefix}${formatCost(u.costUsd)} · ${tokens}`;
})();

const title = (() => {
  const u = props.usage;
  if (!u || u.messageCount === 0) return 'No usage data yet';
  const src =
    u.costSource === 'provider'
      ? 'Provider-reported (exact)'
      : u.costSource === 'derived'
        ? 'Estimated from pricing table'
        : u.costSource === 'mixed'
          ? 'Mix of provider-reported and estimated'
          : 'Unknown cost source';
  return [
    `${u.promptTokens.toLocaleString()} prompt + ${u.completionTokens.toLocaleString()} completion = ${u.totalTokens.toLocaleString()} tokens`,
    u.costUsd > 0 ? `$${u.costUsd.toFixed(6)} USD — ${src}` : src,
    u.pricingDataDate ? `Pricing data: ${u.pricingDataDate}` : '',
  ]
    .filter(Boolean)
    .join('\n');
})();
</script>

<template>
  <div
    v-if="!loading && label"
    class="flex items-center gap-1.5"
    data-testid="session-cost-cell"
    :title="title"
  >
    <span class="uppercase tracking-[0.14em] text-ink-subtle">cost</span>
    <span class="font-mono text-ink-muted tabular-nums">{{ label }}</span>
  </div>
</template>
