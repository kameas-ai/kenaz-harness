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
export const HarnessClientKey: InjectionKey<HarnessClient> =
  Symbol('HarnessClient');

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
