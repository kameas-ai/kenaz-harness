import { inject, type InjectionKey } from 'vue';

/**
 * customRecipeAuthoring.ts — WP02 stop-gap gate: "custom recipe authoring is
 * available".
 *
 * `CustomRecipeTab.vue`'s `save()` has no backend to call —
 * `MCP_SaveCustomRecipe` does not exist yet — so both doors onto that form
 * (`AddMCPServerModal`'s Custom tab, and `KenazToolsPanel`'s per-row Edit
 * button, which lands directly on the Custom tab) are gated behind this
 * single flag. Hiding the tab alone is not enough: the row Edit button is a
 * second, more prominent entry point onto the same broken `save()`.
 *
 * One flag, both doors (CLAUDE.md C-001 / mcp-connector-lifecycle-01PMMC01
 * FR-001): `KenazToolsPanel.vue` and `AddMCPServerModal.vue` both call
 * `useCustomRecipeAuthoringEnabled()` and read the *same* injected value (or,
 * absent a provider, the same `CUSTOM_RECIPE_AUTHORING_ENABLED` default). A
 * test that flips the injected value must move both assertions together —
 * two independent constants would let the doors drift apart silently.
 *
 * RETIREMENT CONDITION (dated interim state, not a permanent default-off):
 * flip `CUSTOM_RECIPE_AUTHORING_ENABLED` to `true` — or delete this module
 * and the `v-if`s that read it — in the same commit that lands
 * `mcp-connector-lifecycle-01PMMC01` WP06 (`MCP_SaveCustomRecipe` persists
 * through `recipes.UserStore.Save`). See `docs/unwired-ledger.md`'s
 * 2026-08-18 entry for the dated justification and owner. Until WP06 lands,
 * this stays `false` in every build — a `true` default would open a door
 * whose handler still throws.
 */
export const CUSTOM_RECIPE_AUTHORING_ENABLED = false;

// Symbol.for so the identity survives Vite HMR — see HarnessClientKey in
// harnessClientContext.ts for why a plain Symbol() would break inject()
// across a hot-reload boundary.
export const CustomRecipeAuthoringKey: InjectionKey<boolean> = Symbol.for(
  'kenaz.customRecipeAuthoringEnabled',
) as InjectionKey<boolean>;

/**
 * useCustomRecipeAuthoringEnabled — the single read site both gated
 * components call. Returns the value provided via
 * `provide(CustomRecipeAuthoringKey, ...)` (tests use this to flip the flag
 * without module-mocking) or, absent a provider (every real app boot),
 * `CUSTOM_RECIPE_AUTHORING_ENABLED`.
 */
export function useCustomRecipeAuthoringEnabled(): boolean {
  return inject(CustomRecipeAuthoringKey, CUSTOM_RECIPE_AUTHORING_ENABLED);
}
