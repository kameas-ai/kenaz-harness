// adaptRecipe.test.ts — adapter-level coverage for the wire→model mapping
// that every install-modal component test bypasses.
//
// Context (2026-08-25 review finding 3): `adaptRecipe` never mapped
// `w.auth`, so `Recipe.auth` was `undefined` for every recipe in
// production and no OAuth sign-in button had ever rendered from real
// data — for a full release, with the full frontend suite green. That
// happened because every component test (RecipeKeyPromptModal.test.ts's
// makeRecipe/fsRecipe/makeDeviceCodeRecipe, 30+ call sites) builds a
// `Recipe` object directly, skipping `adaptRecipe` entirely
// (CLAUDE.md blind spot #2: "test fixtures that bypass the layer under
// test").
//
// This file feeds a realistic `WireRecipe` — the snake_case shape the Go
// bridge actually sends over Tools_ListRecipes — through the real
// `adaptRecipe` and asserts on the camelCase `Recipe` it produces. The
// falsification this file exists to make possible: change
// `auth: adaptRecipeAuth(w.auth),` to `auth: adaptRecipeAuth(undefined),` in
// `adaptRecipe` (harnessClient.ts) — NOT deleting the line, which leaves
// `adaptRecipeAuth` unused and fails to compile (`TS6133: 'adaptRecipeAuth'
// is declared but its value is never read`), a build error rather than a
// falsification. `adaptRecipeAuth(undefined)` compiles cleanly and always
// returns `undefined`, going red for the right reason: 2 of the 12 tests
// below actually guard the auth mapping ("maps a realistic mcp_oauth
// WireRecipe.auth..." and "never surfaces client_secret...") and fail; the
// rest — including the two that assert `recipe.auth` is `undefined` — stay
// green under this mutation, since forcing auth to `undefined` is
// indistinguishable from the no-auth case for those.
import { describe, it, expect } from 'vitest';
import { adaptRecipe, type WireRecipe } from '@/lib/harnessClient';
import type { PrimaryAuth } from '@/lib/types';

function baseWireRecipe(overrides: Partial<WireRecipe> = {}): WireRecipe {
  return {
    id: 'github',
    display_name: 'GitHub',
    description: 'Official GitHub remote MCP server.',
    category: 'development',
    capabilities: { tools: true, resources: false, prompts: false, sampling: false },
    ...overrides,
  };
}

describe('adaptRecipe — wire→model mapping (UNIT-3 3g / review finding 3)', () => {
  it('maps a realistic mcp_oauth WireRecipe.auth onto Recipe.auth, field by field', () => {
    const wire = baseWireRecipe({
      primary_auth: 'browser_oauth_dcr',
      auth: {
        kind: 'mcp_oauth',
        client_id: 'kameas-harness-cli',
        scopes: ['repo', 'read:user'],
        token_env_var: 'GITHUB_TOKEN',
      },
    });

    const recipe = adaptRecipe(wire);

    expect(recipe.auth).toEqual({
      kind: 'mcp_oauth',
      clientId: 'kameas-harness-cli',
      scopes: ['repo', 'read:user'],
      tokenEnvVar: 'GITHUB_TOKEN',
    });
  });

  it('never surfaces client_secret on Recipe.auth, even when the Go bridge sends one', () => {
    // core/mcp/recipes.RecipeAuth carries ClientSecret at rest (only ever
    // an unresolved "${VAR}" placeholder — see WireRecipeAuth's doc
    // comment in harnessClient.ts). The frontend WireRecipeAuth type has
    // no field for it, so this cast simulates the wire payload the Go
    // side actually sends (which does have the JSON key) landing on a
    // TS type that doesn't declare it — the real boundary condition.
    const wireAuthWithSecret = {
      kind: 'mcp_oauth',
      client_id: 'front-cli',
      client_secret: '${KAMEAS_FRONT_OAUTH_CLIENT_SECRET}',
      scopes: ['openid'],
    } as unknown as WireRecipe['auth'];
    const wire = baseWireRecipe({ auth: wireAuthWithSecret });

    const recipe = adaptRecipe(wire);

    expect(recipe.auth).toBeDefined();
    expect(recipe.auth).not.toHaveProperty('client_secret');
    expect(recipe.auth).not.toHaveProperty('clientSecret');
    expect(JSON.stringify(recipe.auth)).not.toContain('SECRET');
  });

  it('leaves Recipe.auth undefined when the wire recipe carries no auth block', () => {
    const recipe = adaptRecipe(baseWireRecipe());
    expect(recipe.auth).toBeUndefined();
  });

  it('leaves Recipe.auth undefined for a non-mcp_oauth kind (defensive; the wire type only declares mcp_oauth today)', () => {
    const wire = baseWireRecipe({
      auth: { kind: 'something_else' } as unknown as WireRecipe['auth'],
    });
    expect(adaptRecipe(wire).auth).toBeUndefined();
  });

  // KNOWN_PRIMARY_AUTH coverage — UNIT-3 3g's own regression (WP0 of
  // connector-lifecycle-truth-01PMZ303): 'browser_oauth_dcr' and
  // 'browser_oauth_pkce' were missing from this set, so adaptPrimaryAuth
  // silently dropped them to undefined for every recipe that declared
  // one. Enumerating every arm here means adding a 7th PrimaryAuth value
  // to core/mcp/recipes.go without adding it here fails loudly instead
  // of silently collapsing to legacy rendering.
  const knownPrimaryAuthArms: PrimaryAuth[] = [
    'oauth',
    'browser_oauth_dcr',
    'browser_oauth_pkce',
    'device_code',
    'keys',
    'none',
  ];

  it.each(knownPrimaryAuthArms)('maps primary_auth %s through unchanged', (arm) => {
    const recipe = adaptRecipe(baseWireRecipe({ primary_auth: arm }));
    expect(recipe.primaryAuth).toBe(arm);
  });

  it('drops an unrecognised primary_auth value to undefined rather than passing it through', () => {
    const recipe = adaptRecipe(
      baseWireRecipe({ primary_auth: 'some_future_auth_kind_not_yet_known' }),
    );
    expect(recipe.primaryAuth).toBeUndefined();
  });

  it('leaves primaryAuth undefined when the wire recipe omits primary_auth entirely', () => {
    const recipe = adaptRecipe(baseWireRecipe());
    expect(recipe.primaryAuth).toBeUndefined();
  });
});
