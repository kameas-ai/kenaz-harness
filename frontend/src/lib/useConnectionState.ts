/**
 * useConnectionState — first-paint state machine + connection-lost
 * tracking. States: connecting → ready ↔ degraded → lost. FR-013, FR-017.
 *
 * The state ref is module-scoped so every consumer reads the same
 * source of truth. ShellStatus polling (useShellStatus) updates it as
 * a side effect; useConnectionState just exposes a readonly view.
 */

import { ref, type Ref, readonly } from 'vue';
import type { ConnectionState } from './types';

const state = ref<ConnectionState>('connecting');

export function setConnectionState(s: ConnectionState): void {
  state.value = s;
}

export function useConnectionState(): Readonly<Ref<ConnectionState>> {
  return readonly(state) as Readonly<Ref<ConnectionState>>;
}
