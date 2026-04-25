/**
 * Model-family detection used by the chat-surface model switcher.
 *
 * Sessions are blocked from switching across families because models
 * in different families use different tokenizers / training mixes —
 * a Claude conversation would read incoherent if mid-stream we asked
 * Llama to continue it. Same family (e.g. Claude Sonnet ⇄ Claude
 * Opus) is fine: same tokenizer, same context format.
 *
 * Heuristic by kind:
 *   - anthropic         → 'claude'
 *   - openai            → 'gpt'
 *   - openrouter        → vendor segment of "vendor/model" id
 *   - bedrock           → strip leading region prefix ("us." / "eu.")
 *                          then take the first dot-segment
 *                          ("anthropic" / "meta" / "amazon" / "mistral" / …)
 *   - ollama / unknown  → first identifier token before '-' or ':'
 */

import type { Provider } from './types';

export type ModelFamily = string;

export function inferFamily(
  kind: string | undefined,
  modelId: string,
): ModelFamily {
  const id = (modelId || '').trim();
  if (!id) return kind || 'unknown';
  switch (kind) {
    case 'anthropic':
      return 'claude';
    case 'openai':
      return 'gpt';
    case 'openrouter': {
      const slash = id.indexOf('/');
      return slash >= 0 ? id.slice(0, slash).toLowerCase() : 'openrouter';
    }
    case 'bedrock': {
      // Strip a leading 2-letter region prefix the inference-profile
      // ids have ("us.meta.…", "eu.anthropic.…").
      const stripped = id.replace(/^[a-z]{2}\./, '');
      const dot = stripped.indexOf('.');
      return dot > 0 ? stripped.slice(0, dot).toLowerCase() : 'bedrock';
    }
    case 'ollama':
    default: {
      // first token before '-' or ':' — "llama3.1:8b" → "llama3.1"
      // close enough for an unknown-source heuristic.
      const colon = id.indexOf(':');
      const dash = id.indexOf('-');
      const idx = [colon, dash].filter((i) => i >= 0).sort((a, b) => a - b)[0];
      return (idx !== undefined ? id.slice(0, idx) : id).toLowerCase();
    }
  }
}

export interface ModelChoice {
  providerId: string;
  providerName: string;
  modelId: string;
  family: ModelFamily;
}

/**
 * flattenChoices expands every (provider, modelId) pair across all
 * configured providers. The session's model-switcher pill consumes
 * this list and filters by family.
 */
export function flattenChoices(providers: readonly Provider[]): ModelChoice[] {
  const out: ModelChoice[] = [];
  for (const p of providers) {
    const models =
      p.models && p.models.length > 0 ? p.models : p.model ? [p.model] : [];
    for (const m of models) {
      out.push({
        providerId: p.id,
        providerName: p.name || p.id,
        modelId: m,
        family: inferFamily(p.kind, m),
      });
    }
  }
  return out;
}
