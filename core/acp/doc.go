// Package acp is the harness's single public seam for the A2A v1.0
// agent-to-agent protocol (Linux Foundation, released 2026-03-12).
//
// # Architectural invariant (DIRECTIVE_001 / FR-015 / C-001)
//
// Only the sub-package core/acp/envelope is permitted to import the
// third-party SDK github.com/a2aproject/a2a-go. Every other package — the
// peer registry, transports, client, server, store, events, verifier, and
// any consumer outside core/acp — depends only on the SDK-agnostic types
// and interfaces defined in this package.
//
// The package layout is:
//
//	core/acp/
//	├── acp.go                  // public types and interfaces (this file's package)
//	├── errors.go               // typed error taxonomy
//	├── peers/                  // bundle-declared peer registry + AgentCard cache
//	├── server/                 // inbound A2A endpoint coordinator
//	├── client/                 // outbound dispatch coordinator
//	├── envelope/               // SOLE importer of a2a-go (SDK wrapper)
//	├── transports/{uds, http_loopback, http_lan, http_public}/
//	├── events/                 // typed emit helpers into core/event
//	├── store/                  // persistence adapter for tasks + messages
//	├── verify/                 // signed-card verification seam (FR-020)
//	└── internal/               // shared helpers (skill router, ULID minting)
//
// Adding or upgrading a transport touches its own sub-package plus a single
// registration call inside core/acp/envelope. Adding or replacing the SDK
// touches only core/acp/envelope. The rest of core/ is invariant under
// these changes.
package acp
