// kenaz-harness default LLM fallback routing policy
// (model-fallback-routing-01NDFSEX04 WP03).
//
// Stance: default-allow. Any configured fallback chain may be used by
// the local user. Operators who want fail-closed behaviour (no automatic
// fallback) should add a forbid rule scoped to the FallbackChain resource
// type, e.g.:
//
//   forbid (
//     principal == User::"local",
//     action == Action::"llm.fallback",
//     resource
//   );
//
// To deny a specific chain only:
//
//   forbid (
//     principal == User::"local",
//     action == Action::"llm.fallback",
//     resource == FallbackChain::"my-chain-id"
//   );

// Permit any fallback chain hop by default for the local user.
permit (
    principal == User::"local",
    action == Action::"llm.fallback",
    resource
);
