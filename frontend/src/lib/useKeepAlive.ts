/**
 * useKeepAlive — per-session UI-state cache (scroll position, draft
 * input). FR-002, plan §4.1. In-memory only; persisted summaries
 * round-trip through RPC for restart resilience (NOT settings.json
 * per privacy CI invariant #5).
 */

import { reactive } from 'vue';

interface SessionUIState {
  scroll?: number;
  draftInput?: string;
  expandedPanels?: string[];
  lastViewedMessageId?: string;
}

const cache: Record<string, SessionUIState> = reactive({});

export function useKeepAlive(sessionId: string) {
  if (!cache[sessionId]) cache[sessionId] = {};
  const state = cache[sessionId];

  return {
    get scroll(): number {
      return state.scroll ?? 0;
    },
    set scroll(v: number) {
      state.scroll = v;
    },
    get draftInput(): string {
      return state.draftInput ?? '';
    },
    set draftInput(v: string) {
      state.draftInput = v;
    },
  };
}
