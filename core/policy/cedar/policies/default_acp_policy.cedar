// default_acp_policy.cedar — ACP envelope dispatch default bundle
// for mission acp-orchestration-integration-01NDFSEX06.
//
// Resource family: ACPEnvelope::"<peer_id>"
// Actions:
//   Action::"acp_send"    — gates ACP_Dispatch before transport
//   Action::"acp_receive" — gates inbound envelopes after transport
//                           delivery (when the receive gate is invoked)
// Context attrs available at eval time:
//   peer_id          (string) — the peer's stable identifier
//   peer_trust_tier  (string) — "pending" | "verified" | "revoked"
//   transport        (string) — "uds" | "http_loopback" | "http_lan"
//   direction        (string) — "send" | "receive"
//
// Default posture (spec FR-007, FR-008):
//   acp_send:
//     - peer_trust_tier == "verified" → permit.
//       The user has explicitly verified and trusted the peer card;
//       dispatch is allowed.
//     - anything else (pending, revoked, or absent) → no rule (falls
//       through to default-deny from this forbid; see below).
//     - peer_trust_tier == "revoked" → forbid (explicit safety net in
//       addition to the implicit NotApplicable / default-deny path).
//
//   acp_receive:
//     - peer_trust_tier == "verified" → permit.
//     - peer_trust_tier == "pending"  → permit (with audit). The
//       receive gate records a KindACPEnvelope event so operators
//       can review pending-peer traffic and revoke if unwanted.
//     - peer_trust_tier == "revoked"  → forbid.
//
// Operators extend this bundle by writing additional `.cedar` files in
// `<DataDir>/policy/`; on-disk files compose with this embedded bundle
// in lexicographic order (Engine.Reload semantics). To open all peers
// for dispatch regardless of trust tier, add a permitting rule in a
// user policy file.
//
// NOTE: the engine runs in default-allow (DefaultDeny=false) posture by
// default. For acp_send this means an unmatched principal still results
// in NotApplicable which the ACP view treats as ALLOW. To make the
// default fail-closed for acp_send, set DefaultDeny=true in settings
// or — preferred — the forbid rule below covers revoked peers and any
// trust tier other than "verified" is explicitly denied:

// permit acp_send to verified peers only.
permit (
    principal == User::"local",
    action == Action::"acp_send",
    resource is ACPEnvelope
) when {
    context.peer_trust_tier == "verified"
};

// forbid acp_send to revoked peers — belt-and-suspenders; revocation
// is also enforced by removing the peer from the registry.
forbid (
    principal == User::"local",
    action == Action::"acp_send",
    resource is ACPEnvelope
) when {
    context.peer_trust_tier == "revoked"
};

// forbid acp_send to unverified (pending) peers so the default-allow
// posture does NOT silently allow dispatching to untrusted agents.
// FR-007: acp_send is permitted ONLY to peers at trust tier >= verified.
forbid (
    principal == User::"local",
    action == Action::"acp_send",
    resource is ACPEnvelope
) when {
    context.peer_trust_tier != "verified"
};

// permit acp_receive from verified peers.
permit (
    principal == User::"local",
    action == Action::"acp_receive",
    resource is ACPEnvelope
) when {
    context.peer_trust_tier == "verified"
};

// permit acp_receive from pending peers (with warning audit event
// recorded by the ACP view layer). FR-008.
permit (
    principal == User::"local",
    action == Action::"acp_receive",
    resource is ACPEnvelope
) when {
    context.peer_trust_tier == "pending"
};

// forbid acp_receive from revoked peers.
forbid (
    principal == User::"local",
    action == Action::"acp_receive",
    resource is ACPEnvelope
) when {
    context.peer_trust_tier == "revoked"
};
