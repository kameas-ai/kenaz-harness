# Spec: ACP orchestration integration

**Status**: draft · **Owner**: alecfeeman

> Successor to the archived `acp-orchestration-01KQ17ZK` mission. That mission landed the wire-level layer (cards, FSM, peer registry, three transports, CBOR framing, signed-envelope verification via the trust engine). This spec wires the layer up to the rest of the harness — RPC, Cedar, UI, audit — so a user can actually invoke and observe a peer agent.

**Status**: draft · **Owner**: alecfeeman

## 1. Why

`core/acp/` is roughly 80% built and 0% integrated. The bytes-in / bytes-out layer is done; what's missing is:

- An **RPC surface** beyond the stub that lets sessions discover peers and dispatch turns.
- A **Cedar resource type** for ACP envelopes so policy can gate what a session is allowed to send to a peer.
- A **frontend** affordance so users can see peers, trust them, and inspect cross-agent traffic.
- An **audit thread** so cross-agent calls are observable post-hoc.
- A worked **bundled scenario** (one agent invoking another over UDS on the same machine) that proves the integration without requiring a remote endpoint.

Until those pieces ship, the trust engine has nothing to verify in production, the transports have no live traffic, and the platform's multi-agent thesis is asserted-not-demonstrated. This is the highest-leverage way to turn invested code into a visible capability.

## 2. Goals

- New RPC verbs: `ACP_ListPeers`, `ACP_TrustPeer`, `ACP_RevokePeer`, `ACP_Dispatch(peer_id, turn_payload)`, `ACP_GetTrace(envelope_id)`.
- Cedar resource type `ACP::Envelope` with attributes `peer_id`, `peer_trust_tier`, `transport`, gated by new actions `Action::"acp_send"`, `Action::"acp_receive"`.
- Settings → Peers panel: list known peers, see trust state, paste a card to trust, revoke trust, inspect last-seen envelopes.
- Audit log integration: every envelope sent or received emits an `acp_envelope` kind with peer_id + transport + outcome (no payload bytes).
- Bundled demo: a `core/acp/demo/` package that runs a UDS-server peer alongside the main session and demonstrates a full envelope round-trip.

## 3. Non-goals

- Public peer discovery over WAN. UDS + HTTP-loopback + HTTP-LAN only (matching what transports already exist).
- Federation / multi-hop routing. Direct peer-to-peer only.
- Multi-agent orchestration patterns (debate, voting, planning consensus). This mission wires the substrate; orchestration patterns are downstream.
- Embedding the ACP UI inside the chat surface. Settings-only for v1 of the integration.

## 4. Functional requirements

### RPC

| ID | Requirement | Status |
|---|---|---|
| FR-001 | `ACP_ListPeers()` returns `[]Peer{ID, CardFingerprint, Transport, TrustTier, LastSeen}`. | proposed |
| FR-002 | `ACP_TrustPeer(card_blob)` verifies the signed card via the trust engine and adds the peer if valid; returns the resulting trust tier. | proposed |
| FR-003 | `ACP_RevokePeer(peer_id)` removes the peer + invalidates outstanding envelopes. | proposed |
| FR-004 | `ACP_Dispatch(peer_id, turn_payload)` builds an envelope, runs Cedar check, sends via the matched transport, returns an envelope_id. | proposed |
| FR-005 | `ACP_GetTrace(envelope_id)` returns the full envelope lifecycle including signing/verifying timestamps and any error. | proposed |

### Policy

| ID | Requirement | Status |
|---|---|---|
| FR-006 | New Cedar resource type `ACP::Envelope` with `peer_id`, `peer_trust_tier`, `transport`, `direction` attributes. | proposed |
| FR-007 | Actions `Action::"acp_send"` and `Action::"acp_receive"` are gated before any byte is signed / verified. Deny → no transport call. | proposed |
| FR-008 | Default policy permits `acp_send` only to peers at trust tier ≥ `verified`; permits `acp_receive` from any tier ≥ `pending` but with a warning audit event. | proposed |

### UI

| ID | Requirement | Status |
|---|---|---|
| FR-009 | Settings → Peers panel lists peers; per-row actions: Trust / Revoke / Inspect. | proposed |
| FR-010 | "Inspect" opens a drawer showing the last N envelopes (kind + timestamp + outcome) with a link into the audit log. | proposed |
| FR-011 | A new top-level `peers://` route renders the same panel full-screen for accessibility. | proposed |

### Audit + demo

| ID | Requirement | Status |
|---|---|---|
| FR-012 | `core/context/audit/audit.go` gains `KindACPEnvelope` with payload struct `{EnvelopeID, PeerID, Transport, Direction, Outcome, BytesIn, BytesOut, ErrorCode}`. No payload bytes. | proposed |
| FR-013 | `core/acp/demo/` ships a small server peer + integration test that round-trips an envelope via UDS and asserts the audit + trace fields. | proposed |

## 5. Open questions

- **Peer cards: how does a user obtain one?** For v1, paste-the-blob is the only UX. A future mission can layer QR codes / one-time tokens / fleet-mediated trust.
- **Envelope size limits.** Set a conservative ceiling (1 MB?) and surface clear errors. Tune later when a workload demands more.
- **Streaming over ACP.** Out of scope — request/response only for v1.
- **Cross-harness version compatibility.** Stamp envelopes with a wire-format version and refuse mismatches; cleanup story is post-1.0.

## 6. Acceptance criteria

- Two harnesses on the same host (one acting as peer via UDS) can trust each other via pasted cards, exchange a turn envelope, and both record matching audit events.
- A Cedar policy denying `acp_send` to a specific peer reliably blocks the send before any byte hits the transport.
- The Peers panel renders the live peer list and a freshly-revoked peer disappears within one event tick.
- The bundled `core/acp/demo/` test passes in CI under `-race -short`.
