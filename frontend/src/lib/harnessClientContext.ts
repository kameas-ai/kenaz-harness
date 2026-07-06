import { type App, type InjectionKey, inject } from 'vue';
import {
  createFakeHarnessClient,
  type HarnessClient,
} from './harnessClient';

/**
 * Vue provide/inject token for the typed RPC client. Components and
 * composables consume the client through `useHarnessClient()`; tests
 * swap a fake via `provideFakeClient(seed)`. FR-007 / FR-008 / SC-006.
 */
// Use Symbol.for so the symbol identity survives Vite HMR. A plain
// `Symbol(...)` would mint a fresh identity each time this module hot-
// replaces, causing inject() in newly-mounted components to miss the
// provide() that ran with the OLD symbol at boot — surfacing as
// "useHarnessClient called outside of a HarnessClient provider".
export const HarnessClientKey: InjectionKey<HarnessClient> =
  Symbol.for('kenaz.harnessClient') as InjectionKey<HarnessClient>;

export function installHarnessClient(app: App, client: HarnessClient): void {
  app.provide(HarnessClientKey, client);
}

export function useHarnessClient(): HarnessClient {
  const c = inject(HarnessClientKey);
  if (!c) {
    throw new Error(
      'useHarnessClient called outside of a HarnessClient provider — install one with installHarnessClient or provideFakeClient.',
    );
  }
  return c;
}

/** Test helper: provide a fake client to a Vue test app. */
export function provideFakeClient(
  app: App,
  seed: Partial<HarnessClient> = {},
): HarnessClient {
  const client = createFakeHarnessClient(seed);
  app.provide(HarnessClientKey, client);
  return client;
}
